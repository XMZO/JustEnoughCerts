package i18n

import (
	"math"
	"slices"
	"testing"

	"golang.org/x/text/language"
)

var (
	en = language.AmericanEnglish
	zh = language.MustParse("zh-CN")
	fr = language.French
	ru = language.Russian
	ar = language.Arabic
)

func TestFormat(t *testing.T) {
	days := "{n, plural, =0 {no days} one {# day} other {# days}}"
	tests := []struct {
		tag  language.Tag
		src  string
		args Args
		want string
	}{
		{en, "plain text", nil, "plain text"},
		{en, "Renew {domain} now", Args{"domain": "example.com"}, "Renew example.com now"},
		{en, "{a}{b} {a}", Args{"a": "x", "b": 2}, "x2 x"},
		{en, "{ spaced }", Args{"spaced": "ok"}, "ok"},
		{en, "a {n} b", Args{"n": 12345}, "a 12345 b"}, // a plain argument is not grouped

		// Apostrophes, as ICU DOUBLE_OPTIONAL and FormatJS treat them.
		{en, "don't", nil, "don't"},
		{en, "It''s", nil, "It's"},
		{en, "'{literal}'", nil, "{literal}"},
		{en, "'{a''b}' c", nil, "{a'b} c"},
		{en, "use '<b>' tags", nil, "use <b> tags"},
		{en, "'}' closes", nil, "} closes"},
		{en, "unterminated '{quote", nil, "unterminated {quote"},
		{en, "# and '# outside plural", nil, "# and '# outside plural"},
		{en, "{n, plural, other {'#' is #}}", Args{"n": 5}, "# is 5"},
		{en, "trailing '", nil, "trailing '"},

		// Plurals.
		{en, days, Args{"n": 0}, "no days"},
		{en, days, Args{"n": 1}, "1 day"},
		{en, days, Args{"n": 2}, "2 days"},
		{en, days, Args{"n": 1234567}, "1,234,567 days"},
		{en, days, Args{"n": int64(-1)}, "-1 day"},
		{en, days, Args{"n": uint8(1)}, "1 day"},
		{en, "{n, plural, one {one} other {other}}", Args{"n": int64(math.MinInt64)}, "other"},
		{zh, "{n, plural, other {# items}}", Args{"n": 1}, "1 items"},
		{zh, "{n, plural, other {# items}}", Args{"n": 1234}, "1,234 items"},
		{fr, "{n, plural, one {# jour} many {# jours} other {# jours}}", Args{"n": 0}, "0 jour"},
		{ru, "{n, plural, one {one} few {few} many {many} other {other}}", Args{"n": 21}, "one"},
		{ru, "{n, plural, one {one} few {few} many {many} other {other}}", Args{"n": 22}, "few"},
		{ru, "{n, plural, one {one} few {few} many {many} other {other}}", Args{"n": 25}, "many"},
		{en, "{n, plural, one {# day} other {# days}}", Args{"n": int64(20_000_001)}, "20,000,001 days"},

		// Selects, and nesting.
		{en, "{kind, select, dns {DNS} http {HTTP} other {check}}", Args{"kind": "dns"}, "DNS"},
		{en, "{kind, select, dns {DNS} http {HTTP} other {check}}", Args{"kind": "email"}, "check"},
		{en, "{kind, select, aliyun-esa {ESA} other {x}}", Args{"kind": "aliyun-esa"}, "ESA"},
		{
			en,
			"{n, plural, one {{kind, select, dns {one DNS record #} other {one check}}} other {# checks}}",
			Args{"n": 1, "kind": "dns"},
			"one DNS record #", // '#' belongs to the innermost plural only
		},
		{
			en,
			"{kind, select, dns {{n, plural, one {# record} other {# records}}} other {-}}",
			Args{"n": 3, "kind": "dns"},
			"3 records",
		},
	}
	for _, tt := range tests {
		m, err := ParseMessage(tt.src)
		if err != nil {
			t.Errorf("ParseMessage(%q): %v", tt.src, err)
			continue
		}
		got, err := m.Format(tt.tag, tt.args)
		if err != nil || got != tt.want {
			t.Errorf("Format(%s, %q, %v) = %q, %v; want %q", tt.tag, tt.src, tt.args, got, err, tt.want)
		}
	}
}

func TestFormatErrors(t *testing.T) {
	tests := []struct {
		src  string
		args Args
		want string
	}{
		{"Hi {name}", nil, "Hi {name}"},
		{"{n, plural, other {# x}}", Args{"n": "three"}, "{n}"},
		{"{n, plural, other {# x}}", Args{"n": 1.5}, "{n}"},
		{"{k, select, other {x}}", nil, "{k}"},
	}
	for _, tt := range tests {
		m, err := ParseMessage(tt.src)
		if err != nil {
			t.Fatal(err)
		}
		got, err := m.Format(en, tt.args)
		if err == nil || got != tt.want {
			t.Errorf("Format(%q, %v) = %q, %v; want %q and an error", tt.src, tt.args, got, err, tt.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{
		"{name",
		"{}",
		"{1}",
		"{name, number}",
		"{when, date, short}",
		"{n, plural other {x}}",
		"{n, plural, one {x}}",
		"{n, plural, offset:1 other {x}}",
		"{n, plural, few {x} other {y} few {z}}",
		"{n, plural, =x {a} other {b}}",
		"{n, plural, =1 {a} =1 {b} other {c}}",
		"{n, plural, other x}",
		"{n, plural, other {x}",
		"{k, select, a {x}}",
		"{k, select, other {x} other {y}}",
		"{k, select, {x} other {y}}",
		"stray } brace",
		"{a, select, other {{b, select, other {{c, select, other {{d, select, other {{e, select, other {x}}}}}}}}}}",
	} {
		if m, err := ParseMessage(src); err == nil {
			t.Errorf("ParseMessage(%q) = %v, want an error", src, m)
		}
	}
	// Four levels are fine.
	if _, err := ParseMessage("{a, select, other {{b, select, other {{c, select, other {{d, select, other {x}}}}}}}}"); err != nil {
		t.Errorf("four nested selects: %v", err)
	}
}

func TestArguments(t *testing.T) {
	m, err := ParseMessage("{who} has {n, plural, one {# {thing}} other {# {thing}s}} {k, select, a {A} b {{k2, select, x {X} other {}}} other {O}}")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]ArgKinds{"who": ArgText, "thing": ArgText, "n": ArgPlural, "k": ArgSelect, "k2": ArgSelect}
	got := m.Arguments()
	if len(got) != len(want) {
		t.Fatalf("Arguments() = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Arguments()[%s] = %v, want %v", k, got[k], v)
		}
	}
	branches := m.SelectBranches()
	if !slices.Equal(branches["k"], []string{"a", "b", "other"}) || !slices.Equal(branches["k2"], []string{"other", "x"}) {
		t.Errorf("SelectBranches() = %v", branches)
	}
	mixed, _ := ParseMessage("{n} {n, plural, other {#}}")
	if mixed.Arguments()["n"] != ArgText|ArgPlural {
		t.Errorf("an argument used two ways: %v", mixed.Arguments())
	}
}

func TestPluralCategories(t *testing.T) {
	tests := map[language.Tag][]string{
		en: {"one", "other"},
		zh: {"other"},
		fr: {"one", "other"}, // see PluralCategories on "many"
		ru: {"one", "few", "many", "other"},
		ar: {"zero", "one", "two", "few", "many", "other"},
	}
	for tag, want := range tests {
		if got := PluralCategories(tag); !slices.Equal(got, want) {
			t.Errorf("PluralCategories(%s) = %v, want %v", tag, got, want)
		}
	}
}

func TestCheckPlurals(t *testing.T) {
	tests := []struct {
		tag   language.Tag
		src   string
		valid bool
	}{
		{en, "{n, plural, one {#} other {#}}", true},
		{en, "{n, plural, =0 {none} one {#} other {#}}", true},
		{en, "{n, plural, other {#}}", false},         // "1 days"
		{zh, "{n, plural, other {#}}", true},          // Chinese has no "one"
		{zh, "{n, plural, one {#} other {#}}", false}, // unreachable branch
		{en, "{k, select, a {{n, plural, other {#}}} other {}}", false},
		{en, "no plurals at all", true},
	}
	for _, tt := range tests {
		m, err := ParseMessage(tt.src)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.CheckPlurals(tt.tag); (err == nil) != tt.valid {
			t.Errorf("CheckPlurals(%s, %q) = %v, want valid=%v", tt.tag, tt.src, err, tt.valid)
		}
	}
}

func TestMessageString(t *testing.T) {
	const src = "Hello {name}"
	m, err := ParseMessage(src)
	if err != nil || m.String() != src {
		t.Errorf("String() = %q, %v", m, err)
	}
}
