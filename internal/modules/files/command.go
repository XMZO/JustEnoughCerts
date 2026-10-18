package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// maxOutput bounds the output of a command kept in the log.
const maxOutput = 4000

// commandTimeout bounds a command, which may wait for a service to
// reload; tests shorten it.
var commandTimeout = 5 * time.Minute

// parseCommand reads script as a shell program: POSIX sh, or Bash.
func parseCommand(script string) (*syntax.File, error) {
	file, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorCommandSyntax, Params: map[string]any{"error": err.Error()}})
	}
	return file, nil
}

// runCommand runs script with the shell built into JEC, in dir, where the
// files of req are: what a command finds there and in its environment is
// what certbot gives its deploy hooks, and more. What it writes goes to
// the log.
func runCommand(ctx context.Context, script, dir string, req contracts.DeployRequest, log func(contracts.LogLine)) error {
	file, err := parseCommand(script)
	if err != nil {
		return err
	}
	var out output
	runner, err := interp.New(
		interp.Dir(dir),
		interp.Env(expand.ListEnviron(append(environ(), commandEnv(dir, req)...)...)),
		interp.StdIO(nil, &out, &out),
	)
	if err != nil {
		return retry.Permanent(err)
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.FilesLogCommand, Detail: script})
	run, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	err = runner.Run(run, file)
	if b := out.bytes(); len(b) > 0 {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.FilesLogOutput, Detail: string(b)})
	}
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		// JEC is stopping: the deployment is done again later.
		return ctx.Err()
	case errors.Is(run.Err(), context.DeadlineExceeded):
		return retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorCommandTimeout, Params: map[string]any{"minutes": int(commandTimeout.Minutes())}})
	}
	if status, ok := errors.AsType[interp.ExitStatus](err); ok {
		return retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorCommand, Params: map[string]any{"status": int(status)}})
	}
	return retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorCommand, Params: map[string]any{"status": -1}, Err: err})
}

// commandEnv is what a command learns of the deployment: the paths of the
// files and the domains, under JEC's names and those of certbot's deploy
// hooks, so that those work as they are.
func commandEnv(dir string, req contracts.DeployRequest) []string {
	env := []string{
		"JEC_CERT_DIR=" + dir,
		"JEC_CERT_FILE=" + filepath.Join(dir, "cert.pem"),
		"JEC_CHAIN_FILE=" + filepath.Join(dir, "chain.pem"),
		"JEC_FULLCHAIN_FILE=" + filepath.Join(dir, "fullchain.pem"),
		"JEC_KEY_FILE=" + filepath.Join(dir, "privkey.pem"),
		"JEC_DOMAINS=" + strings.Join(req.Domains, " "),
		"JEC_CERTIFICATE_ID=" + strconv.FormatInt(req.CertificateID, 10),
		"RENEWED_LINEAGE=" + dir,
		"RENEWED_DOMAINS=" + strings.Join(req.Domains, " "),
	}
	if len(req.Domains) > 0 {
		env = append(env, "JEC_DOMAIN="+req.Domains[0])
	}
	if len(req.Chain) > 0 {
		env = append(env, "JEC_NOT_AFTER="+req.Chain[0].NotAfter.UTC().Format(time.RFC3339))
	}
	return env
}

// environ is JEC's environment but for its own settings, such as the
// master key, which a command has no use for.
func environ() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "JEC_") {
			env = append(env, kv)
		}
	}
	return env
}

// output keeps the end of what a command writes, from its standard output
// and error at once.
type output struct {
	mu  sync.Mutex
	buf []byte
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.buf = append(o.buf, p...)
	if len(o.buf) > 2*maxOutput {
		o.buf = append(o.buf[:0], o.buf[len(o.buf)-maxOutput:]...)
	}
	return len(p), nil
}

func (o *output) bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.buf) > maxOutput {
		return o.buf[len(o.buf)-maxOutput:]
	}
	return o.buf
}
