// Package configfile writes JEC's configuration to a YAML file and reads
// one back (PLAN.md 11): the credentials and provider instances, the
// certificates and the settings, to take them to another JEC or to keep
// them in Git. Modules add the sections they keep
// (module.Registry.Config); the kernel keeps the credentials and the
// providers. Secrets stay out of a file unless asked for.
//
// An import adds and changes, and never deletes. It runs in one
// transaction, which a dry run rolls back, so that what a file would do
// shows before it is done; an error in any entry stops it all.
package configfile

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// Version is the format of the files JEC writes. A file of a later one is
// refused.
const Version = 1

// Section is a part of the file that a module keeps, under a key of its
// own.
type Section struct {
	// Key is the section's key in the file, such as certificates.
	Key string
	// Title names the section where an import tells what it did.
	Title i18n.Key
	// New returns a pointer to a new value that the section's part of a
	// file decodes into, such as *[]Certificate. Its fields have yaml
	// tags; a file with keys they lack is refused.
	New func() any
	// Export returns a value of New's type, filled with what JEC has.
	Export func(ctx context.Context, x Export) (any, error)
	// Import makes, in tx, the changes that v asks for: a value of New's
	// type, decoded from a file. It reports what it does with each entry
	// through in. The caller commits tx unless an error stops the import
	// or it is a dry run; after, if not nil, runs once it committed, as to
	// wake a job. Import must not write to the database but through tx.
	Import func(ctx context.Context, tx *sql.Tx, v any, in *Import) (after func(), err error)
}

// Export tells a section what to write.
type Export struct {
	// Secrets asks for secret values, which are left out otherwise.
	Secrets bool
}

// Action is what an import does with an entry.
type Action string

// Actions.
const (
	Add    Action = "add"
	Change Action = "change"
	Keep   Action = "keep" // JEC has it as the file says
	Skip   Action = "skip" // left as it is, for a problem
)

// Problem is something wrong with an entry: an error stops the import, a
// warning tells of what it leaves as it is.
type Problem struct {
	// Field is where in the entry, such as config.email or targets[1]; ""
	// for the entry as a whole.
	Field   string         `json:"field,omitempty"`
	Key     i18n.Key       `json:"key"`
	Params  map[string]any `json:"params,omitempty"`
	Warning bool           `json:"warning,omitempty" doc:"Warnings tell of what the import leaves as it is; other problems stop it"`
}

// Item is an entry of a file, and what an import does with it.
type Item struct {
	// Name is what people call the entry, such as a provider's name or a
	// certificate's domains; "" for a section that is a single entry, as
	// settings are.
	Name     string    `json:"name,omitempty"`
	Action   Action    `json:"action" enum:"add,change,keep,skip"`
	Problems []Problem `json:"problems,omitempty"`
}

// Report is what an import did, or would do, section by section.
type Report struct {
	Sections []SectionReport `json:"sections" doc:"The sections the file has, in its order"`
	Errors   int             `json:"errors" doc:"How many problems stop the import"`
	Applied  bool            `json:"applied" doc:"Whether the changes were made, or only shown"`
}

// SectionReport is what an import does with the entries of a section.
type SectionReport struct {
	Key   string   `json:"key"`
	Title i18n.Key `json:"title"`
	Items []Item   `json:"items"`
}

// Import is what a section reads its part of a file with: the providers
// the file names, and the report.
type Import struct {
	report      *Report
	section     *SectionReport
	named       map[ref][]Provider
	credentials map[string]namedCredential
}

// ref is how a file names a provider instance: by its name, among those
// of a kind.
type ref struct {
	kind providers.Kind
	name string
}

// Provider is a provider instance that a file names.
type Provider struct {
	ID   int64
	Name string
	Type providers.Type
}

// Provider returns the provider instance of kind called name, as the file
// leaves them: one it adds or changes, or one JEC has. The problem says
// why name names none: there is no such instance, or there are several.
func (in *Import) Provider(kind providers.Kind, name string) (Provider, *Problem) {
	found := in.named[ref{kind, name}]
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return Provider{}, &Problem{Key: i18n.KernelConfigErrorNoProvider, Params: map[string]any{"name": name, "kind": string(kind)}}
	}
	return Provider{}, &Problem{Key: i18n.KernelConfigErrorAmbiguous, Params: map[string]any{"name": name}}
}

// ByID returns provider instance id, as the file leaves it, when its type
// is available: so that a section reads it within the import, not around
// it.
func (in *Import) ByID(id int64) (Provider, bool) {
	for _, list := range in.named {
		for _, p := range list {
			if p.ID == id {
				return p, true
			}
		}
	}
	return Provider{}, false
}

// Report adds an entry of the section, with what the import does with it.
func (in *Import) Report(it Item) {
	for _, p := range it.Problems {
		if !p.Warning {
			in.report.Errors++
		}
	}
	in.section.Items = append(in.section.Items, it)
}

// begin starts the part of the report of a section.
func (in *Import) begin(key string, title i18n.Key) {
	in.report.Sections = append(in.report.Sections, SectionReport{Key: key, Title: title, Items: []Item{}})
	in.section = &in.report.Sections[len(in.report.Sections)-1]
}

// Registry holds the sections of the file.
type Registry struct {
	db        *sql.DB
	providers *providers.Registry
	enabled   func(module string) bool
	sections  []entry
}

// entry is a section with the module it belongs to.
type entry struct {
	Section
	module string
}

// New returns a registry of sections that writes the provider instances
// of prov; enabled tells which modules are on.
func New(db *sql.DB, prov *providers.Registry, enabled func(module string) bool) *Registry {
	return &Registry{db: db, providers: prov, enabled: enabled}
}

// Add adds a section of module. Its key is its own: not another
// section's, nor one the kernel keeps.
func (r *Registry) Add(module string, s Section) error {
	if s.Key == "" || s.Key == keyVersion || s.Key == keyCredentials || s.Key == keyProviders || slices.ContainsFunc(r.sections, func(e entry) bool { return e.Key == s.Key }) {
		return fmt.Errorf("configfile: module %s: section key %q is taken", module, s.Key)
	}
	if s.New == nil || s.Export == nil || s.Import == nil {
		return fmt.Errorf("configfile: module %s: section %s lacks New, Export or Import", module, s.Key)
	}
	if t := reflect.TypeOf(s.New()); t == nil || t.Kind() != reflect.Pointer {
		return fmt.Errorf("configfile: module %s: section %s: New returns %v, not a pointer", module, s.Key, t)
	}
	r.sections = append(r.sections, entry{Section: s, module: module})
	return nil
}

// The keys the kernel keeps.
const (
	keyVersion     = "version"
	keyCredentials = "credentials"
	keyProviders   = "providers"
)

// The fields of a file before those of the sections.
const kernelFields = 3

// fileType is what a file decodes into and is written from: the version,
// the credentials, the providers, and a field for each section. A
// section's field is a pointer, nil where the file lacks the section.
func (r *Registry) fileType() reflect.Type {
	fields := []reflect.StructField{
		{Name: "Version", Type: reflect.TypeFor[int](), Tag: `yaml:"version"`},
		{Name: "Credentials", Type: reflect.TypeFor[*[]credentialEntry](), Tag: `yaml:"credentials,omitempty"`},
		{Name: "Providers", Type: reflect.TypeFor[*[]providerEntry](), Tag: `yaml:"providers,omitempty"`},
	}
	for i, s := range r.sections {
		fields = append(fields, reflect.StructField{
			Name: "S" + strconv.Itoa(i), Type: reflect.TypeOf(s.New()), Tag: reflect.StructTag(`yaml:"` + s.Key + `,omitempty"`),
		})
	}
	return reflect.StructOf(fields)
}

// Export writes the configuration as YAML: the credentials and the
// providers, then the section of each module that is on.
func (r *Registry) Export(ctx context.Context, x Export) ([]byte, error) {
	file := reflect.New(r.fileType()).Elem()
	file.Field(0).SetInt(Version)
	creds, names, err := r.exportCredentials(ctx, x)
	if err != nil {
		return nil, err
	}
	if len(creds) > 0 {
		file.Field(1).Set(reflect.ValueOf(&creds))
	}
	list, err := r.exportProviders(ctx, x, names)
	if err != nil {
		return nil, err
	}
	file.Field(2).Set(reflect.ValueOf(&list))
	for i, s := range r.sections {
		if !r.enabled(s.module) {
			continue
		}
		v, err := s.Export(ctx, x)
		if err != nil {
			return nil, fmt.Errorf("configfile: export %s: %w", s.Key, err)
		}
		f := file.Field(kernelFields + i)
		if reflect.TypeOf(v) != f.Type() {
			return nil, fmt.Errorf("configfile: export %s: %T, not %s", s.Key, v, f.Type())
		}
		f.Set(reflect.ValueOf(v))
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(file.Interface()); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// checkKeys refuses a file that is empty, or has a section JEC lacks,
// with a message clearer than the decoder's, which spells out the type of
// the whole file.
func (r *Registry) checkKeys(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return &FileError{Key: i18n.KernelConfigErrorUnreadable, Params: map[string]any{"error": err.Error()}}
	}
	if len(doc.Content) == 0 {
		return &FileError{Key: i18n.KernelConfigErrorEmpty}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil // the decoder says what is wrong
	}
	for i := 0; i < len(root.Content); i += 2 {
		k := root.Content[i]
		if k.Value == keyVersion || k.Value == keyCredentials || k.Value == keyProviders || slices.ContainsFunc(r.sections, func(e entry) bool { return e.Key == k.Value }) {
			continue
		}
		return &FileError{Key: i18n.KernelConfigErrorSection, Params: map[string]any{"name": k.Value, "line": k.Line}}
	}
	return nil
}

// FileError says why a file cannot be imported at all.
type FileError struct {
	Key    i18n.Key
	Params map[string]any
}

func (e *FileError) Error() string { return fmt.Sprintf("configfile: %s %v", e.Key, e.Params) }

// Import makes the changes data asks for: it adds and changes the
// credentials and the providers, then each section the file has, in one
// transaction. It
// commits them only when apply is set and no entry has an error; the
// report tells what was, or would be, done. A file that cannot be read
// fails with a FileError.
func (r *Registry) Import(ctx context.Context, data []byte, apply bool) (*Report, error) {
	if err := r.checkKeys(data); err != nil {
		return nil, err
	}
	file := reflect.New(r.fileType())
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(file.Interface()); err != nil {
		return nil, &FileError{Key: i18n.KernelConfigErrorUnreadable, Params: map[string]any{"error": err.Error()}}
	}
	f := file.Elem()
	switch v := f.Field(0).Int(); {
	case v == 0:
		return nil, &FileError{Key: i18n.KernelConfigErrorNoVersion}
	case v > Version:
		return nil, &FileError{Key: i18n.KernelConfigErrorVersion, Params: map[string]any{"version": int(v), "supported": Version}}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	report := &Report{Sections: []SectionReport{}}
	in := &Import{report: report}
	if err := r.importCredentials(ctx, tx, f.Field(1).Interface().(*[]credentialEntry), in); err != nil {
		return nil, err
	}
	if err := r.importProviders(ctx, tx, f.Field(2).Interface().(*[]providerEntry), in); err != nil {
		return nil, err
	}
	var afters []func()
	for i, s := range r.sections {
		v := f.Field(kernelFields + i)
		if v.IsNil() {
			continue
		}
		in.begin(s.Key, s.Title)
		if !r.enabled(s.module) {
			in.Report(Item{Action: Skip, Problems: []Problem{{Key: i18n.KernelConfigErrorModuleOff, Params: map[string]any{"module": s.module}}}})
			continue
		}
		after, err := s.Import(ctx, tx, v.Interface(), in)
		if err != nil {
			return nil, fmt.Errorf("configfile: import %s: %w", s.Key, err)
		}
		if after != nil {
			afters = append(afters, after)
		}
	}
	// A section without entries, as an empty list, tells nothing.
	report.Sections = slices.DeleteFunc(report.Sections, func(s SectionReport) bool { return len(s.Items) == 0 })
	if !apply || report.Errors > 0 {
		return report, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	report.Applied = true
	for _, after := range afters {
		after()
	}
	return report, nil
}
