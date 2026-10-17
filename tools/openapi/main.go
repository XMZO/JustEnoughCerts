// Command openapi writes the OpenAPI document of the API, with every module
// of package all, to web/openapi.json; the web UI's API types are generated
// from it.
//
//	go run ./tools/openapi [-o web/openapi.json] [-check]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/XMZO/JustEnoughCerts/internal/kernel"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/modules/all"
)

func main() {
	out := flag.String("o", "web/openapi.json", "output file")
	check := flag.Bool("check", false, "fail if the file is not up to date instead of writing it")
	flag.Parse()
	if err := run(*out, *check); err != nil {
		fmt.Fprintln(os.Stderr, "openapi:", err)
		os.Exit(1)
	}
}

func run(out string, check bool) error {
	dir, err := os.MkdirTemp("", "jec-openapi-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cfg := config.Defaults()
	cfg.DataDir = dir
	app, err := kernel.New(context.Background(), cfg, slog.New(slog.DiscardHandler), all.Modules())
	if err != nil {
		return err
	}
	defer app.Close()

	doc := app.OpenAPI()
	doc.Info.Version = "dev" // keep the file stable from build to build
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if check {
		old, err := os.ReadFile(out)
		if err != nil || !bytes.Equal(bytes.ReplaceAll(old, []byte("\r\n"), []byte("\n")), data) {
			return fmt.Errorf("%s is stale: run go run ./tools/openapi, then pnpm -C web gen:api", out)
		}
		return nil
	}
	return os.WriteFile(out, data, 0o644) //nolint:gosec // G306: a source file
}
