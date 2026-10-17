package files

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// Layouts of a web root.
const (
	shared    = "shared"     // one web root serves every domain
	perDomain = "per_domain" // each domain's web root is a subdirectory, named after the domain
)

func webrootType() providers.Type {
	return providers.Type{
		ID: "files.webroot", Kind: providers.Solver, Challenge: providers.ChallengeHTTP,
		Name: i18n.FilesWebrootName, Description: i18n.FilesWebrootDescription,
		Tags: []string{"self_hosted", "http"}, Testable: true,
		Fields: []providers.Field{
			{Key: "directory", Label: i18n.FilesFieldWebroot, Help: i18n.FilesFieldWebrootHelp, Kind: providers.Text, Required: true},
			{Key: "layout", Label: i18n.FilesFieldWebrootLayout, Kind: providers.Select, Required: true, Default: shared, Options: []providers.Option{
				{Value: shared, Label: i18n.FilesWebrootShared},
				{Value: perDomain, Label: i18n.FilesWebrootPerDomain},
			}},
		},
	}
}

// webroot is the directory a local web server serves the domains from,
// where the files of validations go.
type webroot struct {
	path   string
	layout string
}

var (
	_ contracts.FileSolver = (*webroot)(nil)
	_ contracts.Tester     = (*webroot)(nil)
)

func webrootFactory(_ context.Context, in providers.Instance) (any, error) {
	return &webroot{path: strings.TrimSpace(in.Config["directory"]), layout: in.Config["layout"]}, nil
}

// Close has nothing to release.
func (*webroot) Close() error { return nil }

// Test makes the web root, as the first validation would, and writes and
// removes a file there.
func (w *webroot) Test(ctx context.Context) (contracts.Message, error) {
	d := &directory{path: w.path}
	if _, err := d.Test(ctx); err != nil {
		return contracts.Message{}, err
	}
	return contracts.Message{Key: i18n.FilesTestWebrootOk, Params: map[string]any{"directory": w.path}}, nil
}

// PutFile writes f where the web server finds it: its URL path under the
// web root, or under the domain's subdirectory of it. The file is readable
// by all, as the web server needs.
func (w *webroot) PutFile(_ context.Context, f contracts.File) error {
	file, err := w.file(f)
	if err != nil {
		return err
	}
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: a web server has to reach the files
		return fail(dir, err)
	}
	if err := writeFile(file, f.Content, 0o644); err != nil {
		return fail(dir, err)
	}
	return nil
}

// RemoveFile removes f, which may be gone already. The directories stay.
func (w *webroot) RemoveFile(_ context.Context, f contracts.File) error {
	file, err := w.file(f)
	if err != nil {
		return err
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fail(filepath.Dir(file), err)
	}
	return nil
}

// file returns where f goes.
func (w *webroot) file(f contracts.File) (string, error) {
	if !filepath.IsAbs(w.path) {
		return "", retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorRelative, Params: map[string]any{"directory": w.path}})
	}
	if !f.Valid() {
		return "", retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorFilePath, Params: map[string]any{"path": f.Path}})
	}
	root := w.path
	if w.layout == perDomain {
		name, err := dirName([]string{f.Domain})
		if err != nil {
			return "", retry.Permanent(err)
		}
		root = filepath.Join(root, name)
	}
	return filepath.Join(root, filepath.FromSlash(f.Path)), nil
}
