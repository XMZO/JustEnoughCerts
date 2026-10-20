package config

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// load parses args and resolves the configuration against env only, so the
// tests never see the developer's own JEC_* variables.
func load(t *testing.T, args []string, env map[string]string) (Config, error) {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	l := Bind(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %q: %v", args, err)
	}
	return l.Load(func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := load(t, []string{"--data-dir", dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{DataDir: dir, Listen: ":8080", HSTS: "false", Log: Log{Level: "info", Format: "text"}}
	if cfg != want {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
	if got := cfg.BasePath(); got != "" {
		t.Errorf("BasePath() = %q, want empty", got)
	}
}

func TestPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, FileName), "listen: \":7000\"\nlog:\n  level: debug\n")

	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"file", nil, nil, ":7000"},
		{"env over file", nil, map[string]string{"JEC_LISTEN": ":7001"}, ":7001"},
		{"flag over env", []string{"--listen", ":7002"}, map[string]string{"JEC_LISTEN": ":7001"}, ":7002"},
		{"empty env is unset", nil, map[string]string{"JEC_LISTEN": ""}, ":7000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := load(t, append([]string{"--data-dir", dir}, tt.args...), tt.env)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Listen != tt.want {
				t.Errorf("Listen = %q, want %q", cfg.Listen, tt.want)
			}
			// A partial log section keeps the defaults it does not mention.
			if cfg.Log != (Log{Level: "debug", Format: "text"}) {
				t.Errorf("Log = %+v", cfg.Log)
			}
		})
	}
}

func TestDataDirFromEnvLocatesFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, FileName), "listen: \":7000\"\n")
	cfg, err := load(t, nil, map[string]string{"JEC_DATA_DIR": dir})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != dir || cfg.Listen != ":7000" {
		t.Errorf("got %+v", cfg)
	}
}

func TestExplicitConfigFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(t.TempDir(), "jec.yaml")
	writeFile(t, file, "base_url: https://example.com/certs/\n")

	tests := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{"flag", []string{"--config", file}, nil},
		{"env", nil, map[string]string{"JEC_CONFIG": file}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := load(t, append([]string{"--data-dir", dir}, tt.args...), tt.env)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.BaseURL != "https://example.com/certs" || cfg.BasePath() != "/certs" {
				t.Errorf("BaseURL = %q, BasePath() = %q", cfg.BaseURL, cfg.BasePath())
			}
		})
	}
}

func TestFileErrors(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.yaml")
	unknown := filepath.Join(dir, "unknown.yaml")
	writeFile(t, unknown, "listen: \":7000\"\nlisten_port: 7000\n")
	dataDirKey := filepath.Join(dir, "datadir.yaml")
	writeFile(t, dataDirKey, "data_dir: /elsewhere\n")

	for name, file := range map[string]string{
		"missing explicit file": missing,
		"unknown key":           unknown,
		"data dir in file":      dataDirKey,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := load(t, []string{"--data-dir", dir, "--config", file}, nil); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, FileName), "")
	if _, err := load(t, []string{"--data-dir", dir}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidValues(t *testing.T) {
	dir := t.TempDir()
	tests := map[string][]string{
		"listen without port":   {"--listen", "8080"},
		"listen bad port":       {"--listen", ":99999"},
		"base URL not absolute": {"--base-url", "certs.example.com"},
		"base URL scheme":       {"--base-url", "ftp://example.com"},
		"base URL query":        {"--base-url", "https://example.com/?a=1"},
		"base URL user":         {"--base-url", "https://u:p@example.com"},
		"log level":             {"--log-level", "loud"},
		"log format":            {"--log-format", "xml"},
		"hsts":                  {"--hsts", "yes"},
		"empty data dir":        {"--data-dir", ""},
		"TLS without a key":     {"--tls-cert", "cert.pem"},
		"TLS without a cert":    {"--tls-key", "key.pem"},
		"HTTP without TLS":      {"--http-listen", ":80"},
		"HTTP without port":     {"--tls-cert", "c.pem", "--tls-key", "k.pem", "--http-listen", "80"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := load(t, append([]string{"--data-dir", dir}, args...), nil)
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.TrimSpace(err.Error()) == "" {
				t.Error("empty error message")
			}
		})
	}
}

func TestNormalizedValues(t *testing.T) {
	cfg, err := load(t, []string{
		"--data-dir", "relative/dir",
		"--base-url", "https://example.com/",
		"--log-level", "WARN",
		"--log-format", "JSON",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(cfg.DataDir) {
		t.Errorf("DataDir %q is not absolute", cfg.DataDir)
	}
	if cfg.BaseURL != "https://example.com" || cfg.BasePath() != "" {
		t.Errorf("BaseURL = %q, BasePath() = %q", cfg.BaseURL, cfg.BasePath())
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Format = %q", cfg.Log.Format)
	}
	if level, err := cfg.Log.SlogLevel(); err != nil || level.String() != "WARN" {
		t.Errorf("SlogLevel() = %v, %v", level, err)
	}
}

func TestSecretKeyFromEnvOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, FileName), "listen: \":7000\"\n")
	cfg, err := load(t, []string{"--data-dir", dir}, map[string]string{"JEC_SECRET_KEY": "c2VjcmV0"})
	if err != nil || cfg.SecretKey != "c2VjcmV0" {
		t.Errorf("SecretKey = %q, %v", cfg.SecretKey, err)
	}
	writeFile(t, filepath.Join(dir, FileName), "secret_key: c2VjcmV0\n")
	if _, err := load(t, []string{"--data-dir", dir}, nil); err == nil {
		t.Error("the config file set the secret key")
	}
}
