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
	"github.com/XMZO/JustEnoughCerts/internal/lib/certfiles"
)

// Layouts of a directory.
const (
	byDomain = "by_domain" // a subdirectory for each certificate, named after its primary domain
	flat     = "flat"      // the files right in the directory
)

func directoryType() providers.Type {
	fields := []providers.Field{
		{Key: "directory", Label: i18n.FilesFieldDirectory, Help: i18n.FilesFieldDirectoryHelp, Kind: providers.Text, Required: true},
		{Key: "layout", Label: i18n.FilesFieldLayout, Kind: providers.Select, Required: true, Default: byDomain, Options: []providers.Option{
			{Value: byDomain, Label: i18n.FilesLayoutByDomain},
			{Value: flat, Label: i18n.FilesLayoutFlat},
		}},
		{Key: "command", Label: i18n.FilesFieldCommand, Help: i18n.FilesFieldCommandHelp, Kind: providers.Multiline},
	}
	fields = append(fields, certfiles.Fields()...)
	fields = append(fields, providers.Field{Key: "owner", Label: i18n.FilesFieldOwner, Help: i18n.FilesFieldOwnerHelp, Kind: providers.Text, Advanced: true})
	return providers.Type{
		ID: "files.directory", Kind: providers.Target, Name: i18n.FilesDirectoryName, Description: i18n.FilesDirectoryDescription,
		Tags: []string{"self_hosted"}, Testable: true, Fields: fields,
	}
}

// directory is a directory at work.
type directory struct {
	path    string
	layout  string
	command string
	files   certfiles.Files
	owner   *owner // nil leaves the files to whoever runs JEC
}

var (
	_ contracts.Target = (*directory)(nil)
	_ contracts.Tester = (*directory)(nil)
)

func factory(_ context.Context, in providers.Instance) (any, error) {
	files, err := certfiles.Parse(in.Config, in.Secrets)
	if err != nil {
		return nil, err
	}
	o, err := parseOwner(strings.TrimSpace(in.Config["owner"]))
	if err != nil {
		return nil, err
	}
	return &directory{
		path: strings.TrimSpace(in.Config["directory"]), layout: in.Config["layout"], command: strings.TrimSpace(in.Config["command"]),
		files: files, owner: o,
	}, nil
}

// Close has nothing to release.
func (*directory) Close() error { return nil }

// Test makes the directory, as the first deployment would, and writes and
// removes a file there. It reads the command without running it.
func (d *directory) Test(context.Context) (contracts.Message, error) {
	if !filepath.IsAbs(d.path) {
		return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorRelative, Params: map[string]any{"directory": d.path}})
	}
	if d.command != "" {
		if _, err := parseCommand(d.command); err != nil {
			return contracts.Message{}, err
		}
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
// old one, so that a reader never sees half a file, and then runs the
// command. The files take the modes and the owner set; those with the
// private key are readable by their owner only, unless set otherwise.
// Files are overwritten, so there is nothing to remove afterwards.
func (d *directory) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
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
	files, err := d.files.Make(req.Domains, req.Chain, req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.FilesLogWrite, Params: map[string]any{"directory": dir}})
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: a web server has to reach the files
		return none, fail(dir, err)
	}
	names := map[string]string{}
	for _, f := range files {
		if err := writeFile(filepath.Join(dir, f.Name), f.Data, f.Mode, d.owner); err != nil {
			if errors.Is(err, errChown) {
				return none, retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorChown, Params: map[string]any{"owner": d.owner.name, "directory": dir}, Err: err})
			}
			return none, fail(dir, err)
		}
		names[f.Kind] = f.Name
	}
	if d.command != "" {
		if err := runCommand(ctx, d.command, dir, names, req, log); err != nil {
			return none, err
		}
	}
	return contracts.DeployResult{}, nil
}

// dirName names the subdirectory of a certificate after its primary
// domain, as a file name: *.example.com becomes star.example.com.
var dirName = certfiles.DomainName

// errChown is the error of a file whose owner could not be changed.
var errChown = errors.New("files: the owner could not be set")

// writeFile replaces path with data, through a temporary file in the same
// directory, of mode and owner o, unless nil.
func writeFile(path string, data []byte, mode fs.FileMode, o *owner) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // fails once renamed
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if o != nil {
		if err := tmp.Chown(o.uid, o.gid); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("%w: %w", errChown, err)
		}
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
