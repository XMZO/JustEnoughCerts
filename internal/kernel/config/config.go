// Package config resolves the process configuration.
//
// Each setting comes from, in increasing order of precedence: a built-in
// default, the YAML config file, a JEC_* environment variable and a
// command-line flag.
package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Config is the resolved process configuration.
type Config struct {
	// DataDir holds the database, the encryption key and logs. The config
	// file is looked up there by default, so the file cannot move it.
	DataDir string `yaml:"-"`
	// Listen is the TCP address the HTTP server binds to.
	Listen string `yaml:"listen"`
	// BaseURL is the URL users open in the browser. It is needed when that
	// differs from what the server sees: behind a reverse proxy, or under a
	// sub-path. Empty means the server is reached directly at its root.
	BaseURL string `yaml:"base_url"`
	// HSTS is "true" for answers to say Strict-Transport-Security: browsers
	// then reach the panel over HTTPS alone for a year, so it is only for a
	// panel never served over plain HTTP. "false" by default.
	HSTS string `yaml:"hsts"`
	// Log configures the process log.
	Log Log `yaml:"log"`
	// SecretKey is the master key sealing the stored secrets, base64 of 32
	// bytes, from JEC_SECRET_KEY only: never a flag, which other processes
	// can read, nor the config file. Empty means the key file in DataDir.
	SecretKey string `yaml:"-"`
}

// Log configures the process log.
type Log struct {
	// Level is debug, info, warn or error.
	Level string `yaml:"level"`
	// Format is text or json.
	Format string `yaml:"format"`
}

// FileName is the name of the config file looked up in the data directory.
const FileName = "config.yaml"

// Defaults returns the built-in configuration.
func Defaults() Config {
	return Config{
		DataDir: "data",
		Listen:  ":8080",
		HSTS:    "false",
		Log:     Log{Level: "info", Format: "text"},
	}
}

// BasePath returns the path of BaseURL without a trailing slash, or "" when
// the UI is served at the root.
func (c Config) BasePath() string {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return ""
	}
	return strings.TrimRight(u.Path, "/")
}

// SlogLevel returns Level as a slog.Level.
func (l Log) SlogLevel() (slog.Level, error) {
	var level slog.Level
	err := level.UnmarshalText([]byte(l.Level))
	return level, err
}

// setting is a string option that can be given as a flag or as an
// environment variable.
type setting struct {
	flag, env, usage string
	field            func(*Config) *string
}

var settings = []setting{
	{"data-dir", "JEC_DATA_DIR", "directory for the database, keys and logs", func(c *Config) *string { return &c.DataDir }},
	{"listen", "JEC_LISTEN", "TCP address to serve HTTP on", func(c *Config) *string { return &c.Listen }},
	{"base-url", "JEC_BASE_URL", "URL users open in the browser, if it differs from what the server sees", func(c *Config) *string { return &c.BaseURL }},
	{"hsts", "JEC_HSTS", "true to send Strict-Transport-Security, when the panel is reached over HTTPS alone", func(c *Config) *string { return &c.HSTS }},
	{"log-level", "JEC_LOG_LEVEL", "log level: debug, info, warn or error", func(c *Config) *string { return &c.Log.Level }},
	{"log-format", "JEC_LOG_FORMAT", "log format: text or json", func(c *Config) *string { return &c.Log.Format }},
}

const (
	fileFlag = "config"
	fileEnv  = "JEC_CONFIG"
	keyEnv   = "JEC_SECRET_KEY"
)

// Loader resolves the configuration of one command from its flags, the
// environment and the config file.
type Loader struct {
	flags  *flag.FlagSet
	file   *string
	values map[string]*string // by flag name
}

// Bind registers the configuration flags on fs. Call Load after fs.Parse.
func Bind(fs *flag.FlagSet) *Loader {
	def := Defaults()
	l := &Loader{flags: fs, values: make(map[string]*string, len(settings))}
	l.file = fs.String(fileFlag, "", fmt.Sprintf("YAML config file (env %s; default <data-dir>/%s if it exists)", fileEnv, FileName))
	for _, s := range settings {
		l.values[s.flag] = fs.String(s.flag, *s.field(&def), fmt.Sprintf("%s (env %s)", s.usage, s.env))
	}
	return l
}

// Load resolves the configuration. lookupEnv is normally os.LookupEnv; a
// variable set to the empty string counts as unset.
func (l *Loader) Load(lookupEnv func(string) (string, bool)) (Config, error) {
	given := make(map[string]bool)
	l.flags.Visit(func(f *flag.Flag) { given[f.Name] = true })
	env := func(key string) (string, bool) {
		v, ok := lookupEnv(key)
		return v, ok && v != ""
	}
	override := func(c *Config, s setting) {
		if v, ok := env(s.env); ok {
			*s.field(c) = v
		}
		if given[s.flag] {
			*s.field(c) = *l.values[s.flag]
		}
	}

	cfg := Defaults()
	// The config file is looked up in the data directory, so that one is
	// resolved from the environment and flags before the file is read.
	for _, s := range settings {
		if s.flag == "data-dir" {
			override(&cfg, s)
		}
	}
	path, required := filepath.Join(cfg.DataDir, FileName), false
	if v, ok := env(fileEnv); ok {
		path, required = v, true
	}
	if given[fileFlag] {
		path, required = *l.file, true
	}
	if path != "" {
		if err := readFile(&cfg, path, required); err != nil {
			return Config{}, err
		}
	}
	for _, s := range settings {
		override(&cfg, s)
	}
	if v, ok := env(keyEnv); ok {
		cfg.SecretKey = v
	}
	if err := cfg.normalize(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func readFile(cfg *Config, path string, required bool) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !required {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config file: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	return nil
}

// normalize validates c and rewrites values into canonical form.
func (c *Config) normalize() error {
	if c.DataDir == "" {
		return errors.New("data directory is empty")
	}
	dir, err := filepath.Abs(c.DataDir)
	if err != nil {
		return fmt.Errorf("data directory: %w", err)
	}
	c.DataDir = dir

	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", c.Listen, err)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("listen address %q: invalid port", c.Listen)
	}

	if c.BaseURL != "" {
		if c.BaseURL, err = normalizeBaseURL(c.BaseURL); err != nil {
			return err
		}
	}

	c.HSTS = strings.ToLower(c.HSTS)
	if c.HSTS != "true" && c.HSTS != "false" {
		return fmt.Errorf("hsts %q: want true or false", c.HSTS)
	}

	if _, err := c.Log.SlogLevel(); err != nil {
		return fmt.Errorf("log level %q: want debug, info, warn or error", c.Log.Level)
	}
	c.Log.Format = strings.ToLower(c.Log.Format)
	if c.Log.Format != "text" && c.Log.Format != "json" {
		return fmt.Errorf("log format %q: want text or json", c.Log.Format)
	}
	return nil
}

func normalizeBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("base URL %q: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("base URL %q: want an absolute http or https URL", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("base URL %q: must not contain credentials, a query or a fragment", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}
