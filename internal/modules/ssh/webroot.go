package ssh

import (
	"context"
	"errors"
	"path"
	"strings"
	"sync"

	gossh "golang.org/x/crypto/ssh"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// Layouts of a web root, as the files module has them.
const (
	shared    = "shared"
	perDomain = "per_domain"
)

func webrootType() providers.Type {
	return providers.Type{
		ID: "ssh.webroot", Kind: providers.Solver, Challenge: providers.ChallengeHTTP,
		Name: i18n.SshWebrootName, Description: i18n.SshWebrootDescription,
		Tags: []string{"self_hosted", "http"}, Testable: true,
		Fields: append(connFields(),
			providers.Field{Key: "directory", Label: i18n.SshFieldWebroot, Help: i18n.SshFieldWebrootHelp, Kind: providers.Text, Required: true},
			providers.Field{Key: "layout", Label: i18n.SshFieldWebrootLayout, Kind: providers.Select, Required: true, Default: shared, Options: []providers.Option{
				{Value: shared, Label: i18n.SshWebrootShared},
				{Value: perDomain, Label: i18n.SshWebrootPerDomain},
			}},
		),
	}
}

// webroot is the directory a web server on a machine reached over SSH
// serves the domains from, where the files of validations go. It connects
// on its first use and keeps the connection until closed.
type webroot struct {
	conn
	directory, layout string

	mu     sync.Mutex
	client *gossh.Client
}

var (
	_ contracts.FileSolver = (*webroot)(nil)
	_ contracts.Tester     = (*webroot)(nil)
)

func webrootFactory(_ context.Context, in providers.Instance) (any, error) {
	c, err := newConn(in)
	if err != nil {
		return nil, err
	}
	return &webroot{conn: c, directory: strings.TrimSpace(in.Config["directory"]), layout: in.Config["layout"]}, nil
}

// PutFile writes f where the web server finds it, through a temporary file
// renamed into place, readable by all.
func (w *webroot) PutFile(ctx context.Context, f contracts.File) error {
	file, err := w.file(f)
	if err != nil {
		return err
	}
	dir := path.Dir(file)
	tmp := path.Join(dir, "."+path.Base(file)+".jec")
	return w.run(ctx, dir, "mkdir -p "+quote(dir)+" && umask 022 && cat > "+quote(tmp)+" && mv -f "+quote(tmp)+" "+quote(file), f.Content)
}

// RemoveFile removes f, which may be gone already. The directories stay.
func (w *webroot) RemoveFile(ctx context.Context, f contracts.File) error {
	file, err := w.file(f)
	if err != nil {
		return err
	}
	return w.run(ctx, path.Dir(file), "rm -f "+quote(file), nil)
}

// Close ends the connection, if there is one.
func (w *webroot) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.client == nil {
		return nil
	}
	err := w.client.Close()
	w.client = nil
	return err
}

// run runs command in dir's name, connecting first if need be. The
// connection closes when ctx ends.
func (w *webroot) run(ctx context.Context, dir, command string, stdin []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.client == nil {
		client, err := w.dial(ctx)
		if err != nil {
			return err
		}
		w.client = client
	}
	client := w.client
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	if out, err := run(client, command, stdin); err != nil {
		var exit *gossh.ExitError
		if !errors.As(err, &exit) {
			_ = client.Close() // the connection broke: dial again next time
			w.client = nil
		}
		return w.writeFailed(ctx, dir, out, err)
	}
	return nil
}

// Test connects, signs in and makes sure that the web root can be written.
func (w *webroot) Test(ctx context.Context) (contracts.Message, error) {
	if !path.IsAbs(w.directory) {
		return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorRelative, Params: map[string]any{"directory": w.directory}})
	}
	if err := w.run(ctx, w.directory, "mkdir -p "+quote(w.directory)+" && test -w "+quote(w.directory), nil); err != nil {
		return contracts.Message{}, err
	}
	return contracts.Message{Key: i18n.SshTestWebrootOk, Params: map[string]any{"user": w.user, "address": w.addr, "directory": w.directory}}, nil
}

// file returns where f goes.
func (w *webroot) file(f contracts.File) (string, error) {
	if !path.IsAbs(w.directory) {
		return "", retry.Permanent(&contracts.Problem{Key: i18n.SshErrorRelative, Params: map[string]any{"directory": w.directory}})
	}
	if !f.Valid() {
		return "", retry.Permanent(&contracts.Problem{Key: i18n.SshErrorFilePath, Params: map[string]any{"path": f.Path}})
	}
	root := w.directory
	if w.layout == perDomain {
		name, err := dirName([]string{f.Domain})
		if err != nil {
			return "", retry.Permanent(err)
		}
		root = path.Join(root, name)
	}
	return path.Join(root, f.Path), nil
}
