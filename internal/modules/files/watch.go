package files

import (
	"bytes"
	"cmp"
	"context"
	"crypto/x509"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// Limits of a scan: how deep it goes, how many files it reads, and how big
// a file may be. certbot's /etc/letsencrypt and acme.sh's home fit well.
const (
	scanDepth = 4
	scanFiles = 5000
	scanSize  = 1 << 20
)

// recheck is how long a certificate waits for the other client to renew
// it, once due, before the directory is looked at again.
const recheck = 6 * time.Hour

func watchType() providers.Type {
	return providers.Type{
		ID: "files.watch", Kind: providers.Source, OwnKey: true,
		Name: i18n.FilesWatchName, Description: i18n.FilesWatchDescription,
		Tags: []string{"self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "directory", Label: i18n.FilesFieldWatchDirectory, Help: i18n.FilesFieldWatchDirectoryHelp, Kind: providers.Text, Required: true},
		},
	}
}

// watcher takes in the certificates another ACME client, such as certbot or
// acme.sh, keeps renewing in a directory, with their keys. It only reads.
type watcher struct {
	dir string
	now func() time.Time
}

var (
	_ contracts.KeySource = (*watcher)(nil)
	_ contracts.Tester    = (*watcher)(nil)
)

func watchFactory(_ context.Context, in providers.Instance) (any, error) {
	return &watcher{dir: strings.TrimSpace(in.Config["directory"]), now: time.Now}, nil
}

// Close has nothing to release.
func (*watcher) Close() error { return nil }

// found is a certificate of the directory, with its key and chain.
type found struct {
	*cryptox.Bundle
	path string // of the file the leaf is in
}

// Collect returns the newest certificate of the directory that covers the
// domains, unless it is no newer than the version in use: then the other
// client has yet to renew it, and the certificate waits.
func (w *watcher) Collect(_ context.Context, req contracts.CollectRequest) (*contracts.Collected, error) {
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.FilesLogWatchScan,
		Params: map[string]any{"directory": w.dir, "domains": strings.Join(req.Domains, ", ")}})
	certs, unreadable, err := w.scan()
	if err != nil {
		return nil, err
	}
	var best *found
	for i, c := range certs {
		if covers(cryptox.Names(c.Leaf), req.Domains) && (best == nil || c.Leaf.NotAfter.After(best.Leaf.NotAfter)) {
			best = &certs[i]
		}
	}
	switch {
	case best == nil && unreadable != "":
		return nil, w.denied(unreadable)
	case best == nil, req.Current == "" && !best.Leaf.NotAfter.After(w.now()):
		return nil, &contracts.Problem{Key: i18n.FilesErrorWatchNone,
			Params: map[string]any{"directory": w.dir, "domains": strings.Join(req.Domains, ", ")}}
	case req.Current != "" && (cryptox.Fingerprint(best.Leaf) == req.Current || !best.Leaf.NotAfter.After(req.CurrentEnd)):
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.FilesLogWatchSame,
			Params: map[string]any{"path": best.path, "hours": int(recheck.Hours())}})
		return nil, &contracts.Wait{After: recheck}
	}
	log(contracts.LogLine{Level: contracts.OK, Step: contracts.StepDownload, Key: i18n.FilesLogWatchFound,
		Params: map[string]any{"path": best.path, "date": best.Leaf.NotAfter.Format(time.DateOnly)}})
	return &contracts.Collected{Chain: append([]*x509.Certificate{best.Leaf}, best.Chain...), Key: best.Key}, nil
}

// Test scans the directory and tells what it found.
func (w *watcher) Test(context.Context) (contracts.Message, error) {
	certs, unreadable, err := w.scan()
	if err != nil {
		return contracts.Message{}, err
	}
	if len(certs) == 0 {
		if unreadable != "" {
			return contracts.Message{}, w.denied(unreadable)
		}
		return contracts.Message{}, &contracts.Problem{Key: i18n.FilesErrorWatchEmpty, Params: map[string]any{"directory": w.dir}}
	}
	var names []string
	for _, c := range certs {
		if n := cryptox.Names(c.Leaf); len(n) > 0 && !slices.Contains(names, n[0]) {
			names = append(names, n[0])
		}
	}
	slices.Sort(names)
	return contracts.Message{Key: i18n.FilesTestWatchOk,
		Params: map[string]any{"directory": w.dir, "count": len(names), "domains": strings.Join(names, ", ")}}, nil
}

// covers tells whether names hold each of domains, as the certs module
// checks them: one by one, wildcards as they are, letter case aside.
func covers(names, domains []string) bool {
	for _, d := range domains {
		if !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, d) }) {
			return false
		}
	}
	return len(domains) > 0
}

func (w *watcher) denied(path string) error {
	return &contracts.Problem{Key: i18n.FilesErrorWatchDenied, Params: map[string]any{"path": path}}
}

// scan finds the certificates of the directory that come with their
// private key: in each directory, each key with the certificates there,
// as certbot keeps them (live/<name>/fullchain.pem and privkey.pem) and
// acme.sh too (<domain>/fullchain.cer and <domain>.key). The same leaf
// found twice counts once. unreadable is a file it could not read, if
// any: the keys of other clients are often their owner's alone.
func (w *watcher) scan() (certs []found, unreadable string, err error) {
	if !filepath.IsAbs(w.dir) {
		return nil, "", retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorRelative, Params: map[string]any{"directory": w.dir}})
	}
	type file struct {
		path  string
		certs []*x509.Certificate
		key   bool
		data  []byte
	}
	dirs := map[string][]file{}
	read := 0
	root := filepath.Clean(w.dir)
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case errors.Is(err, fs.ErrPermission):
			unreadable = cmp.Or(unreadable, path)
			return nil
		case err != nil:
			return err
		case d.IsDir():
			if rel, _ := filepath.Rel(root, path); rel != "." && strings.Count(rel, string(filepath.Separator)) >= scanDepth-1 {
				return fs.SkipDir
			}
			return nil
		}
		if !slices.Contains([]string{".pem", ".crt", ".cer", ".key"}, strings.ToLower(filepath.Ext(path))) || read >= scanFiles {
			return nil
		}
		info, err := os.Stat(path) // through the links of certbot's live/
		if err != nil || !info.Mode().IsRegular() || info.Size() > scanSize {
			if errors.Is(err, fs.ErrPermission) {
				unreadable = cmp.Or(unreadable, path)
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				unreadable = cmp.Or(unreadable, path)
			}
			return nil
		}
		read++
		f := file{path: path, data: data}
		if bytes.Contains(data, []byte("PRIVATE KEY-----")) {
			f.key = true
		} else if f.certs, err = cryptox.ParseCertificates(data); err != nil {
			return nil // not a certificate after all
		}
		dirs[filepath.Dir(path)] = append(dirs[filepath.Dir(path)], f)
		return nil
	})
	switch {
	case errors.Is(walkErr, fs.ErrNotExist):
		return nil, "", &contracts.Problem{Key: i18n.FilesErrorWatchMissing, Params: map[string]any{"directory": w.dir}, Err: walkErr}
	case errors.Is(walkErr, fs.ErrPermission):
		return nil, "", w.denied(w.dir)
	case walkErr != nil:
		return nil, "", walkErr
	}

	seen := map[string]bool{}
	for _, files := range dirs {
		var all []*x509.Certificate
		for _, f := range files {
			all = append(all, f.certs...)
		}
		for _, f := range files {
			if !f.key {
				continue
			}
			key, err := cryptox.ParseKey(f.data)
			if err != nil {
				continue
			}
			b, err := cryptox.NewBundle(all, key)
			if err != nil || seen[cryptox.Fingerprint(b.Leaf)] {
				continue
			}
			seen[cryptox.Fingerprint(b.Leaf)] = true
			path := ""
			for _, c := range files {
				if slices.ContainsFunc(c.certs, b.Leaf.Equal) {
					path = c.path
					break
				}
			}
			certs = append(certs, found{Bundle: b, path: path})
		}
	}
	slices.SortFunc(certs, func(a, b found) int { return strings.Compare(a.path, b.path) })
	return certs, unreadable, nil
}
