package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
)

// jec service runs JEC as a systemd service (PLAN.md 8.4). install copies
// the binary in place, adds a system user, writes the unit and starts it;
// installing again, as after downloading a new version, upgrades it.
// uninstall stops the service and removes the unit, keeping the data.
// deploy/systemd/jec.service is the unit with the default paths.

//go:embed jec.service.tmpl
var unitText string

var unitTemplate = template.Must(template.New("unit").Parse(unitText))

// unitMarker is in the first line of the units install writes: it only
// replaces or removes a unit with it.
const unitMarker = `written by "jec service install"`

// service is where install puts JEC.
type service struct {
	Bin     string // where the binary goes, which the unit runs
	DataDir string
	User    string
	Unit    string // the unit file
}

func defaultService() service {
	return service{Bin: "/usr/local/bin/jec", DataDir: "/var/lib/jec", User: "jec", Unit: "/etc/systemd/system/jec.service"}
}

var (
	// servicePath is a path systemd takes as it is in ExecStart= and
	// ReadWritePaths=: no spaces, quotes, specifiers or variables.
	servicePath = regexp.MustCompile(`^/[A-Za-z0-9._@+,:/-]+$`)
	serviceUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// check tells what is wrong with s, if anything.
func (s service) check() error {
	for _, p := range []struct{ flag, path string }{{"-bin", s.Bin}, {"-data-dir", s.DataDir}, {"-unit", s.Unit}} {
		if !servicePath.MatchString(p.path) || filepath.Clean(p.path) != p.path {
			return fmt.Errorf("%s: %q is not a plain absolute path", p.flag, p.path)
		}
	}
	if !serviceUser.MatchString(s.User) {
		return fmt.Errorf("-user: %q is not a user name", s.User)
	}
	if !strings.HasSuffix(s.Unit, ".service") {
		return fmt.Errorf("-unit: %q does not end in .service", s.Unit)
	}
	return nil
}

// name is the name of the unit, as systemctl takes it.
func (s service) name() string {
	return filepath.Base(s.Unit)
}

// stateDirectory is DataDir below /var/lib, which systemd then creates and
// hands to the user, or "" for a data directory elsewhere.
func (s service) stateDirectory() string {
	if rel, ok := strings.CutPrefix(s.DataDir, "/var/lib/"); ok {
		return rel
	}
	return ""
}

// unit returns the unit file for s.
func (s service) unit() ([]byte, error) {
	var b bytes.Buffer
	err := unitTemplate.Execute(&b, struct {
		service
		StateDirectory string
	}{s, s.stateDirectory()})
	return b.Bytes(), err
}

// host is the machine jec service changes; tests fake it.
type host interface {
	// root tells whether the process may change the system.
	root() bool
	// systemd tells whether systemd runs the system.
	systemd() bool
	// executable returns the path of the running binary.
	executable() (string, error)
	// lookupUser returns the ids of a user, or a user.UnknownUserError.
	lookupUser(name string) (uid, gid int, err error)
	// run runs a command, with its output on out.
	run(out io.Writer, name string, args ...string) error
}

// serviceHost is the machine; tests replace it.
var serviceHost host = system{}

type system struct{}

func (system) root() bool { return os.Geteuid() == 0 }

// systemd tells as sd_booted(3) does.
func (system) systemd() bool {
	fi, err := os.Stat("/run/systemd/system")
	return err == nil && fi.IsDir()
}

func (system) executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

func (system) lookupUser(name string) (int, int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, err
	}
	gid, err := strconv.Atoi(u.Gid)
	return uid, gid, err
}

func (system) run(out io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...) //nolint:gosec // G204: systemctl and useradd, with checked arguments
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// serviceCommand is a command of jec service.
type serviceCommand struct{ name, summary string }

var serviceCommands = []serviceCommand{
	{"install", "copy jec to -bin, add the user, and run JEC as a systemd service; again, to upgrade"},
	{"uninstall", "stop the service and remove its unit, keeping the data, the user and the binary"},
	{"unit", "print the unit install writes"},
}

func serviceUsage(w io.Writer) {
	fmt.Fprint(w, "Usage:\n")
	for _, c := range serviceCommands {
		fmt.Fprintf(w, "  jec service %-9s  %s\n", c.name, c.summary)
	}
	fmt.Fprint(w, "\nRun \"jec service <command> -h\" for its flags. Change the unit with \"systemctl edit jec\".\n")
}

func runService(_ context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		serviceUsage(stderr)
		return exitUsage
	}
	sub := args[0]
	switch {
	case sub == "help" || sub == "-h" || sub == "-help" || sub == "--help":
		serviceUsage(stdout)
		return exitOK
	case !slices.ContainsFunc(serviceCommands, func(c serviceCommand) bool { return c.name == sub }):
		fmt.Fprintf(stderr, "jec service: unknown command %q\n\n", sub)
		serviceUsage(stderr)
		return exitUsage
	}
	fs := flag.NewFlagSet("jec service "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	s := defaultService()
	fs.StringVar(&s.Bin, "bin", s.Bin, "where the binary goes, which the service runs")
	fs.StringVar(&s.DataDir, "data-dir", s.DataDir, "the data directory of the service")
	fs.StringVar(&s.User, "user", s.User, "the user the service runs as, added if missing; root to write anywhere")
	fs.StringVar(&s.Unit, "unit", s.Unit, "the unit file")
	start := true
	force := false
	if sub == "install" {
		fs.BoolVar(&start, "start", start, "start the service, or restart it")
	}
	if sub != "unit" {
		fs.BoolVar(&force, "force", force, "replace or remove a unit that jec service did not write")
	}
	if code, ok := parseFlags(fs, args[1:]); !ok {
		return code
	}
	var err error
	switch sub {
	case "install":
		err = installService(serviceHost, s, start, force, stdout)
	case "uninstall":
		err = uninstallService(serviceHost, s, force, stdout)
	case "unit":
		err = printUnit(s, stdout)
	}
	if err != nil {
		fmt.Fprintf(stderr, "jec service %s: %v\n", sub, err)
		return exitError
	}
	return exitOK
}

func printUnit(s service, out io.Writer) error {
	if err := s.check(); err != nil {
		return err
	}
	unit, err := s.unit()
	if err != nil {
		return err
	}
	_, err = out.Write(unit)
	return err
}

// ready checks s, and that h can run it as a service.
func ready(h host, s service) error {
	if err := s.check(); err != nil {
		return err
	}
	if !h.systemd() {
		return errors.New("systemd does not run this system")
	}
	if !h.root() {
		return errors.New("this changes the system: run it as root, as with sudo")
	}
	return nil
}

// ours tells whether the unit file at path, if any, may be replaced or
// removed: install wrote it, or force says so.
func ours(path string, force bool) (exists bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	first, _, _ := bytes.Cut(data, []byte("\n"))
	if !force && !bytes.Contains(first, []byte(unitMarker)) {
		return true, fmt.Errorf("jec service did not write %s: move it away, or add -force", path)
	}
	return true, nil
}

func installService(h host, s service, start, force bool, out io.Writer) error {
	if err := ready(h, s); err != nil {
		return err
	}
	unit, err := s.unit()
	if err != nil {
		return err
	}
	if _, err := ours(s.Unit, force); err != nil {
		return err
	}
	if err := installBinary(h, s.Bin, out); err != nil {
		return err
	}
	if err := addUser(h, s, out); err != nil {
		return err
	}
	if s.stateDirectory() == "" {
		if err := makeDataDir(h, s); err != nil {
			return err
		}
	}
	if err := writeAtomic(s.Unit, unit, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote %s.\n", s.Unit)
	steps := [][]string{{"daemon-reload"}, {"enable", s.name()}}
	if start {
		steps = append(steps, []string{"restart", s.name()})
	}
	for _, args := range steps {
		if err := h.run(out, "systemctl", args...); err != nil {
			return err
		}
	}
	if !start {
		fmt.Fprintf(out, "Start it with: systemctl start %s\n", s.name())
		return nil
	}
	fmt.Fprintf(out, "JEC runs as %s, with its data in %s. Open http://<this host>:8080 (by default)\n"+
		"and, the first time, enter the setup token that this prints:\n  journalctl -u %s | grep setup_token\n",
		s.name(), s.DataDir, s.name())
	return nil
}

// installBinary copies the running binary to bin, unless it runs from there.
func installBinary(h host, bin string, out io.Writer) error {
	src, err := h.executable()
	if err != nil {
		return err
	}
	if a, err := os.Stat(src); err != nil {
		return err
	} else if b, err := os.Stat(bin); err == nil && os.SameFile(a, b) {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil { //nolint:gosec // G301: a directory of programs, as /usr/local/bin
		return err
	}
	if err := writeAtomic(bin, data, 0o755); err != nil {
		return err
	}
	fmt.Fprintf(out, "Copied %s to %s.\n", src, bin)
	return nil
}

// addUser adds the system user of s, if missing.
func addUser(h host, s service, out io.Writer) error {
	if s.User == "root" {
		return nil
	}
	_, _, err := h.lookupUser(s.User)
	var unknown user.UnknownUserError
	if !errors.As(err, &unknown) {
		return err // nil when the user exists
	}
	shell := "/bin/false"
	for _, p := range []string{"/usr/sbin/nologin", "/sbin/nologin"} {
		if _, err := os.Stat(p); err == nil {
			shell = p
			break
		}
	}
	if err := h.run(out, "useradd", "--system", "--user-group", "--home-dir", s.DataDir, "--no-create-home", "--shell", shell, s.User); err != nil {
		return err
	}
	fmt.Fprintf(out, "Added the system user %s.\n", s.User)
	return nil
}

// makeDataDir creates a data directory outside /var/lib, which systemd
// leaves alone, for the user of s.
func makeDataDir(h host, s service) error {
	uid, gid, err := h.lookupUser(s.User)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.DataDir, 0o700); err != nil {
		return err
	}
	return os.Chown(s.DataDir, uid, gid)
}

func uninstallService(h host, s service, force bool, out io.Writer) error {
	if err := ready(h, s); err != nil {
		return err
	}
	exists, err := ours(s.Unit, force)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%s does not exist: JEC is not installed as a service there", s.Unit)
	}
	if err := h.run(out, "systemctl", "disable", "--now", s.name()); err != nil {
		return err
	}
	if err := os.Remove(s.Unit); err != nil {
		return err
	}
	if err := h.run(out, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	kept := []string{"the data in " + s.DataDir, "the binary " + s.Bin}
	if s.User != "root" {
		kept = append(kept, "the user "+s.User)
	}
	if _, err := os.Stat(s.Unit + ".d"); err == nil {
		kept = append(kept, "the changes of systemctl edit in "+s.Unit+".d")
	}
	fmt.Fprintf(out, "Stopped %s and removed %s. Kept %s.\n", s.name(), s.Unit, strings.Join(kept, ", "))
	return nil
}

// writeAtomic writes data to path through a file beside it, so that path
// is never half written, nor a running binary changed in place.
func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // after the rename, there is none
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
