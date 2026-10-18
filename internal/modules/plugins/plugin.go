package plugins

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// What a plugin may describe.
const (
	maxTypes  = 32
	maxFields = 64
)

var (
	// idPattern is what the ID of a plugin, the name of its file without
	// the extension, and the IDs of its types are made of.
	idPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	// fieldKinds are the kinds of setting a plugin's form may have.
	fieldKinds = []providers.FieldKind{providers.Text, providers.Multiline, providers.Number, providers.URL, providers.Email, providers.Select, providers.Switch}
)

// loaded is a file of the plugins directory, as JEC found it: the plugin
// with the types it registered, or why it is not in use.
type loaded struct {
	file   string
	plugin *plugin
	name   text
	desc   text
	types  []providers.Type
	specs  map[string]typeSpec // by the plugin's own type ID
	// added are the IDs of the types the registry took.
	added []string
	err   *contracts.Message
}

// fail records why the plugin is not in use.
func (l *loaded) fail(key i18n.Key, params map[string]any) {
	l.err = &contracts.Message{Key: key, Params: params}
}

// scan lists the executables of dir, in the order of their names. Other
// files are left alone, as are those whose name starts with a dot.
func scan(dir string) ([]*loaded, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	dirProblem := unsafe(info)
	var out []*loaded
	seen := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || !executable(info) {
			continue
		}
		id := strings.TrimSuffix(name, filepath.Ext(name))
		l := &loaded{file: name, plugin: &plugin{id: id, path: filepath.Join(dir, name), data: filepath.Join(dir, id+".data")}}
		switch problem := cmp.Or(dirProblem, unsafe(info)); {
		case !idPattern.MatchString(id):
			l.fail(i18n.PluginsErrorName, map[string]any{"file": name})
		case seen[id] != "":
			l.fail(i18n.PluginsErrorRepeated, map[string]any{"file": name, "other": seen[id]})
		case problem == "writable":
			l.fail(i18n.PluginsErrorWritable, map[string]any{"file": name})
		case problem == "owner":
			l.fail(i18n.PluginsErrorOwner, map[string]any{"file": name, "uid": os.Geteuid()})
		}
		if seen[id] == "" {
			seen[id] = name
		}
		out = append(out, l)
	}
	return out, nil
}

// describe asks the plugin for its types and makes the provider types of
// them, with their texts in the languages JEC speaks. It returns what the
// plugin wrote on its standard error.
func (l *loaded) describe(ctx context.Context, tags []language.Tag) []byte {
	var d description
	stderr, err := l.plugin.call(ctx, describeTimeout, request{Action: actionDescribe}, &d)
	if err != nil {
		var p *contracts.Problem
		if errors.As(err, &p) {
			l.fail(p.Key, p.Params)
		} else {
			l.fail(i18n.PluginsErrorRun, map[string]any{"plugin": l.plugin.id})
		}
		return stderr
	}
	if !d.OK {
		l.fail(i18n.PluginsErrorFailed, map[string]any{"plugin": l.plugin.id, "message": clip(d.Error)})
		return stderr
	}
	types, specs, err := build(l.plugin.id, d, tags)
	if err != nil {
		l.fail(i18n.PluginsErrorDescribe, map[string]any{"plugin": l.plugin.id, "problem": err.Error()})
		return stderr
	}
	l.name, l.desc, l.types, l.specs = d.Name, d.Description, types, specs
	return stderr
}

// build makes the provider types of a plugin's description.
func build(id string, d description, tags []language.Tag) ([]providers.Type, map[string]typeSpec, error) {
	switch {
	case len(d.Types) == 0:
		return nil, nil, errors.New("it offers no types")
	case len(d.Types) > maxTypes:
		return nil, nil, fmt.Errorf("it offers %d types, more than %d", len(d.Types), maxTypes)
	}
	var out []providers.Type
	specs := map[string]typeSpec{}
	for i, spec := range d.Types {
		t, err := buildType(id, spec, tags)
		if err != nil {
			return nil, nil, fmt.Errorf("type %d (%q): %w", i+1, spec.ID, err)
		}
		if _, ok := specs[spec.ID]; ok {
			return nil, nil, fmt.Errorf("type %d: %q comes twice", i+1, spec.ID)
		}
		specs[spec.ID] = spec
		out = append(out, t)
	}
	return out, specs, nil
}

// buildType makes the provider type of spec: its ID is plugins.<plugin>.
// <type>, and its texts are messages of the plugin's own.
func buildType(plugin string, spec typeSpec, tags []language.Tag) (providers.Type, error) {
	var none providers.Type
	if !idPattern.MatchString(spec.ID) {
		return none, errors.New("an ID is lowercase letters, digits, - and _, starting with a letter, 32 at most")
	}
	kind := providers.Kind(spec.Kind)
	if !slices.Contains([]providers.Kind{providers.Target, providers.Notifier, providers.Solver}, kind) {
		return none, fmt.Errorf("the kind %q is not target, notifier or solver", spec.Kind)
	}
	if spec.Name.empty() {
		return none, errors.New("it has no name")
	}
	if len(spec.Fields) > maxFields {
		return none, fmt.Errorf("it has %d fields, more than %d", len(spec.Fields), maxFields)
	}
	w := words{tags: tags, texts: map[string]map[i18n.Key]string{}}
	t := providers.Type{
		ID: "plugins." + plugin + "." + spec.ID, Kind: kind, Tags: []string{"plugin"}, Testable: spec.Testable,
		Name: w.add(spec.Name, plugin, spec.ID, "name"),
	}
	if !spec.Description.empty() {
		t.Description = w.add(spec.Description, plugin, spec.ID, "description")
	}
	switch {
	case kind == providers.Solver && spec.Challenge == "":
		t.Challenge = providers.ChallengeDNS
	case kind == providers.Solver && (spec.Challenge == providers.ChallengeDNS || spec.Challenge == providers.ChallengeHTTP):
		t.Challenge = spec.Challenge
	case spec.Challenge != "":
		return none, fmt.Errorf("the challenge %q is for solvers, dns or http", spec.Challenge)
	}
	for _, f := range spec.Fields {
		if !slices.Contains(fieldKinds, providers.FieldKind(f.Kind)) {
			return none, fmt.Errorf("field %q: the kind %q is not one of %v", f.Key, f.Kind, fieldKinds)
		}
		field := providers.Field{
			Key: f.Key, Kind: providers.FieldKind(f.Kind), Required: f.Required, Secret: f.Secret,
			Advanced: f.Advanced, Default: f.Default, When: f.When,
		}
		if !f.Label.empty() {
			field.Label = w.add(f.Label, plugin, spec.ID, "fields", f.Key, "label")
		}
		if !f.Help.empty() {
			field.Help = w.add(f.Help, plugin, spec.ID, "fields", f.Key, "help")
		}
		for i, o := range f.Options {
			label := o.Label
			if label.empty() {
				label = text{"": o.Value}
			}
			field.Options = append(field.Options, providers.Option{
				Value: o.Value, Label: w.add(label, plugin, spec.ID, "fields", f.Key, "options", strconv.Itoa(i)),
			})
		}
		t.Fields = append(t.Fields, field)
	}
	if kind == providers.Notifier {
		// The notify module picks the events and the language.
		t.Fields = append(t.Fields, contracts.NotifierFields()...)
	}
	t.Texts = w.texts
	return t, nil
}

// words gathers the texts of a type by language, as messages.
type words struct {
	tags  []language.Tag
	texts map[string]map[i18n.Key]string
}

// add makes the message of t at path, in each language, and returns its
// key.
func (w *words) add(t text, plugin string, path ...string) i18n.Key {
	key := i18n.PluginText(plugin, path...)
	for _, tag := range w.tags {
		s := clip(t.in(tag))
		if s == "" {
			continue
		}
		if w.texts[tag.String()] == nil {
			w.texts[tag.String()] = map[i18n.Key]string{}
		}
		w.texts[tag.String()][key] = literal(s)
	}
	return key
}

// literal is s as a message of the ICU MessageFormat subset of the locale
// files, which shows s as it is: apostrophes are doubled, and braces are
// quoted.
func literal(s string) string {
	var b strings.Builder
	quoted := false
	for _, r := range s {
		switch r {
		case '\'':
			b.WriteString("''")
		case '{', '}':
			if !quoted {
				b.WriteByte('\'')
				quoted = true
			}
			b.WriteRune(r)
		default:
			if quoted {
				b.WriteByte('\'')
				quoted = false
			}
			b.WriteRune(r)
		}
	}
	if quoted {
		b.WriteByte('\'')
	}
	return b.String()
}

// wait is how long a DNS record of a solver of spec may take to be seen,
// and how often to look.
func wait(spec typeSpec) (time.Duration, time.Duration) {
	timeout, interval := 2*time.Minute, 5*time.Second
	if w := spec.Wait; w != nil {
		if w.Timeout > 0 {
			timeout = time.Duration(min(w.Timeout, 3600)) * time.Second
		}
		if w.Interval > 0 {
			interval = time.Duration(min(w.Interval, 300)) * time.Second
		}
	}
	return timeout, interval
}
