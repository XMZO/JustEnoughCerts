package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/config"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/httpserver"
)

func runHealthcheck(ctx context.Context, args []string, _, stderr io.Writer) int {
	fs := flag.NewFlagSet("jec healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	loader := config.Bind(fs)
	timeout := fs.Duration("timeout", 3*time.Second, "how long to wait for an answer")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	cfg, err := loader.Load(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(stderr, "jec healthcheck:", err)
		return exitError
	}
	target, err := healthURL(cfg.Listen, cfg.TLS())
	if err != nil {
		fmt.Fprintln(stderr, "jec healthcheck:", err)
		return exitError
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if err := probe(ctx, target); err != nil {
		fmt.Fprintln(stderr, "jec healthcheck:", err)
		return exitError
	}
	return exitOK
}

// healthURL returns the health endpoint of a server listening on listen, as
// reached from the same host, over HTTPS when it serves that.
func healthURL(listen string, https bool) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", err
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	scheme := "http://"
	if https {
		scheme = "https://"
	}
	return scheme + net.JoinHostPort(host, port) + httpserver.HealthPath, nil
}

// probeClient never uses a proxy: the target is always this host, whose
// certificate is for the names it is reached by elsewhere.
var probeClient = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // this host

func probe(ctx context.Context, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", target, resp.Status)
	}
	return nil
}
