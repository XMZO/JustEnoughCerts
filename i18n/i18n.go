// Package i18n holds every user-facing text of JEC and renders it in the
// viewer's language.
//
// The texts live in locales/, one JSON file per language; the backend
// embeds those files and the web UI imports the same files. Code refers to
// texts only through the Key constants generated from the base locale
// (keys_gen.go), never through string literals: "go run ./tools/i18nlint"
// rejects those, and "go run ./tools/i18n check" runs the other gates of
// PLAN.md 7.4.
package i18n

//go:generate go run ../tools/i18n -root .. gen

import (
	"cmp"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sync"

	"golang.org/x/text/language"
)

// Key identifies a message, e.g. "certs.list.empty".
type Key string

// Args are the named arguments of a message.
type Args map[string]any

// BaseLocale defines the set of keys and is the fallback for messages a
// locale does not translate.
var BaseLocale = language.AmericanEnglish

//go:embed locales/*.json
var embedded embed.FS

var defaultCatalog = sync.OnceValues(func() (*Catalog, error) {
	locales, err := fs.Sub(embedded, "locales")
	if err != nil {
		return nil, err
	}
	return Load(locales)
})

// Default returns the catalog of the locale files built into the binary.
func Default() (*Catalog, error) { return defaultCatalog() }

// Catalog holds the parsed messages of every locale.
type Catalog struct {
	base    *locale
	locales map[language.Tag]*locale
	tags    []language.Tag // the base locale first, then by tag
	matcher language.Matcher
}

type locale struct {
	meta     Meta
	messages map[Key]*Message
}

// Load parses the locale files at the root of fsys.
func Load(fsys fs.FS) (*Catalog, error) {
	files, err := ReadDir(fsys)
	if err != nil {
		return nil, err
	}
	c := &Catalog{locales: make(map[language.Tag]*locale, len(files))}
	var errs []error
	for _, f := range files {
		loc := &locale{meta: f.Meta, messages: make(map[Key]*Message, len(f.Messages))}
		for key, src := range f.Messages {
			m, err := ParseMessage(src)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %s: %w", f.Tag, key, err))
				continue
			}
			loc.messages[key] = m
		}
		c.locales[f.Tag] = loc
		c.tags = append(c.tags, f.Tag)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	var ok bool
	if c.base, ok = c.locales[BaseLocale]; !ok {
		return nil, fmt.Errorf("i18n: the base locale %s is missing", BaseLocale)
	}
	slices.SortFunc(c.tags, func(a, b language.Tag) int {
		switch {
		case a == BaseLocale:
			return -1
		case b == BaseLocale:
			return 1
		}
		return cmp.Compare(a.String(), b.String())
	})
	c.matcher = language.NewMatcher(c.tags)
	return c, nil
}

// Tags returns the available locales, the base locale first.
func (c *Catalog) Tags() []language.Tag { return slices.Clone(c.tags) }

// Meta returns the metadata of an available locale.
func (c *Catalog) Meta(tag language.Tag) (Meta, bool) {
	loc, ok := c.locales[tag]
	if !ok {
		return Meta{}, false
	}
	return loc.meta, true
}

// Match returns the available locale that best fits prefs, in order of
// preference, or the base locale if none does.
func (c *Catalog) Match(prefs ...language.Tag) language.Tag {
	_, i, confidence := c.matcher.Match(prefs...)
	if confidence == language.No {
		return BaseLocale
	}
	return c.tags[i]
}

// MatchAcceptLanguage is Match for an HTTP Accept-Language header.
func (c *Catalog) MatchAcceptLanguage(header string) language.Tag {
	prefs, _, err := language.ParseAcceptLanguage(header)
	if err != nil || len(prefs) == 0 {
		return BaseLocale
	}
	return c.Match(prefs...)
}

// Format renders the message key in the locale tag, falling back to the
// base locale's text when tag does not translate it. Like Message.Format it
// renders as much as it can and reports what went wrong.
func (c *Catalog) Format(tag language.Tag, key Key, args Args) (string, error) {
	loc, ok := c.locales[tag]
	if !ok {
		tag = c.Match(tag)
		loc = c.locales[tag]
	}
	if m, ok := loc.messages[key]; ok {
		return m.Format(tag, args)
	}
	if m, ok := c.base.messages[key]; ok {
		return m.Format(BaseLocale, args)
	}
	return string(key), fmt.Errorf("i18n: no message %s", key)
}
