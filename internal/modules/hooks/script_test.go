package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// open makes a script that runs in a directory of its own, not there yet.
func open(t *testing.T, text string) *script {
	t.Helper()
	s, err := newScript(providers.Instance{Config: map[string]string{"script": text}}, filepath.Join(t.TempDir(), "hooks"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// note is a note about a failed deployment of certificate 3.
func note(t *testing.T) contracts.Note {
	t.Helper()
	data, err := json.Marshal(contracts.CertificateEvent{
		CertificateID: 3, Domains: []string{"example.com", "www.example.com"}, VersionID: 9,
		Fingerprint: "ab12", NotAfter: time.Date(2027, 5, 1, 8, 0, 0, 0, time.UTC),
		Deployment: &contracts.Deployment{ID: 4, Target: 5, Attempt: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	return contracts.Note{
		Event: contracts.CertificateDeployFailed, Time: time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC),
		Title: "example.com failed", Subject: "[JEC] example.com failed", Text: "line 1\nline 2",
		Link: "https://jec.example.com/certificates/3/deployments/4", Data: data,
	}
}

func TestNotify(t *testing.T) {
	t.Setenv("JEC_SECRET_KEY", "s3cret")
	s := open(t, `cat > note.json
{
  echo "$JEC_EVENT|$JEC_TIME|$JEC_SUBJECT|$JEC_LINK"
  echo "$JEC_CERTIFICATE_ID|$JEC_DOMAIN|$JEC_DOMAINS|$JEC_NOT_AFTER|$JEC_FINGERPRINT"
  echo "$JEC_TEXT"
  echo "secret:$JEC_SECRET_KEY"
} > env.txt`)
	n := note(t)
	if err := s.Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(s.dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the directory: %v, %v", info, err)
	}
	env, err := os.ReadFile(filepath.Join(s.dir, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := "certificate.deploy_failed|2026-10-19T02:00:00Z|[JEC] example.com failed|https://jec.example.com/certificates/3/deployments/4\n" +
		"3|example.com|example.com www.example.com|2027-05-01T08:00:00Z|ab12\n" +
		"line 1\nline 2\nsecret:\n"
	if string(env) != want {
		t.Errorf("the script found\n%s\nwant\n%s", env, want)
	}
	var got contracts.Note
	b, err := os.ReadFile(filepath.Join(s.dir, "note.json"))
	if err != nil || json.Unmarshal(b, &got) != nil || got.Subject != n.Subject || string(got.Data) != string(n.Data) {
		t.Errorf("the script read %s, %v", b, err)
	}
}

func TestNotifyTest(t *testing.T) {
	s := open(t, `echo "$JEC_EVENT|$JEC_TITLE|${JEC_CERTIFICATE_ID-none}" > env.txt`)
	n := contracts.Note{Event: contracts.NoteTest, Title: "A test", Time: time.Now()}
	if err := s.Notify(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.dir, "env.txt")); string(b) != "notify.test|A test|none\n" {
		t.Errorf("a test gave the script %q", b)
	}
}

func TestNotifyFails(t *testing.T) {
	for _, c := range []struct {
		script    string
		key       i18n.Key
		status    int
		output    string
		permanent bool
	}{
		{"echo working\necho 'no route to the API' >&2\nexit 1", i18n.HooksErrorStatusOutput, 1, "no route to the API", true},
		{"exit 2", i18n.HooksErrorStatus, 2, "", true},
		{"echo 'try later' >&2; exit 75", i18n.HooksErrorStatusOutput, 75, "try later", false},
		{"no-such-program-of-jec --notify", i18n.HooksErrorStatusOutput, 127, "no-such-program-of-jec", true},
	} {
		err := open(t, c.script).Notify(t.Context(), note(t))
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || p.Params["status"] != c.status || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%q: %v", c.script, err)
			continue
		}
		if out, _ := p.Params["output"].(string); !strings.Contains(out, c.output) || (c.output == "" && out != "") {
			t.Errorf("%q: output %q", c.script, out)
		}
	}
}

func TestSyntax(t *testing.T) {
	_, err := newScript(providers.Instance{Config: map[string]string{"script": "if then"}}, t.TempDir())
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.HooksErrorSyntax || !retry.IsPermanent(err) {
		t.Errorf("a script that is not shell: %v", err)
	}
}

func TestTimeout(t *testing.T) {
	old := scriptTimeout
	scriptTimeout = 100 * time.Millisecond
	t.Cleanup(func() { scriptTimeout = old })
	err := open(t, "while :; do :; done").Notify(t.Context(), note(t))
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.HooksErrorTimeout || !retry.IsPermanent(err) {
		t.Errorf("a script that does not end: %v", err)
	}
}

func TestStopping(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	err := open(t, "while :; do :; done").Notify(ctx, note(t))
	if !errors.Is(err, context.Canceled) || retry.IsPermanent(err) {
		t.Errorf("JEC stopping: %v", err)
	}
}

func TestDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := newScript(providers.Instance{Config: map[string]string{"script": "true"}}, filepath.Join(file, "hooks"))
	if err != nil {
		t.Fatal(err)
	}
	var p *contracts.Problem
	if err := s.Notify(t.Context(), note(t)); !errors.As(err, &p) || p.Key != i18n.HooksErrorDirectory || !retry.IsPermanent(err) {
		t.Errorf("no directory to run in: %v", err)
	}
}

func TestReason(t *testing.T) {
	long := strings.Repeat("x", 2*maxReason)
	for out, want := range map[string]string{
		"":                         "",
		"  \n\n":                   "",
		"first\nsecond\r\n\n":      "second",
		"one line":                 "one line",
		long:                       long[:maxReason] + "...",
		"bad \xff\xfe bytes\n":     "bad  bytes",
		"before\n" + long + "\n  ": long[:maxReason] + "...",
	} {
		if got := reason([]byte(out)); got != want {
			t.Errorf("reason(%q) = %q, want %q", out, got, want)
		}
	}
}
