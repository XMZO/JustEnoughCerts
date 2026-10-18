package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/shellx"
)

// scriptTimeout bounds a script, within the time the notify module gives
// a notifier; tests shorten it.
var scriptTimeout = 20 * time.Second

// tempFail is the exit status of a script that failed in a way that may
// pass, EX_TEMPFAIL of sysexits.h, as mail programs use it: the event is
// tried again later. Other failures are not.
const tempFail = 75

// maxReason bounds the line of output a failure shows.
const maxReason = 300

func scriptType() providers.Type {
	return providers.Type{
		ID: "hooks.script", Kind: providers.Notifier, Name: i18n.HooksScriptName, Description: i18n.HooksScriptDescription,
		Tags: []string{"self_hosted"},
		Fields: append([]providers.Field{
			{Key: "script", Label: i18n.HooksFieldScript, Help: i18n.HooksFieldScriptHelp, Kind: providers.Multiline, Required: true},
		}, contracts.NotifierFields()...),
	}
}

// script runs a shell script for each note.
type script struct {
	file *syntax.File
	// dir is where it runs, made on first use.
	dir string
}

var _ contracts.Notifier = (*script)(nil)

func newScript(in providers.Instance, dir string) (*script, error) {
	file, err := shellx.Parse(in.Config["script"])
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.HooksErrorSyntax, Params: map[string]any{"error": err.Error()}})
	}
	return &script{file: file, dir: dir}, nil
}

// Close has nothing to release.
func (*script) Close() error { return nil }

// Notify runs the script with n in its environment and, as JSON, on its
// standard input.
func (s *script) Notify(ctx context.Context, n contracts.Note) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.HooksErrorDirectory, Params: map[string]any{"directory": s.dir, "error": err.Error()}, Err: err})
	}
	stdin, err := json.Marshal(n)
	if err != nil {
		return retry.Permanent(err)
	}
	run, cancel := context.WithTimeout(ctx, scriptTimeout)
	defer cancel()
	out, err := shellx.Run(run, shellx.Script{File: s.file, Dir: s.dir, Env: env(n), Stdin: stdin})
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		// JEC is stopping, or the notify module ran out of time: the
		// event comes again.
		return ctx.Err()
	case errors.Is(err, context.DeadlineExceeded):
		return retry.Permanent(&contracts.Problem{Key: i18n.HooksErrorTimeout, Params: map[string]any{"seconds": int(scriptTimeout / time.Second)}})
	}
	status, ok := shellx.Status(err)
	if !ok {
		return retry.Permanent(&contracts.Problem{Key: i18n.HooksErrorRun, Params: map[string]any{"error": err.Error()}, Err: err})
	}
	p := &contracts.Problem{Key: i18n.HooksErrorStatus, Params: map[string]any{"status": status}}
	if r := reason(out); r != "" {
		p = &contracts.Problem{Key: i18n.HooksErrorStatusOutput, Params: map[string]any{"status": status, "output": r}}
	}
	if status == tempFail {
		return p
	}
	return retry.Permanent(p)
}

// env is what a script learns of a note in its environment: the note in
// its language, and what the event was about, when it was about a
// certificate. A test has no certificate.
func env(n contracts.Note) []string {
	env := []string{
		"JEC_EVENT=" + n.Event,
		"JEC_TIME=" + n.Time.UTC().Format(time.RFC3339),
		"JEC_TITLE=" + n.Title,
		"JEC_SUBJECT=" + n.Subject,
		"JEC_TEXT=" + n.Text,
		"JEC_LINK=" + n.Link,
	}
	var d contracts.CertificateEvent
	if len(n.Data) == 0 || json.Unmarshal(n.Data, &d) != nil || d.CertificateID == 0 {
		return env
	}
	env = append(env,
		"JEC_CERTIFICATE_ID="+strconv.FormatInt(d.CertificateID, 10),
		"JEC_DOMAINS="+strings.Join(d.Domains, " "),
	)
	if len(d.Domains) > 0 {
		env = append(env, "JEC_DOMAIN="+d.Domains[0])
	}
	if !d.NotAfter.IsZero() {
		env = append(env, "JEC_NOT_AFTER="+d.NotAfter.UTC().Format(time.RFC3339))
	}
	if d.Fingerprint != "" {
		env = append(env, "JEC_FINGERPRINT="+d.Fingerprint)
	}
	return env
}

// reason is the last line a failed script wrote, which says why as a
// rule, cut to maxReason bytes.
func reason(out []byte) string {
	text := strings.TrimSpace(strings.ToValidUTF8(string(out), ""))
	if i := strings.LastIndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[i+1:])
	}
	if len(text) > maxReason {
		text = strings.ToValidUTF8(text[:maxReason], "") + "..."
	}
	return text
}
