package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/XMZO/JustEnoughCerts/internal/kernel"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

// runImportSQLite copies the SQLite database of the data directory, or
// another one, into the PostgreSQL that the configuration names: the way
// from one to the other. JEC is to be stopped meanwhile.
func runImportSQLite(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jec import-sqlite", flag.ContinueOnError)
	fs.SetOutput(stderr)
	loader := config.Bind(fs)
	from := fs.String("from", "", "the SQLite database to copy (default: jec.db in the data directory)")
	replace := fs.Bool("replace", false, "replace what the PostgreSQL database holds already")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	cfg, err := loader.Load(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(stderr, "jec import-sqlite:", err)
		return exitError
	}
	if cfg.DatabaseURL == "" {
		fmt.Fprintln(stderr, "jec import-sqlite: set JEC_DATABASE_URL to the PostgreSQL database to copy into")
		return exitUsage
	}
	path := *from
	if path == "" {
		path = filepath.Join(cfg.DataDir, store.FileName)
	}
	st, err := store.OpenURL(ctx, cfg.DataDir, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(stderr, "jec import-sqlite:", err)
		return exitError
	}
	defer st.Close()
	err = kernel.ImportSQLite(ctx, path, st, slog.New(slog.NewTextHandler(stderr, nil)), modules, *replace)
	switch {
	case errors.Is(err, kernel.ErrNotEmpty):
		fmt.Fprintln(stderr, "jec import-sqlite: the PostgreSQL database holds data already; -replace replaces it")
		return exitError
	case err != nil:
		fmt.Fprintln(stderr, "jec import-sqlite:", err)
		return exitError
	}
	fmt.Fprintf(stdout, "Copied %s into PostgreSQL. Keep the master key: the secrets are sealed with it.\n", path)
	return exitOK
}
