package files

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

func TestCommand(t *testing.T) {
	t.Setenv("JEC_SECRET_KEY", "s3cret")
	root := t.TempDir()
	req := request(t, "example.com", "www.example.com")
	var lines []contracts.LogLine
	req.Log = func(l contracts.LogLine) { lines = append(lines, l) }
	script := `echo "$JEC_DOMAIN|$JEC_DOMAINS|$RENEWED_DOMAINS|$JEC_CERTIFICATE_ID" > done.txt
if [ -f "$JEC_KEY_FILE" ] && [ -f "$JEC_FULLCHAIN_FILE" ] && [ "$RENEWED_LINEAGE" = "$JEC_CERT_DIR" ]; then echo files; fi
[ -z "$JEC_SECRET_KEY" ] && echo no-secret`
	if _, err := open(t, map[string]string{"directory": root, "command": script}).Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "example.com")
	if got := string(read(t, dir, "done.txt")); got != "example.com|example.com www.example.com|example.com www.example.com|1\n" {
		t.Errorf("the command found %q", got)
	}
	if len(lines) != 3 || lines[1].Key != i18n.FilesLogCommand || lines[1].Detail != script ||
		lines[2].Key != i18n.FilesLogOutput || lines[2].Detail != "files\nno-secret\n" {
		t.Errorf("log %+v", lines)
	}
}

func TestCommandFails(t *testing.T) {
	for _, c := range []struct {
		script string
		key    i18n.Key
		status int
		output string
	}{
		{"echo oops >&2\nexit 3", i18n.FilesErrorCommand, 3, "oops\n"},
		{"no-such-program-of-jec --reload", i18n.FilesErrorCommand, 127, "no-such-program-of-jec"},
		{"if then", i18n.FilesErrorCommandSyntax, 0, ""},
	} {
		req := request(t, "example.com")
		var output string
		req.Log = func(l contracts.LogLine) {
			if l.Key == i18n.FilesLogOutput {
				output = l.Detail
			}
		}
		_, err := open(t, map[string]string{"directory": t.TempDir(), "command": c.script}).Deploy(t.Context(), req)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || !retry.IsPermanent(err) || (c.status != 0 && p.Params["status"] != c.status) {
			t.Errorf("%q: %v", c.script, err)
		}
		if !strings.Contains(output, c.output) {
			t.Errorf("%q wrote %q", c.script, output)
		}
	}
}

func TestCommandTimeout(t *testing.T) {
	old := commandTimeout
	commandTimeout = 100 * time.Millisecond
	t.Cleanup(func() { commandTimeout = old })
	_, err := open(t, map[string]string{"directory": t.TempDir(), "command": "while :; do :; done"}).Deploy(t.Context(), request(t, "example.com"))
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.FilesErrorCommandTimeout || !retry.IsPermanent(err) {
		t.Errorf("a command that does not end: %v", err)
	}
}

func TestTestReadsCommand(t *testing.T) {
	_, err := open(t, map[string]string{"directory": t.TempDir(), "command": "echo 'unterminated"}).Test(t.Context())
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.FilesErrorCommandSyntax {
		t.Errorf("a command that is not shell: %v", err)
	}
	if _, err := open(t, map[string]string{"directory": t.TempDir(), "command": "exit 1"}).Test(t.Context()); err != nil {
		t.Errorf("the test ran the command: %v", err)
	}
}
