package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo"
	kernelsystem "github.com/XMZO/JustEnoughCerts/internal/kernel/system"
)

// The places jec upgrade reads from, and the files it changes; tests set
// them.
var (
	releasesList = kernelsystem.ReleasesURL
	downloadBase = kernelsystem.DownloadURL
	// dockerEnv is the file a container of Docker has.
	dockerEnv = "/.dockerenv"
	// unitFile is the unit jec service install writes.
	unitFile = "/etc/systemd/system/jec.service"
	// executable is the binary to replace.
	executable = os.Executable
	inImage    = kernelsystem.InImage
)

// maxArchive bounds the archive jec upgrade downloads.
const maxArchive = 256 << 20

// runUpgrade replaces the binary with that of a newer release: the newest
// this version may move to, or the one asked for. The archive is checked
// against the release's checksums.txt, and the new binary must run before
// it takes the old one's place; a service keeps running the old one until
// it restarts.
func runUpgrade(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jec upgrade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("version", "", "the release to move to, such as 0.1.0; the newest when left out")
	check := fs.Bool("check", false, "only tell whether a newer release is out")
	restart := fs.Bool("restart", false, "restart the jec systemd service afterwards")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if _, err := os.Stat(dockerEnv); err == nil || inImage() {
		fmt.Fprintln(stderr, "jec upgrade: JEC runs in a container: pull the new image instead, as with docker compose pull && docker compose up -d")
		return exitError
	}
	bin, err := executable()
	if err == nil {
		bin, err = filepath.EvalSymlinks(bin)
	}
	if err != nil {
		fmt.Fprintln(stderr, "jec upgrade:", err)
		return exitError
	}
	u := upgrader{client: &http.Client{Timeout: 5 * time.Minute}, out: stdout, bin: bin, current: buildinfo.Get().Version}
	target := strings.TrimPrefix(*version, "v")
	if target == "" {
		r, err := kernelsystem.LatestRelease(ctx, u.client, releasesList, u.current)
		switch {
		case err != nil:
			fmt.Fprintln(stderr, "jec upgrade: asking GitHub for the releases:", err)
			return exitError
		case r.Latest == "" || !r.Newer:
			fmt.Fprintf(stdout, "jec %s is the newest release it may move to.\n", u.current)
			return exitOK
		}
		target = r.Latest
		fmt.Fprintf(stdout, "jec %s is out (running %s): %s\n", target, u.current, r.URL)
	}
	if *check {
		return exitOK
	}
	if err := u.upgrade(ctx, target); err != nil {
		fmt.Fprintln(stderr, "jec upgrade:", err)
		return exitError
	}
	fmt.Fprintf(stdout, "%s is now jec %s.\n", bin, target)
	if _, err := os.Stat(unitFile); err != nil {
		fmt.Fprintln(stdout, "Restart JEC to run it; the database is migrated, after a backup, when it starts.")
		return exitOK
	}
	if !*restart {
		fmt.Fprintln(stdout, "Restart the service to run it: sudo systemctl restart jec")
		return exitOK
	}
	if out, err := exec.CommandContext(ctx, "systemctl", "restart", "jec").CombinedOutput(); err != nil {
		fmt.Fprintf(stderr, "jec upgrade: systemctl restart jec: %v %s\n", err, out)
		return exitError
	}
	fmt.Fprintln(stdout, "The service restarted.")
	return exitOK
}

// upgrader replaces bin with the binary of a release.
type upgrader struct {
	client  *http.Client
	out     io.Writer
	bin     string
	current string
}

// archive is the name of the release's archive for this platform, as
// GoReleaser names it; 32-bit ARM builds are for ARMv7.
func archive(version string) string {
	arch := runtime.GOARCH
	if arch == "arm" {
		arch = "armv7"
	}
	return fmt.Sprintf("jec_%s_%s_%s.tar.gz", version, runtime.GOOS, arch)
}

func (u upgrader) upgrade(ctx context.Context, version string) error {
	name := archive(version)
	base := downloadBase + "/v" + version + "/"
	sums, err := u.fetch(ctx, base+"checksums.txt", 1<<20)
	if err != nil {
		return err
	}
	want := ""
	for line := range strings.SplitSeq(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	if want == "" {
		return fmt.Errorf("release %s has no %s for this platform", version, name)
	}
	fmt.Fprintf(u.out, "Downloading %s ...\n", name)
	data, err := u.fetch(ctx, base+name, maxArchive)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s does not match its SHA-256 in checksums.txt", name)
	}
	program, err := extract(data)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	// The new binary goes next to the old one, so that a rename replaces
	// it at once; a running service keeps the old one open meanwhile.
	tmp, err := os.CreateTemp(filepath.Dir(u.bin), ".jec-upgrade-*")
	if err != nil {
		return fmt.Errorf("%w (run it as the binary's owner, as with sudo)", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(program); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := errors.Join(tmp.Chmod(0o755), tmp.Close()); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, tmp.Name(), "version").Output() //nolint:gosec // G204: the binary just checked against the release's checksums
	if err != nil {
		return fmt.Errorf("the new binary does not run here: %w", err)
	}
	if first, _, _ := strings.Cut(string(out), "\n"); first != "jec "+version {
		return fmt.Errorf("the new binary is not jec %s: it says %q", version, first)
	}
	return os.Rename(tmp.Name(), u.bin)
}

// fetch GETs url, up to limit bytes.
func (u upgrader) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: more than %d bytes", url, limit)
	}
	return data, nil
}

// extract returns the program jec of a release's archive.
func extract(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no jec in it")
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && strings.TrimPrefix(h.Name, "./") == "jec" {
			return io.ReadAll(io.LimitReader(tr, maxArchive))
		}
	}
}
