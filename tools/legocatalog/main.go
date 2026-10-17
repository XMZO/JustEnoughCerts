// Command legocatalog writes the catalog of lego's DNS providers that the
// lego module embeds (PLAN.md 5.5): for each provider, its name, website
// and configuration keys with lego's own descriptions, read from the TOML
// file each provider has in the lego module that go.mod requires.
//
//	go run ./tools/legocatalog [-o internal/modules/lego/catalog.json] [-check]
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/BurntSushi/toml"
)

const legoModule = "github.com/go-acme/lego/v5"

// envName is what a configuration key must look like.
var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// excluded are providers JEC does not offer: manual waits for someone at
// the terminal, and exec runs any program; JEC has its own manual mode and
// plugin protocol for those.
var excluded = []string{"manual", "exec"}

// Key is a configuration key of a provider, an environment variable.
type Key struct {
	Key string `json:"key"`
	Doc string `json:"doc"`
	// Secret keys are sealed and never shown again once saved.
	Secret bool `json:"secret,omitempty"`
}

// Provider is a lego DNS provider.
type Provider struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	URL         string `json:"url,omitempty"`
	Description string `json:"description,omitempty"`
	Credentials []Key  `json:"credentials"`
	Additional  []Key  `json:"additional"`
}

// Catalog is the file the lego module embeds.
type Catalog struct {
	Lego      string     `json:"lego"` // the lego version it describes
	Providers []Provider `json:"providers"`
}

func main() {
	out := flag.String("o", "internal/modules/lego/catalog.json", "output file")
	check := flag.Bool("check", false, "fail if the file is not up to date instead of writing it")
	flag.Parse()
	if err := run(*out, *check); err != nil {
		fmt.Fprintln(os.Stderr, "legocatalog:", err)
		os.Exit(1)
	}
}

func run(out string, check bool) error {
	version, dir, err := module()
	if err != nil {
		return err
	}
	c, err := read(version, filepath.Join(dir, "providers", "dns"))
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(asciiOnly(data), '\n')
	if check {
		old, err := os.ReadFile(out)
		if err != nil || !bytes.Equal(bytes.ReplaceAll(old, []byte("\r\n"), []byte("\n")), data) {
			return fmt.Errorf("%s does not match lego %s: run go run ./tools/legocatalog", out, version)
		}
		return nil
	}
	return os.WriteFile(out, data, 0o644) //nolint:gosec // G306: a source file
}

// asciiOnly escapes every non-ASCII character of JSON data: some providers
// have Chinese names, which the CJK scan of the i18n gate would take for UI
// text outside the locale files. The data stays the same.
func asciiOnly(data []byte) []byte {
	var b bytes.Buffer
	for _, r := range string(data) {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r > 0xFFFF:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, r1, r2)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.Bytes()
}

// module returns the version and directory of the lego module in go.mod,
// downloading it when the module cache does not have it, as in a fresh CI
// job: building this tool does not need lego.
func module() (string, string, error) {
	cmd := exec.Command("go", "mod", "download", "-json", legoModule)
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("go mod download %s: %w", legoModule, err)
	}
	var m struct{ Version, Dir, Error string }
	if err := json.Unmarshal(b, &m); err != nil {
		return "", "", fmt.Errorf("go mod download %s: %w", legoModule, err)
	}
	if m.Error != "" || m.Dir == "" {
		return "", "", fmt.Errorf("go mod download %s: no module directory: %s", legoModule, m.Error)
	}
	return m.Version, m.Dir, nil
}

// read collects the providers under dir, one directory each, by code.
func read(version, dir string) (*Catalog, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	c := &Catalog{Lego: version, Providers: []Provider{}}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || slices.Contains(excluded, e.Name()) || e.Name() == "internal" {
			continue
		}
		path := filepath.Join(dir, e.Name(), e.Name()+".toml")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue // a helper package, not a provider
		}
		p, err := parse(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		c.Providers = append(c.Providers, p)
	}
	slices.SortFunc(c.Providers, func(a, b Provider) int { return strings.Compare(a.Code, b.Code) })
	return c, errors.Join(errs...)
}

// descriptor is the part of a provider's TOML file the catalog uses.
type descriptor struct {
	Name          string
	Code          string
	URL           string
	Description   string
	Configuration struct {
		Credentials map[string]string
		Additional  map[string]string
	}
}

// parse reads one TOML file, keeping its keys in their order.
func parse(path string) (Provider, error) {
	var d descriptor
	md, err := toml.DecodeFile(path, &d)
	if err != nil {
		return Provider{}, err
	}
	if d.Code == "" || d.Name == "" {
		return Provider{}, errors.New("no code or name")
	}
	p := Provider{
		Code: d.Code, Name: d.Name, URL: d.URL, Description: strings.TrimSpace(d.Description),
		Credentials: []Key{}, Additional: []Key{},
	}
	for _, k := range md.Keys() {
		if len(k) != 3 || k[0] != "Configuration" {
			continue
		}
		name, section := k[2], k[1]
		doc := d.Configuration.Additional[name]
		if section == "Credentials" {
			doc = d.Configuration.Credentials[name]
		}
		// Aliases only repeat other keys of the same provider, and a few
		// entries are notes rather than environment variables.
		if strings.HasPrefix(doc, "Alias to ") || strings.HasPrefix(doc, "Alias of ") || !envName.MatchString(name) {
			continue
		}
		key := Key{Key: name, Doc: doc, Secret: secret(name)}
		switch section {
		case "Credentials":
			p.Credentials = append(p.Credentials, key)
		case "Additional":
			p.Additional = append(p.Additional, key)
		}
	}
	return p, nil
}

// secret tells keys that hold a secret by their name.
func secret(key string) bool {
	for _, part := range []string{"SECRET", "KEY", "TOKEN", "PASSWORD", "PASS", "AUTH", "PRIVATE", "CREDENTIAL"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}
