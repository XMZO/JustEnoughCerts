package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/httpserver"
)

func runServe(ctx context.Context, args []string, _, stderr io.Writer) int {
	fs := flag.NewFlagSet("jec serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	loader := config.Bind(fs)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	cfg, err := loader.Load(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(stderr, "jec serve:", err)
		return exitError
	}
	log := newLogger(cfg.Log, stderr)
	if err := serve(ctx, cfg, log); err != nil {
		log.Error("server failed", "err", err)
		return exitError
	}
	return exitOK
}

func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	if err := checkDataDir(cfg.DataDir); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	bi := buildinfo.Get()
	log.Info("starting",
		"version", bi.Version, "commit", bi.Commit,
		"listen", ln.Addr().String(), "base_url", cfg.BaseURL, "data_dir", cfg.DataDir)
	defer log.Info("stopped")
	return httpserver.New(cfg, log).Serve(ctx, ln)
}

// newLogger returns the process logger. Timestamps are always UTC, so logs
// line up across hosts whatever TZ is set to.
func newLogger(c config.Log, w io.Writer) *slog.Logger {
	level, _ := c.SlogLevel() // validated by config.Load
	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey && len(groups) == 0 {
				a.Value = slog.TimeValue(a.Value.Time().UTC())
			}
			return a
		},
	}
	if c.Format == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

// checkDataDir creates the data directory and makes sure it is writable, so
// that a wrong volume mount or ownership fails at startup with a clear
// message instead of at the first write.
func checkDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("data directory: %w", err)
	}
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return fmt.Errorf("data directory %s is not writable by uid %d gid %d (chown it to that user, or run as its owner): %w",
			dir, os.Getuid(), os.Getgid(), err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		return fmt.Errorf("data directory: %w", err)
	}
	return os.Remove(name)
}
