// Command jec runs the Just Enough Certs server and its helper commands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // zoneinfo for the scratch release image, which has none

	_ "golang.org/x/crypto/x509roots/fallback" // CA roots, for the same reason

	"github.com/XMZO/JustEnoughCerts/internal/kernel/children"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
	// exitRestart ends a process that has to start again, as to restore a
	// backup: whatever started it, such as Docker, starts it again.
	exitRestart = 3
)

// command is a jec subcommand.
type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string, stdout, stderr io.Writer) int
}

func commands() []command {
	return []command{
		{"serve", "run the server", runServe},
		{"healthcheck", "exit 0 if the local server is up (for container health checks)", runHealthcheck},
		{"reset-password", "set a new password for an account, turn its two-factor authentication off and sign it out everywhere", runResetPassword},
		{"import-sqlite", "copy the SQLite database into the PostgreSQL that JEC_DATABASE_URL names", runImportSQLite},
		{"doctor", "check the data directory, the database, the master key, the network to a CA and the clock", runDoctor},
		{"service", "install or uninstall JEC as a systemd service", runService},
		{"version", "print version information", runVersion},
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "help", "-h", "-help", "--help":
		usage(stdout)
		return exitOK
	case "-version", "--version":
		return runVersion(ctx, nil, stdout, stderr)
	case children.Command:
		// Not in the usage: the server starts these itself, with its DNS
		// resolvers, if any.
		if servers, err := dnscheck.ParseResolvers(os.Getenv(dnscheck.ResolversEnv)); err == nil {
			dnscheck.UseResolvers(servers)
		}
		return children.Main(ctx)
	}
	for _, c := range commands() {
		if c.name == args[0] {
			return c.run(ctx, args[1:], stdout, stderr)
		}
	}
	fmt.Fprintf(stderr, "jec: unknown command %q\n\n", args[0])
	usage(stderr)
	return exitUsage
}

func usage(w io.Writer) {
	fmt.Fprint(w, "Just Enough Certs: self-hosted SSL/TLS certificate management.\n\n")
	fmt.Fprint(w, "Usage:\n  jec <command> [flags]\n\nCommands:\n")
	width := 0
	for _, c := range commands() {
		width = max(width, len(c.name))
	}
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-*s  %s\n", width, c.name, c.summary)
	}
	fmt.Fprint(w, "\nRun \"jec <command> -h\" for the flags of a command.\n")
}

// parseFlags parses args into fs. When the command should not go on, after
// -h or on a usage error, it returns false and the exit code to return.
func parseFlags(fs *flag.FlagSet, args []string) (int, bool) {
	err := fs.Parse(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return exitOK, false
	case err != nil:
		return exitUsage, false // fs has already printed the error
	case fs.NArg() > 0:
		fmt.Fprintf(fs.Output(), "%s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return exitUsage, false
	}
	return exitOK, true
}
