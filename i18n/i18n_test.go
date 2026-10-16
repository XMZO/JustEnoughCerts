package i18n

import (
	"testing"
	"testing/fstest"

	"golang.org/x/text/language"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load(fstest.MapFS{
		"en-US.json": {Data: []byte(`{
  "_meta": {"name": "English", "dir": "ltr", "completeness": 100},
  "certs": {"list": {
    "count": "{n, plural, one {# certificate} other {# certificates}}",
    "title": "Certificates"
  }}
}`)},
		"zh-CN.json": {Data: []byte(`{
  "_meta": {"name": "Chinese (test)", "dir": "ltr", "completeness": 50},
  "certs": {"list": {"count": "{n, plural, other {# zs}}"}}
}`)},
		"fr.json": {Data: []byte(`{"_meta": {"name": "Francais", "dir": "ltr", "completeness": 0}}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCatalogFormat(t *testing.T) {
	c := testCatalog(t)
	tests := []struct {
		tag  language.Tag
		key  Key
		n    int
		want string
	}{
		{zh, "certs.list.count", 1234, "1,234 zs"},
		{zh, "certs.list.title", 1, "Certificates"}, // falls back to the base locale
		{en, "certs.list.count", 1, "1 certificate"},
		{language.German, "certs.list.title", 1, "Certificates"},          // not available: base locale
		{language.MustParse("zh-Hans-CN"), "certs.list.count", 1, "1 zs"}, // matched to zh-CN
	}
	for _, tt := range tests {
		got, err := c.Format(tt.tag, tt.key, Args{"n": tt.n})
		if err != nil || got != tt.want {
			t.Errorf("Format(%s, %s) = %q, %v; want %q", tt.tag, tt.key, got, err, tt.want)
		}
	}
	if got, err := c.Format(en, "certs.list.missing", nil); err == nil || got != "certs.list.missing" {
		t.Errorf("unknown key: %q, %v", got, err)
	}
}

func TestCatalogMatch(t *testing.T) {
	c := testCatalog(t)
	if got := c.Tags(); len(got) != 3 || got[0] != BaseLocale || got[1] != fr || got[2] != zh {
		t.Errorf("Tags() = %v", got)
	}
	tests := map[string]language.Tag{
		"zh-CN,zh;q=0.9,en;q=0.8": zh,
		"zh":                      zh,
		"fr-CA":                   fr,
		"de-DE,de;q=0.9":          BaseLocale,
		"en-GB":                   BaseLocale,
		"":                        BaseLocale,
		"not a header;;":          BaseLocale,
	}
	for header, want := range tests {
		if got := c.MatchAcceptLanguage(header); got != want {
			t.Errorf("MatchAcceptLanguage(%q) = %s, want %s", header, got, want)
		}
	}
	if m, ok := c.Meta(zh); !ok || m.Name != "Chinese (test)" {
		t.Errorf("Meta(zh) = %+v, %v", m, ok)
	}
	if _, ok := c.Meta(language.German); ok {
		t.Error("Meta of a missing locale")
	}
}

func TestLoadErrors(t *testing.T) {
	meta := `"_meta": {"name": "x", "dir": "ltr", "completeness": 100}`
	if _, err := Load(fstest.MapFS{"zh-CN.json": {Data: []byte(`{` + meta + `}`)}}); err == nil {
		t.Error("a catalog without the base locale loaded")
	}
	bad := fstest.MapFS{"en-US.json": {Data: []byte(`{` + meta + `, "a": {"b": {"c": "{broken"}}}`)}}
	if _, err := Load(bad); err == nil {
		t.Error("a catalog with a broken message loaded")
	}
}

// The embedded locale files must load; the i18n gates check the rest.
func TestDefault(t *testing.T) {
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []language.Tag{BaseLocale, zh} {
		if m, ok := c.Meta(tag); !ok || m.Name == "" {
			t.Errorf("Meta(%s) = %+v, %v", tag, m, ok)
		}
	}
}
