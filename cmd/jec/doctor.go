package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
)

// defaultCA is the ACME directory doctor reaches unless told another.
const defaultCA = "https://acme-v02.api.letsencrypt.org/directory"

// Clock offsets doctor warns of and fails on: a code of two-factor
// authentication lives 30 seconds, and CAs date certificates by their
// clock.
const (
	clockWarn = 30 * time.Second
	clockFail = 5 * time.Minute
)

// doctorClient reaches the CA without the proxy of the environment, as
// JEC's own clients do; tests replace it.
var doctorClient = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{}}

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jec doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	loader := config.Bind(fs)
	offline := fs.Bool("offline", false, "skip the checks that reach the internet")
	ca := fs.String("ca", defaultCA, "the ACME directory to reach, for the network and the clock")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	cfg, err := loader.Load(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(stderr, "jec doctor:", err)
		return exitError
	}
	d := &doctor{out: stdout}
	d.local(ctx, cfg)
	if !*offline {
		d.network(ctx, cfg, *ca)
	}
	if d.failed {
		fmt.Fprintln(stdout, "\nSomething is wrong: see the lines marked FAIL.")
		return exitError
	}
	fmt.Fprintln(stdout, "\nNothing is wrong.")
	return exitOK
}

// doctor prints what it checks, a line each.
type doctor struct {
	out    io.Writer
	failed bool
}

func (d *doctor) ok(what, format string, args ...any)   { d.line("ok", what, format, args...) }
func (d *doctor) warn(what, format string, args ...any) { d.line("WARN", what, format, args...) }
func (d *doctor) info(what, format string, args ...any) { d.line("--", what, format, args...) }
func (d *doctor) fail(what, format string, args ...any) {
	d.failed = true
	d.line("FAIL", what, format, args...)
}

func (d *doctor) line(mark, what, format string, args ...any) {
	fmt.Fprintf(d.out, "[%-4s] %-13s %s\n", mark, what, fmt.Sprintf(format, args...))
}

// local checks the data directory, the database and the master key.
func (d *doctor) local(ctx context.Context, cfg config.Config) {
	if err := checkDataDir(cfg.DataDir); err != nil {
		d.fail("data", "%v", err)
		return
	}
	d.ok("data", "%s is writable", cfg.DataDir)

	if _, err := os.Stat(filepath.Join(cfg.DataDir, store.RestoreFile)); err == nil {
		d.info("restore", "%s waits to take the database's place at the next start", store.RestoreFile)
	}

	// The database, as it is: doctor makes none.
	if cfg.DatabaseURL == "" {
		if _, err := os.Stat(filepath.Join(cfg.DataDir, store.FileName)); errors.Is(err, fs.ErrNotExist) {
			d.info("database", "none yet: JEC makes it when it first starts")
			d.key(ctx, cfg, nil)
			return
		}
	}
	st, err := store.OpenURL(ctx, cfg.DataDir, cfg.DatabaseURL)
	if err != nil {
		d.fail("database", "%v", err)
		return
	}
	defer func() { _ = st.Close() }()
	where := filepath.Join(cfg.DataDir, store.FileName)
	if cfg.DatabaseURL != "" {
		where = "PostgreSQL"
	}
	exists := "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'kernel_users'"
	if st.Dialect() == store.Postgres {
		exists = "SELECT count(*) FROM pg_tables WHERE schemaname = current_schema() AND tablename = 'kernel_users'"
	}
	var tables, accounts int
	if err := st.DB().QueryRowContext(ctx, exists).Scan(&tables); err != nil {
		d.fail("database", "%v", err)
		return
	}
	if tables == 0 {
		d.info("database", "%s opens, empty: JEC makes its tables when it first starts", where)
		d.key(ctx, cfg, nil)
		return
	}
	if err := st.DB().QueryRowContext(ctx, "SELECT count(*) FROM kernel_users").Scan(&accounts); err != nil {
		d.fail("database", "%v", err)
		return
	}
	d.ok("database", "%s opens, %d account(s)", where, accounts)
	d.key(ctx, cfg, st.DB())
}

// key checks that the master key opens the secrets of db, if any.
func (d *doctor) key(ctx context.Context, cfg config.Config, db *sql.DB) {
	var check []byte
	if db != nil {
		err := db.QueryRowContext(ctx, "SELECT value FROM kernel_meta WHERE name = 'secret_check'").Scan(&check)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			d.fail("master key", "%v", err)
			return
		}
	}
	path := filepath.Join(cfg.DataDir, secrets.KeyFile)
	if cfg.SecretKey == "" {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			if check != nil {
				d.fail("master key", "%s is missing, and the database has secrets sealed with it: restore it", path)
				return
			}
			d.info("master key", "none yet: JEC makes %s when it first starts", secrets.KeyFile)
			return
		}
	}
	box, _, err := secrets.Load(cfg.DataDir, cfg.SecretKey)
	switch {
	case err != nil:
		d.fail("master key", "%v", err)
	case check == nil:
		d.ok("master key", "reads; nothing sealed with it yet")
	case !box.Verify(check):
		d.fail("master key", "not the one the database was sealed with: restore %s, or the JEC_SECRET_KEY used before", secrets.KeyFile)
	default:
		d.ok("master key", "opens the secrets of the database")
	}
}

// network checks that the CA's name resolves and its directory answers,
// and how far the clock is from the CA's.
func (d *doctor) network(ctx context.Context, cfg config.Config, directory string) {
	u, err := url.Parse(directory)
	if err != nil || u.Hostname() == "" {
		d.fail("ca", "%q is no URL", directory)
		return
	}
	host := u.Hostname()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		d.fail("dns", "%s does not resolve through the system's resolvers: %v", host, err)
		return
	}
	d.ok("dns", "%s resolves to %s", host, strings.Join(addrs, ", "))
	if cfg.DNSResolvers != "" {
		servers, err := dnscheck.ParseResolvers(cfg.DNSResolvers)
		if err != nil {
			d.fail("dns", "%v", err)
			return
		}
		dnscheck.UseResolvers(servers)
		if zone, err := dnscheck.Zone(ctx, host); err != nil {
			d.fail("dns", "JEC_DNS_RESOLVERS find no zone of %s: %v", host, err)
		} else {
			d.ok("dns", "JEC_DNS_RESOLVERS find the zone %s", zone)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, directory, http.NoBody)
	if err != nil {
		d.fail("ca", "%v", err)
		return
	}
	start := time.Now()
	resp, err := doctorClient.Do(req)
	if err != nil {
		d.fail("ca", "%s does not answer: %v", directory, err)
		return
	}
	_ = resp.Body.Close()
	took := time.Since(start).Round(time.Millisecond)
	if resp.StatusCode != http.StatusOK {
		d.fail("ca", "%s answered %s", directory, resp.Status)
	} else {
		d.ok("ca", "%s answered in %s", directory, took)
	}
	d.clock(resp.Header.Get("Date"), start.Add(took/2))
}

// clock compares the time the CA said in date with now, local.
func (d *doctor) clock(date string, now time.Time) {
	at, err := http.ParseTime(date)
	if err != nil {
		d.info("clock", "the CA told no time to compare with")
		return
	}
	off := now.Sub(at)
	// Date has whole seconds: a second either way is nothing.
	switch abs := max(off, -off); {
	case abs < clockWarn:
		d.ok("clock", "within %s of the CA's", clockWarn)
	case abs < clockFail:
		d.warn("clock", "%s %s: codes of two-factor authentication may fail; set the time by NTP", abs.Round(time.Second), aheadBehind(off))
	default:
		d.fail("clock", "%s %s: sign-ins with two-factor authentication and certificates' dates go wrong; set the time by NTP", abs.Round(time.Second), aheadBehind(off))
	}
}

func aheadBehind(off time.Duration) string {
	if off > 0 {
		return "ahead of the CA"
	}
	return "behind the CA"
}
