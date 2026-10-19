package main

import (
	"bytes"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeHost is a machine with systemd, on which useradd adds the user and
// every other command only counts.
type fakeHost struct {
	notRoot, noSystemd bool
	exe                string
	users              map[string]bool
	ran                [][]string
}

func (h *fakeHost) root() bool                  { return !h.notRoot }
func (h *fakeHost) systemd() bool               { return !h.noSystemd }
func (h *fakeHost) executable() (string, error) { return h.exe, nil }

func (h *fakeHost) lookupUser(name string) (int, int, error) {
	if !h.users[name] {
		return 0, 0, user.UnknownUserError(name)
	}
	return os.Getuid(), os.Getgid(), nil
}

func (h *fakeHost) run(_ io.Writer, name string, args ...string) error {
	h.ran = append(h.ran, append([]string{name}, args...))
	if name == "useradd" {
		h.users[args[len(args)-1]] = true
	}
	return nil
}

// newFake returns a fake host whose jec is a small file, and a service of
// it that installs into a temporary directory.
func newFake(t *testing.T) (*fakeHost, service) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "jec-download")
	if err := os.WriteFile(exe, []byte("#!jec"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := defaultService()
	s.Bin = filepath.Join(dir, "bin", "jec")
	s.Unit = filepath.Join(dir, "jec.service")
	return &fakeHost{exe: exe, users: map[string]bool{"root": true}}, s
}

func TestServiceInstall(t *testing.T) {
	h, s := newFake(t)
	var out bytes.Buffer
	if err := installService(h, s, true, false, &out); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(s.Bin); err != nil || string(b) != "#!jec" {
		t.Errorf("binary: %q, %v", b, err)
	} else if fi, _ := os.Stat(s.Bin); fi.Mode().Perm() != 0o755 {
		t.Errorf("binary mode %v", fi.Mode())
	}
	unit, err := os.ReadFile(s.Unit)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"ExecStart=" + s.Bin + " serve -data-dir /var/lib/jec", "User=jec", "StateDirectory=jec", "ProtectSystem=full"} {
		if !strings.Contains(string(unit), "\n"+line+"\n") {
			t.Errorf("unit lacks %q:\n%s", line, unit)
		}
	}
	want := [][]string{
		{"useradd", "--system", "--user-group", "--home-dir", "/var/lib/jec", "--no-create-home", "--shell", h.ran[0][7], "jec"},
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "jec.service"},
		{"systemctl", "restart", "jec.service"},
	}
	if !slices.EqualFunc(h.ran, want, slices.Equal) {
		t.Errorf("ran %q, want %q", h.ran, want)
	}
	if !strings.Contains(out.String(), "journalctl -u jec.service") {
		t.Errorf("output: %s", out.String())
	}

	// Again, as to upgrade: the user exists, the binary is replaced, the
	// unit is the same, and the service starts again.
	if err := os.WriteFile(h.exe, []byte("#!jec 2"), 0o700); err != nil {
		t.Fatal(err)
	}
	h.ran = nil
	if err := installService(h, s, true, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(s.Bin); string(b) != "#!jec 2" {
		t.Errorf("binary after the upgrade: %q", b)
	}
	if again, _ := os.ReadFile(s.Unit); !bytes.Equal(again, unit) {
		t.Errorf("unit changed:\n%s", again)
	}
	if len(h.ran) != 3 || h.ran[0][0] != "systemctl" {
		t.Errorf("ran %q", h.ran)
	}

	// From the installed binary itself, nothing is copied.
	h.exe = s.Bin
	if err := installService(h, s, false, false, &out); err != nil || strings.Count(out.String(), "Copied") != 1 {
		t.Errorf("from the installed binary: %v\n%s", err, out.String())
	}
}

func TestServiceInstallElsewhere(t *testing.T) {
	h, s := newFake(t)
	s.DataDir = filepath.Join(t.TempDir(), "jec-data")
	s.User = "root"
	if err := installService(h, s, false, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(s.DataDir); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Errorf("data directory: %v, %v", fi, err)
	}
	unit, _ := os.ReadFile(s.Unit)
	if !strings.Contains(string(unit), "\nReadWritePaths="+s.DataDir+"\n") || strings.Contains(string(unit), "StateDirectory") || !strings.Contains(string(unit), "\nUser=root\n") {
		t.Errorf("unit:\n%s", unit)
	}
	for _, cmd := range h.ran {
		if cmd[0] == "useradd" || slices.Contains(cmd, "restart") {
			t.Errorf("ran %q", cmd)
		}
	}
}

func TestServiceRefusals(t *testing.T) {
	h, s := newFake(t)
	for _, tt := range []struct {
		name   string
		change func(*fakeHost, *service)
	}{
		{"not root", func(h *fakeHost, _ *service) { h.notRoot = true }},
		{"no systemd", func(h *fakeHost, _ *service) { h.noSystemd = true }},
		{"relative binary", func(_ *fakeHost, s *service) { s.Bin = "bin/jec" }},
		{"space in the data directory", func(_ *fakeHost, s *service) { s.DataDir = "/var/lib/my jec" }},
		{"specifier", func(_ *fakeHost, s *service) { s.DataDir = "/var/lib/%i" }},
		{"unclean path", func(_ *fakeHost, s *service) { s.DataDir = "/var/lib/../jec" }},
		{"user", func(_ *fakeHost, s *service) { s.User = "Jec Service" }},
		{"unit name", func(_ *fakeHost, s *service) { s.Unit = "/etc/systemd/system/jec" }},
	} {
		h, s := *h, s
		h.users = map[string]bool{}
		tt.change(&h, &s)
		if err := installService(&h, s, true, false, io.Discard); err == nil {
			t.Errorf("%s: installed", tt.name)
		}
		if len(h.ran) > 0 {
			t.Errorf("%s: ran %q", tt.name, h.ran)
		}
	}

	// A unit install did not write stays, unless forced.
	if err := os.WriteFile(s.Unit, []byte("[Unit]\nDescription=mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installService(h, s, true, false, io.Discard); err == nil || !strings.Contains(err.Error(), "-force") {
		t.Errorf("replaced a foreign unit: %v", err)
	}
	if err := uninstallService(h, s, false, io.Discard); err == nil {
		t.Error("removed a foreign unit")
	}
	if len(h.ran) > 0 {
		t.Errorf("ran %q", h.ran)
	}
	if err := installService(h, s, true, true, io.Discard); err != nil {
		t.Errorf("forced: %v", err)
	}
}

func TestServiceUninstall(t *testing.T) {
	h, s := newFake(t)
	if err := uninstallService(h, s, false, io.Discard); err == nil {
		t.Error("uninstalled what is not installed")
	}
	if err := installService(h, s, true, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	h.ran = nil
	var out bytes.Buffer
	if err := uninstallService(h, s, false, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Unit); !os.IsNotExist(err) {
		t.Errorf("unit still there: %v", err)
	}
	if _, err := os.Stat(s.Bin); err != nil {
		t.Errorf("binary gone: %v", err)
	}
	want := [][]string{{"systemctl", "disable", "--now", "jec.service"}, {"systemctl", "daemon-reload"}}
	if !slices.EqualFunc(h.ran, want, slices.Equal) {
		t.Errorf("ran %q, want %q", h.ran, want)
	}
	if !strings.Contains(out.String(), "/var/lib/jec") || !strings.Contains(out.String(), "the user jec") {
		t.Errorf("output: %s", out.String())
	}
}

// TestServiceFile keeps deploy/systemd/jec.service the unit install writes
// with the default paths.
func TestServiceFile(t *testing.T) {
	want, err := defaultService().unit()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "..", "deploy", "systemd", "jec.service"))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("deploy/systemd/jec.service is not the unit of jec service install (%v); run:\n  go run ./cmd/jec service unit > deploy/systemd/jec.service", err)
	}
}

func TestServiceCommand(t *testing.T) {
	code, stdout, _ := runCLI(t.Context(), "service", "unit", "-data-dir", "/srv/jec", "-user", "root")
	if code != exitOK || !strings.Contains(stdout, "\nReadWritePaths=/srv/jec\n") || !strings.Contains(stdout, "\nUser=root\n") {
		t.Errorf("service unit: exit %d\n%s", code, stdout)
	}
	for _, tt := range []struct {
		args []string
		code int
	}{
		{[]string{"service"}, exitUsage},
		{[]string{"service", "help"}, exitOK},
		{[]string{"service", "start"}, exitUsage},
		{[]string{"service", "unit", "-bin", "jec"}, exitError},
		{[]string{"service", "unit", "-start=false"}, exitUsage},
	} {
		if code, _, _ := runCLI(t.Context(), tt.args...); code != tt.code {
			t.Errorf("jec %q: exit %d, want %d", tt.args, code, tt.code)
		}
	}
}
