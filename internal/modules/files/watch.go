package files

import (
	"bytes"
	"cmp"
	"context"
	"crypto/x509"
	"errors"
	"io/fs"
	"os"
	"path"
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
	s, err := w.scan()
	if err != nil {
		return nil, err
	}
	var best *found
	for i, c := range s.certs {
		if cryptox.Covers(cryptox.Names(c.Leaf), req.Domains) && (best == nil || c.Leaf.NotAfter.After(best.Leaf.NotAfter)) {
			best = &s.certs[i]
		}
	}
	switch {
	case best == nil && s.trouble(w) != nil:
		return nil, s.trouble(w)
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
	s, err := w.scan()
	if err != nil {
		return contracts.Message{}, err
	}
	if len(s.certs) == 0 {
		if err := s.trouble(w); err != nil {
			return contracts.Message{}, err
		}
		return contracts.Message{}, &contracts.Problem{Key: i18n.FilesErrorWatchEmpty, Params: map[string]any{"directory": w.dir}}
	}
	var names []string
	for _, c := range s.certs {
		if n := cryptox.Names(c.Leaf); len(n) > 0 && !slices.Contains(names, n[0]) {
			names = append(names, n[0])
		}
	}
	slices.Sort(names)
	return contracts.Message{Key: i18n.FilesTestWatchOk,
		Params: map[string]any{"directory": w.dir, "count": len(names), "domains": strings.Join(names, ", ")}}, nil
}

func (w *watcher) denied(name string) error {
	return &contracts.Problem{Key: i18n.FilesErrorWatchDenied, Params: map[string]any{"path": name}}
}

// scanned is what a scan found, and what kept it from looking further.
type scanned struct {
	certs []found
	// denied is a file the scan could not read, as the keys of other
	// clients are often their owner's alone; outside is a link out of the
	// directory, which it does not follow. Either may explain why it found
	// nothing.
	denied, outside string
}

// trouble explains why s holds no certificate that would do, if it knows.
func (s scanned) trouble(w *watcher) error {
	switch {
	case s.denied != "":
		return w.denied(s.denied)
	case s.outside != "":
		return &contracts.Problem{Key: i18n.FilesErrorWatchOutside, Params: map[string]any{"path": s.outside, "directory": w.dir}}
	}
	return nil
}

// extensions are those of the files a scan reads.
var extensions = []string{".pem", ".crt", ".cer", ".key"}

// scan finds the certificates of the directory that come with their
// private key: in each directory, each key with the certificates there,
// as certbot keeps them (live/<name>/fullchain.pem and privkey.pem) and
// acme.sh too (<domain>/fullchain.cer and <domain>.key). The same leaf
// found twice counts once. It reads through an os.Root, so no link leads
// it out of the directory: certbot's live/ links to archive/, both inside
// /etc/letsencrypt.
func (w *watcher) scan() (scanned, error) {
	var s scanned
	if !filepath.IsAbs(w.dir) {
		return s, retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorRelative, Params: map[string]any{"directory": w.dir}})
	}
	root, err := os.OpenRoot(w.dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, &contracts.Problem{Key: i18n.FilesErrorWatchMissing, Params: map[string]any{"directory": w.dir}, Err: err}
	case errors.Is(err, fs.ErrPermission):
		return s, w.denied(w.dir)
	case err != nil:
		return s, err
	}
	defer root.Close()
	fsys := root.FS()
	show := func(name string) string { return filepath.Join(w.dir, filepath.FromSlash(name)) }

	type file struct {
		name  string
		certs []*x509.Certificate
		key   bool
		data  []byte
	}
	dirs := map[string][]file{}
	read := 0
	err = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		switch {
		case errors.Is(err, fs.ErrPermission):
			s.denied = cmp.Or(s.denied, show(name))
			return nil
		case err != nil:
			return err
		case d.IsDir():
			if name != "." && strings.Count(name, "/") >= scanDepth-1 {
				return fs.SkipDir
			}
			return nil
		case !slices.Contains(extensions, strings.ToLower(path.Ext(name))) || read >= scanFiles:
			return nil
		}
		data, err := readSmall(fsys, name)
		switch {
		case errors.Is(err, fs.ErrPermission):
			s.denied = cmp.Or(s.denied, show(name))
			return nil
		case err != nil && d.Type()&fs.ModeSymlink != 0 && !errors.Is(err, fs.ErrNotExist):
			s.outside = cmp.Or(s.outside, show(name))
			return nil
		case err != nil, data == nil:
			return nil
		}
		read++
		f := file{name: name, data: data}
		if bytes.Contains(data, []byte("PRIVATE KEY-----")) {
			f.key = true
		} else if f.certs, err = cryptox.ParseCertificates(data); err != nil {
			return nil // not a certificate after all
		}
		dirs[path.Dir(name)] = append(dirs[path.Dir(name)], f)
		return nil
	})
	if err != nil {
		return s, err
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
			where := ""
			for _, c := range files {
				if slices.ContainsFunc(c.certs, b.Leaf.Equal) {
					where = show(c.name)
					break
				}
			}
			s.certs = append(s.certs, found{Bundle: b, path: where})
		}
	}
	slices.SortFunc(s.certs, func(a, b found) int { return strings.Compare(a.path, b.path) })
	return s, nil
}

// readSmall reads a regular file of fsys, following links, unless it is
// too big to be a certificate or a key: then nil.
func readSmall(fsys fs.FS, name string) ([]byte, error) {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > scanSize {
		return nil, nil
	}
	return fs.ReadFile(fsys, name)
}
