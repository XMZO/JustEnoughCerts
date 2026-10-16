package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo"
)

func runVersion(_ context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jec version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	bi := buildinfo.Get()
	fmt.Fprintf(stdout, "jec %s\n", bi.Version)
	for _, f := range []struct{ name, value string }{
		{"commit", bi.Commit},
		{"date", bi.Date},
		{"go", bi.GoVersion + " " + bi.Platform},
	} {
		if f.value != "" {
			fmt.Fprintf(stdout, "  %-7s %s\n", f.name+":", f.value)
		}
	}
	return exitOK
}
