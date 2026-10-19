package files

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/certfiles"
	"github.com/XMZO/JustEnoughCerts/internal/lib/shellx"
)

// commandTimeout bounds a command, which may wait for a service to
// reload; tests shorten it.
var commandTimeout = 5 * time.Minute

// parseCommand reads script as a shell program: POSIX sh, or Bash.
func parseCommand(script string) (*syntax.File, error) {
	file, err := shellx.Parse(script)
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorCommandSyntax, Params: map[string]any{"error": err.Error()}})
	}
	return file, nil
}

// runCommand runs script with the shell built into JEC, in dir, where the
// files of req are, under names by kind: what a command finds there and in
// its environment is what certbot gives its deploy hooks, and more. What
// it writes goes to the log.
func runCommand(ctx context.Context, script, dir string, names map[string]string, req contracts.DeployRequest, log func(contracts.LogLine)) error {
	file, err := parseCommand(script)
	if err != nil {
		return err
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.FilesLogCommand, Detail: script})
	run, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, err := shellx.Run(run, shellx.Script{File: file, Dir: dir, Env: commandEnv(dir, names, req)})
	if len(out) > 0 {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.FilesLogOutput, Detail: string(out)})
	}
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		// JEC is stopping: the deployment is done again later.
		return ctx.Err()
	case errors.Is(err, context.DeadlineExceeded):
		return retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorCommandTimeout, Params: map[string]any{"minutes": int(commandTimeout.Minutes())}})
	}
	if status, ok := shellx.Status(err); ok {
		return retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorCommand, Params: map[string]any{"status": status}})
	}
	return retry.Permanent(&contracts.Problem{Key: i18n.FilesErrorCommand, Params: map[string]any{"status": -1}, Err: err})
}

// commandEnv is what a command learns of the deployment: the paths of the
// files, by kind as names has them, and the domains, under JEC's names
// and those of certbot's deploy hooks, so that those work as they are
// while the files keep certbot's names.
func commandEnv(dir string, names map[string]string, req contracts.DeployRequest) []string {
	env := []string{"JEC_CERT_DIR=" + dir}
	for _, v := range []struct{ name, kind string }{
		{"JEC_CERT_FILE", certfiles.KindCert}, {"JEC_CHAIN_FILE", certfiles.KindChain}, {"JEC_FULLCHAIN_FILE", certfiles.KindFullChain},
		{"JEC_KEY_FILE", certfiles.KindKey}, {"JEC_COMBINED_FILE", certfiles.KindCombined}, {"JEC_PFX_FILE", certfiles.KindPFX},
	} {
		if n, ok := names[v.kind]; ok {
			env = append(env, v.name+"="+filepath.Join(dir, n))
		}
	}
	env = append(env,
		"JEC_DOMAINS="+strings.Join(req.Domains, " "),
		"JEC_CERTIFICATE_ID="+strconv.FormatInt(req.CertificateID, 10),
		"RENEWED_LINEAGE="+dir,
		"RENEWED_DOMAINS="+strings.Join(req.Domains, " "),
	)
	if len(req.Domains) > 0 {
		env = append(env, "JEC_DOMAIN="+req.Domains[0])
	}
	if len(req.Chain) > 0 {
		env = append(env, "JEC_NOT_AFTER="+req.Chain[0].NotAfter.UTC().Format(time.RFC3339))
	}
	return env
}
