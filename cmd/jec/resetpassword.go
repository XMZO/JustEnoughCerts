package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/auth"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

// stdin is where reset-password reads the new password; tests replace it.
var stdin = os.Stdin

func runResetPassword(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jec reset-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	loader := config.Bind(fs)
	username := fs.String("username", "", "the account to change (default: the only one)")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	cfg, err := loader.Load(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(stderr, "jec reset-password:", err)
		return exitError
	}
	name, err := resetPassword(ctx, cfg, *username, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "jec reset-password:", err)
		return exitError
	}
	fmt.Fprintf(stdout, "Changed the password of %s and signed it out everywhere.\n", name)
	return exitOK
}

func resetPassword(ctx context.Context, cfg config.Config, username string, prompt io.Writer) (string, error) {
	st, err := store.OpenURL(ctx, cfg.DataDir, cfg.DatabaseURL)
	if err != nil {
		return "", err
	}
	defer st.Close()
	if _, err := st.Migrate(ctx, "kernel", migrations.FS); err != nil {
		return "", err
	}
	if username == "" {
		names, err := auth.Usernames(ctx, st.DB())
		if err != nil {
			return "", err
		}
		switch len(names) {
		case 0:
			return "", errors.New("there is no account yet: open the web UI to set one up")
		case 1:
			username = names[0]
		default:
			return "", fmt.Errorf("there are several accounts, name one with -username: %s", strings.Join(names, ", "))
		}
	}
	password, err := readNewPassword(stdin, prompt)
	if err != nil {
		return "", err
	}
	return username, auth.SetPassword(ctx, st.DB(), username, password)
}

// readNewPassword asks for the password twice, without echo, on a
// terminal; otherwise it reads one line, as from a pipe.
func readNewPassword(in *os.File, prompt io.Writer) (string, error) {
	fd := int(in.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	ask := func(label string) (string, error) {
		fmt.Fprint(prompt, label)
		p, err := term.ReadPassword(fd)
		fmt.Fprintln(prompt)
		return string(p), err
	}
	first, err := ask("New password: ")
	if err != nil {
		return "", err
	}
	again, err := ask("Repeat it: ")
	if err != nil {
		return "", err
	}
	if first != again {
		return "", errors.New("the two passwords differ")
	}
	return first, nil
}
