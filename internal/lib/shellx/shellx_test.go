package shellx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, s Script) ([]byte, error) {
	t.Helper()
	if s.Dir == "" {
		s.Dir = t.TempDir()
	}
	return Run(t.Context(), s)
}

func TestRun(t *testing.T) {
	t.Setenv("JEC_SECRET_KEY", "s3cret")
	t.Setenv("HOOK_TOKEN", "t0ken")
	file, err := Parse(`read -r line; echo "got $line in $(pwd)"
[ -z "$JEC_SECRET_KEY" ] && echo no-secret
echo "$HOOK_TOKEN $GREETING" >&2
echo written > out.txt`)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	out, err := run(t, Script{File: file, Dir: dir, Env: []string{"GREETING=hello"}, Stdin: []byte("a note\n")})
	if err != nil {
		t.Fatal(err)
	}
	if want := "got a note in " + dir + "\nno-secret\nt0ken hello\n"; string(out) != want {
		t.Errorf("output %q, want %q", out, want)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "out.txt")); err != nil || string(b) != "written\n" {
		t.Errorf("redirection wrote %q, %v", b, err)
	}
}

func TestRunStatus(t *testing.T) {
	for script, want := range map[string]int{
		"exit 3":                    3,
		"false":                     1,
		"no-such-program-of-jec -x": 127,
	} {
		file, err := Parse(script)
		if err != nil {
			t.Fatal(err)
		}
		_, err = run(t, Script{File: file})
		if got, ok := Status(err); !ok || got != want {
			t.Errorf("%q: %v", script, err)
		}
	}
	if _, ok := Status(errors.New("other")); ok {
		t.Error("a status from an error without one")
	}
}

func TestRunUnreadInput(t *testing.T) {
	// More than a pipe holds, and the script does not read it.
	file, _ := Parse("exit 0")
	if _, err := run(t, Script{File: file, Stdin: make([]byte, 1<<20)}); err != nil {
		t.Fatal(err)
	}
}

func TestRunEnds(t *testing.T) {
	file, _ := Parse("while :; do :; done")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := Run(ctx, Script{File: file, Dir: t.TempDir()}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a script that does not end: %v", err)
	}
}

func TestRunOutputEnd(t *testing.T) {
	file, _ := Parse(`i=0; while [ $i -lt 1000 ]; do echo "line $i"; i=$((i+1)); done`)
	out, err := run(t, Script{File: file})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != MaxOutput || !strings.HasSuffix(string(out), "line 999\n") {
		t.Errorf("kept %d bytes ending %q", len(out), out[max(len(out)-20, 0):])
	}
}

func TestRunNoDirectory(t *testing.T) {
	file, _ := Parse("true")
	if _, err := Run(t.Context(), Script{File: file, Dir: filepath.Join(t.TempDir(), "gone")}); err == nil {
		t.Error("ran in a directory that is not there")
	}
}

func TestParse(t *testing.T) {
	if _, err := Parse("if then"); err == nil {
		t.Error("parsed a script that is not shell")
	}
	if _, err := Parse("[[ -n $x ]] && echo ${x//a/b}"); err != nil {
		t.Errorf("Bash: %v", err)
	}
}
