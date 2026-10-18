package i18n

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
)

// Messages use the subset of ICU MessageFormat that the web UI's formatter
// (FormatJS) treats the same way:
//
//	text     Renew {domain} now
//	plural   {count, plural, =0 {no certificates} one {# certificate} other {# certificates}}
//	select   {kind, select, dns {DNS record} http {file} other {check}}
//
// In a plural branch '#' is the number, formatted for the locale; a plain
// {count} is printed as is. An apostrophe quotes the following '{', '}',
// '<', '>' (or '#' in a plural branch) up to the next apostrophe, and two
// apostrophes stand for one; any other apostrophe is literal, so "don't"
// needs no escaping. Other argument types (number, date, ...), offsets and
// selectordinal are not supported: format such values before passing them.

// maxDepth bounds how deeply plural and select arguments may nest.
const maxDepth = 4

// Message is a parsed message.
type Message struct {
	src   string
	parts []part
}

type part any

type (
	textPart  string
	argPart   string // argument name
	poundPart struct{}
)

type pluralPart struct {
	arg   string
	exact map[int64][]part
	forms map[string][]part // by plural category
}

type selectPart struct {
	arg      string
	branches map[string][]part
}

// ParseMessage parses src.
func ParseMessage(src string) (*Message, error) {
	p := &parser{src: []rune(src)}
	parts, err := p.message(0, false)
	if err != nil {
		return nil, err
	}
	if !p.eof() {
		return nil, p.errorf("unmatched '}'")
	}
	return &Message{src: src, parts: parts}, nil
}

// String returns the message source.
func (m *Message) String() string { return m.src }

type parser struct {
	src []rune
	pos int
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) peek(offset int) rune {
	if i := p.pos + offset; i < len(p.src) {
		return p.src[i]
	}
	return 0
}

func (p *parser) errorf(format string, a ...any) error {
	return fmt.Errorf("at character %d: %s", p.pos+1, fmt.Sprintf(format, a...))
}

func (p *parser) consume(r rune) bool {
	if p.peek(0) == r && !p.eof() {
		p.pos++
		return true
	}
	return false
}

func (p *parser) space() {
	for !p.eof() && strings.ContainsRune(" \t\r\n", p.src[p.pos]) {
		p.pos++
	}
}

// word reads a run of ASCII letters, digits, '_' and, if dash, '-'.
func (p *parser) word(dash bool) string {
	start := p.pos
	for !p.eof() && isWordChar(p.src[p.pos], dash) {
		p.pos++
	}
	return string(p.src[start:p.pos])
}

func isWordChar(c rune, dash bool) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || dash && c == '-'
}

// message parses text and arguments up to the end of the input or a '}'
// that closes an enclosing branch, which it leaves for the caller.
func (p *parser) message(depth int, inPlural bool) ([]part, error) {
	var parts []part
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, textPart(lit.String()))
			lit.Reset()
		}
	}
	for !p.eof() {
		switch c := p.src[p.pos]; {
		case c == '}':
			flush()
			return parts, nil
		case c == '{':
			flush()
			a, err := p.argument(depth)
			if err != nil {
				return nil, err
			}
			parts = append(parts, a)
		case c == '#' && inPlural:
			flush()
			parts = append(parts, poundPart{})
			p.pos++
		case c == '\'':
			p.quote(&lit, inPlural)
		default:
			lit.WriteRune(c)
			p.pos++
		}
	}
	flush()
	return parts, nil
}

// quote handles an apostrophe the way ICU's DOUBLE_OPTIONAL mode and
// FormatJS do.
func (p *parser) quote(lit *strings.Builder, inPlural bool) {
	switch next := p.peek(1); {
	case next == '\'':
		lit.WriteRune('\'')
		p.pos += 2
		return
	case next == '{' || next == '}' || next == '<' || next == '>' || (next == '#' && inPlural):
	default:
		lit.WriteRune('\'')
		p.pos++
		return
	}
	p.pos++ // the opening apostrophe
	lit.WriteRune(p.src[p.pos])
	p.pos++
	for !p.eof() {
		c := p.src[p.pos]
		if c == '\'' {
			if p.peek(1) == '\'' {
				lit.WriteRune('\'')
				p.pos += 2
				continue
			}
			p.pos++ // the closing apostrophe
			return
		}
		lit.WriteRune(c)
		p.pos++
	}
}

func (p *parser) argument(depth int) (part, error) {
	p.pos++ // '{'
	p.space()
	name := p.word(false)
	if name == "" || name[0] >= '0' && name[0] <= '9' {
		return nil, p.errorf("argument name expected")
	}
	p.space()
	if p.consume('}') {
		return argPart(name), nil
	}
	if !p.consume(',') {
		return nil, p.errorf("',' or '}' expected after argument %s", name)
	}
	p.space()
	kind := p.word(false)
	if kind != "plural" && kind != "select" {
		return nil, p.errorf("argument type %q of %s is not supported: use plural or select, and format other values before passing them", kind, name)
	}
	p.space()
	if !p.consume(',') {
		return nil, p.errorf("',' expected after %s, %s", name, kind)
	}
	if depth >= maxDepth {
		return nil, p.errorf("arguments nested deeper than %d", maxDepth)
	}
	if kind == "plural" {
		return p.plural(name, depth+1)
	}
	return p.selectArg(name, depth+1)
}

func (p *parser) plural(name string, depth int) (part, error) {
	pl := pluralPart{arg: name, exact: map[int64][]part{}, forms: map[string][]part{}}
	for {
		p.space()
		if p.eof() {
			return nil, p.errorf("plural %s is not closed", name)
		}
		if p.consume('}') {
			break
		}
		if p.consume('=') {
			digits := p.word(false)
			n, err := strconv.ParseInt(digits, 10, 64)
			if err != nil {
				return nil, p.errorf("plural %s: bad exact value =%s", name, digits)
			}
			if _, dup := pl.exact[n]; dup {
				return nil, p.errorf("plural %s: =%d given twice", name, n)
			}
			if pl.exact[n], err = p.branch(depth, true); err != nil {
				return nil, err
			}
			continue
		}
		category := p.word(false)
		if !slices.Contains(categoryOrder, category) {
			return nil, p.errorf("plural %s: %q is not a plural category (offsets are not supported)", name, category)
		}
		if _, dup := pl.forms[category]; dup {
			return nil, p.errorf("plural %s: %s given twice", name, category)
		}
		body, err := p.branch(depth, true)
		if err != nil {
			return nil, err
		}
		pl.forms[category] = body
	}
	if _, ok := pl.forms["other"]; !ok {
		return nil, p.errorf("plural %s has no other branch", name)
	}
	return pl, nil
}

func (p *parser) selectArg(name string, depth int) (part, error) {
	sel := selectPart{arg: name, branches: map[string][]part{}}
	for {
		p.space()
		if p.eof() {
			return nil, p.errorf("select %s is not closed", name)
		}
		if p.consume('}') {
			break
		}
		key := p.word(true)
		if key == "" {
			return nil, p.errorf("select %s: branch name expected", name)
		}
		if _, dup := sel.branches[key]; dup {
			return nil, p.errorf("select %s: %s given twice", name, key)
		}
		body, err := p.branch(depth, false)
		if err != nil {
			return nil, err
		}
		sel.branches[key] = body
	}
	if _, ok := sel.branches["other"]; !ok {
		return nil, p.errorf("select %s has no other branch", name)
	}
	return sel, nil
}

func (p *parser) branch(depth int, inPlural bool) ([]part, error) {
	p.space()
	if !p.consume('{') {
		return nil, p.errorf("'{' expected")
	}
	body, err := p.message(depth, inPlural)
	if err != nil {
		return nil, err
	}
	if !p.consume('}') {
		return nil, p.errorf("branch is not closed")
	}
	return body, nil
}

// Format renders m for the locale tag. It renders as much as it can: an
// argument that is missing or has the wrong type comes out as {name}, and
// the error names every such argument.
func (m *Message) Format(tag language.Tag, args Args) (string, error) {
	f := formatter{tag: tag, args: args}
	var b strings.Builder
	f.parts(&b, m.parts, 0)
	return b.String(), errors.Join(f.errs...)
}

type formatter struct {
	tag  language.Tag
	args Args
	errs []error
}

func (f *formatter) fail(b *strings.Builder, name, problem string) {
	f.errs = append(f.errs, fmt.Errorf("argument %s: %s", name, problem))
	b.WriteString("{" + name + "}")
}

// parts writes parts; n is the number of the innermost plural, for '#'.
func (f *formatter) parts(b *strings.Builder, parts []part, n int64) {
	for _, p := range parts {
		switch p := p.(type) {
		case textPart:
			b.WriteString(string(p))
		case argPart:
			v, ok := f.args[string(p)]
			if !ok {
				f.fail(b, string(p), "missing")
				continue
			}
			b.WriteString(text(v))
		case poundPart:
			b.WriteString(message.NewPrinter(f.tag).Sprint(number.Decimal(n)))
		case pluralPart:
			v, ok := integer(f.args[p.arg])
			if !ok {
				f.fail(b, p.arg, "missing or not an integer")
				continue
			}
			body, ok := p.exact[v]
			if !ok {
				if body, ok = p.forms[pluralCategory(f.tag, v)]; !ok {
					body = p.forms["other"]
				}
			}
			f.parts(b, body, v)
		case selectPart:
			v, ok := f.args[p.arg]
			if !ok {
				f.fail(b, p.arg, "missing")
				continue
			}
			body, ok := p.branches[text(v)]
			if !ok {
				body = p.branches["other"]
			}
			f.parts(b, body, n)
		}
	}
}

func text(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func integer(v any) (int64, bool) {
	switch v := v.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint:
		return unsigned(uint64(v))
	case uint64:
		return unsigned(v)
	case float64:
		// A number decoded from JSON, such as the params of a stored
		// message.
		if v != math.Trunc(v) || v < math.MinInt64 || v >= math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case float32:
		return integer(float64(v))
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	}
	return 0, false
}

func unsigned(v uint64) (int64, bool) {
	if v > math.MaxInt64 {
		return 0, false
	}
	return int64(v), true
}

// Argument kinds, as a set.
const (
	ArgText ArgKinds = 1 << iota
	ArgPlural
	ArgSelect
)

// ArgKinds is the set of ways a message uses an argument.
type ArgKinds uint8

// Arguments returns every argument m uses and how.
func (m *Message) Arguments() map[string]ArgKinds {
	out := map[string]ArgKinds{}
	walk(m.parts, func(p part) {
		switch p := p.(type) {
		case argPart:
			out[string(p)] |= ArgText
		case pluralPart:
			out[p.arg] |= ArgPlural
		case selectPart:
			out[p.arg] |= ArgSelect
		}
	})
	return out
}

// SelectBranches returns, for each select argument, its sorted branch names.
func (m *Message) SelectBranches() map[string][]string {
	out := map[string][]string{}
	walk(m.parts, func(p part) {
		if s, ok := p.(selectPart); ok {
			for k := range s.branches {
				if !slices.Contains(out[s.arg], k) {
					out[s.arg] = append(out[s.arg], k)
				}
			}
			slices.Sort(out[s.arg])
		}
	})
	return out
}

// CheckPlurals reports plural arguments whose categories do not match the
// ones tag uses for whole numbers: each must give exactly those, plus any
// =N branches.
func (m *Message) CheckPlurals(tag language.Tag) error {
	want := PluralCategories(tag)
	var errs []error
	walk(m.parts, func(p part) {
		pl, ok := p.(pluralPart)
		if !ok {
			return
		}
		var got []string
		for _, c := range categoryOrder {
			if _, ok := pl.forms[c]; ok {
				got = append(got, c)
			}
		}
		if !slices.Equal(got, want) {
			errs = append(errs, fmt.Errorf("plural %s has categories %v, %s uses %v", pl.arg, got, tag, want))
		}
	})
	return errors.Join(errs...)
}

func walk(parts []part, visit func(part)) {
	for _, p := range parts {
		visit(p)
		switch p := p.(type) {
		case pluralPart:
			for _, body := range p.exact {
				walk(body, visit)
			}
			for _, body := range p.forms {
				walk(body, visit)
			}
		case selectPart:
			for _, body := range p.branches {
				walk(body, visit)
			}
		}
	}
}

// categoryOrder is the CLDR order of plural categories.
var categoryOrder = []string{"zero", "one", "two", "few", "many", "other"}

var formNames = map[plural.Form]string{
	plural.Zero:  "zero",
	plural.One:   "one",
	plural.Two:   "two",
	plural.Few:   "few",
	plural.Many:  "many",
	plural.Other: "other",
}

func pluralCategory(tag language.Tag, n int64) string {
	if n < 0 {
		n = -n
		if n < 0 { // math.MinInt64
			n = math.MaxInt64
		}
	}
	if n > 1<<31-1 {
		// Rules only look at a few trailing digits, so the operand may be
		// taken modulo 10,000,000 (see MatchPlural).
		n = 10_000_000 + n%10_000_000
	}
	return formNames[plural.Cardinal.MatchPlural(tag, int(n), 0, 0, 0, 0)]
}

// PluralCategories returns the plural categories that whole numbers fall
// into in tag, in CLDR order. It always includes "other".
//
// The CLDR data of x/text is older than the browsers'. A browser may pick a
// category this package never does, such as "many" for a million in
// French; FormatJS then falls back to "other", which is also what this
// package picks, as long as messages give only the categories listed here.
func PluralCategories(tag language.Tag) []string {
	seen := map[string]bool{"other": true}
	probe := func(n int64) { seen[pluralCategory(tag, n)] = true }
	for n := range int64(1200) {
		probe(n)
	}
	for n := int64(10_000); n <= 1_000_000_000; n *= 10 {
		probe(n)
		probe(n + 1)
	}
	var out []string
	for _, c := range categoryOrder {
		if seen[c] {
			out = append(out, c)
		}
	}
	return out
}
