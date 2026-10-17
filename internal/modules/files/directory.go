package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// Layouts of a directory.
const (
	byDomain = "by_domain" // a subdirectory for each certificate, named after its primary domain
	flat     = "flat"      // the files right in the directory
)

func directoryType() providers.Type {
	return providers.Type{
		ID: "files.directory", Kind: providers.Target, Name: i18n.FilesDirectoryName, Description: i18n.FilesDirectoryDescription,
		Tags: []string{"self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "directory", Label: i18n.FilesFieldDirectory, Help: i18n.FilesFieldDirectoryHelp, Kind: providers.Text, Required: true},
			{Key: "layout", Label: i18n.FilesFieldLayout, Kind: providers.Select, Required: true, Default: byDomain, Options: []providers.Option{
				{Value: byDomain, Label: i18n.FilesLayoutByDomain},
				{Value: flat, Label: i18n.FilesLayoutFlat},
			}},
		},
	}
}

// directory is a directory at work.
type directory struct {
	path   string
	layout string
}

var (
	_ contracts.Target = (*directory)(nil)
	_ contracts.Tester = (*directory)(nil)
)

func factory(_ context.Context, in providers.Instance) (any, error) {
	return &directory{path: strings.TrimSpace(in.Config["directory"]), layout: in.Config["layout"]}, nil
}

// Close has nothing to release.
func (*directory) Close() error { return nil }

// Test makes the directory, as the first deployment would, and writes and
// removes a file there.
func (d *directory) Test(context.Context) (contracts.Message, error) {
	if !filepath.IsAbs(d.path) {
		return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorRelative, Params: map[string]any{"directory": d.path}})
	}
	if err := os.MkdirAll(d.path, 0o755); err != nil { //nolint:gosec // G301: a web server has to reach the files
		return contracts.Message{}, fail(d.path, err)
	}
	f, err := os.CreateTemp(d.path, ".jec-test-*")
	if err != nil {
		return contracts.Message{}, fail(d.path, err)
	}
	_ = f.Close()
	if err := os.Remove(f.Name()); err != nil {
		return contracts.Message{}, fail(d.path, err)
	}
	return contracts.Message{Key: i18n.FilesTestOk, Params: map[string]any{"directory": d.path}}, nil
}

// Deploy writes the files, each through a temporary file renamed over the
// old one, so that a reader never sees half a file. The private key is
// readable by its owner only. Files are overwritten, so there is nothing
// to remove afterwards.
func (d *directory) Deploy(_ context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if !filepath.IsAbs(d.path) {
		return none, retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorRelative, Params: map[string]any{"directory": d.path}})
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("files: no certificate to deploy"))
	}
	dir := d.path
	if d.layout != flat {
		name, err := dirName(req.Domains)
		if err != nil {
			return none, retry.Permanent(err)
		}
		dir = filepath.Join(d.path, name)
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	files := []struct {
		name string
		data []byte
		mode fs.FileMode
	}{
		{"cert.pem", cryptox.EncodeCertificates(req.Chain[0]), 0o644},
		{"chain.pem", cryptox.EncodeCertificates(req.Chain[1:]...), 0o644},
		{"fullchain.pem", cryptox.EncodeCertificates(req.Chain...), 0o644},
		{"privkey.pem", key, 0o600},
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.FilesLogWrite, Params: map[string]any{"directory": dir}})
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: a web server has to reach the files
		return none, fail(dir, err)
	}
	for _, f := range files {
		if err := writeFile(filepath.Join(dir, f.name), f.data, f.mode); err != nil {
			return none, fail(dir, err)
		}
	}
	return contracts.DeployResult{}, nil
}

// dirName names the subdirectory of a certificate after its primary
// domain, as a file name: *.example.com becomes star.example.com.
func dirName(domains []string) (string, error) {
	if len(domains) == 0 {
		return "", errors.New("files: a certificate without domains")
	}
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		}
		return '_'
	}, strings.Replace(strings.ToLower(domains[0]), "*", "star", 1))
	if name == "." || name == ".." || strings.Trim(name, ".") == "" {
		return "", fmt.Errorf("files: %q makes no directory name", domains[0])
	}
	return name, nil
}

// writeFile replaces path with data, through a temporary file in the same
// directory.
func writeFile(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // fails once renamed
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// fail explains an error of the file system: one that retrying cannot fix,
// such as a missing permission, ends the deployment.
func fail(dir string, err error) error {
	problem := &contracts.Problem{Key: i18n.FilesErrorWrite, Params: map[string]any{"directory": dir}, Err: err}
	if errors.Is(err, fs.ErrPermission) {
		return retry.Permanent(problem)
	}
	return problem
}
