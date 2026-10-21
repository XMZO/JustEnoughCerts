// Package providers keeps the kinds of provider that modules offer and the
// instances people configure from them (PLAN.md 5.5): an ACME account, a
// DNS provider with its credentials, a deploy target, a notification
// channel.
//
// A module registers each type with the fields its configuration takes
// and a Factory that makes a working provider out of an instance. The
// kernel stores instances, plain settings as JSON and secret ones sealed,
// serves the API the forms are built from, and opens instances for the
// modules that use them, such as certs opening a certificate's solver.
package providers

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/secrets"
)

// Kind is what a provider does.
type Kind string

// Kinds.
const (
	Source   Kind = "source"   // where certificates come from
	Solver   Kind = "solver"   // how control of a domain is proven
	Target   Kind = "target"   // where certificates are deployed
	Notifier Kind = "notifier" // how people hear about events
)

var kinds = []Kind{Source, Solver, Target, Notifier}

// FieldKind is how a field is entered.
type FieldKind string

// Field kinds.
const (
	Text      FieldKind = "text"
	Multiline FieldKind = "multiline"
	Number    FieldKind = "number"
	URL       FieldKind = "url"
	Email     FieldKind = "email"
	Select    FieldKind = "select"
	Switch    FieldKind = "switch" // "true" or "false"
	Locale    FieldKind = "locale" // a language JEC speaks, by its tag, such as zh-CN
)

var fieldKinds = []FieldKind{Text, Multiline, Number, URL, Email, Select, Switch, Locale}

// Option is a choice of a Select field.
type Option struct {
	Value string   `json:"value"`
	Label i18n.Key `json:"label"`
}

// Field is a setting of a provider type.
type Field struct {
	// Key names the setting in the stored configuration.
	Key string `json:"key"`
	// Label is the message key of the label. Without one, the UI shows Key,
	// as for the environment variables of lego's DNS providers.
	Label i18n.Key `json:"label,omitempty"`
	// Help is the message key of a hint under the field.
	Help i18n.Key `json:"help,omitempty"`
	// Doc is a third party's own description, in English, shown as it is.
	Doc  string    `json:"doc,omitempty"`
	Kind FieldKind `json:"kind" enum:"text,multiline,number,url,email,select,switch,locale"`
	// Secret values are sealed, and never sent back once saved.
	Secret   bool     `json:"secret,omitempty"`
	Required bool     `json:"required,omitempty"`
	Default  string   `json:"default,omitempty"`
	Options  []Option `json:"options,omitempty"`
	// Advanced settings are those few people change, such as timeouts:
	// the form keeps them apart.
	Advanced bool `json:"advanced,omitempty"`
	// When limits the field to configurations where another field has a
	// value. Elsewhere the field keeps no value and is not required.
	When *When `json:"when,omitempty"`
}

// When is the condition of a field: the plain field Key, which comes
// before it, holds Value.
type When struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Type is a kind of provider a module offers.
type Type struct {
	// ID is <module>.<name>, made of lowercase letters, digits, dots,
	// underscores and hyphens. Instances refer to it, so it never changes.
	ID     string `json:"id"`
	Kind   Kind   `json:"kind" enum:"source,solver,target,notifier"`
	Module string `json:"module"`
	// Name and Description are message keys, for types JEC describes.
	Name        i18n.Key `json:"name,omitempty"`
	Description i18n.Key `json:"description,omitempty"`
	// Brand, Doc and URL describe a third party's type in its own words:
	// its name, a description in English and its website.
	Brand string `json:"brand,omitempty"`
	Doc   string `json:"doc,omitempty"`
	URL   string `json:"url,omitempty"`
	// Tags group types in the UI, such as "china" or "popular".
	Tags   []string `json:"tags,omitempty"`
	Fields []Field  `json:"fields"`
	// NeedsSolver is set on a source that proves control of the domains
	// through a solver, as ACME does; Challenges are the ways it can have
	// them proven, ChallengeDNS when left out.
	NeedsSolver bool     `json:"needsSolver,omitempty"`
	Challenges  []string `json:"challenges,omitempty" enum:"dns,http,record"`
	// OwnKey is set on a source whose certificates come with their own
	// private key (contracts.KeySource): no key type to choose.
	OwnKey bool `json:"ownKey,omitempty"`
	// Holds is set on a source with keys of its own whose providers list
	// the certificates they hold (contracts.Holder), through GET
	// /api/v1/providers/{id}/certificates.
	Holds bool `json:"holds,omitempty"`
	// Revocable is set on a source whose providers can have the
	// certificates they issued revoked (contracts.Revoker).
	Revocable bool `json:"revocable,omitempty"`
	// Backup is set on a source whose providers can stand in for a
	// certificate's own source when it fails: one that issues within an
	// attempt, never waiting and keeping nothing between attempts.
	Backup bool `json:"backup,omitempty"`
	// Challenge is how the providers of a solver type prove control of a
	// domain, ChallengeDNS when left out.
	Challenge string `json:"challenge,omitempty" enum:"dns,http"`
	// Records is set on a solver type of DNS whose providers add records
	// of any type too (contracts.RecordSolver), as vendors' validations
	// ask for.
	Records bool `json:"records,omitempty"`
	// Manual is set on a solver type of DNS whose providers add nothing
	// themselves: people add the records, so there is nothing to try.
	Manual bool `json:"manual,omitempty"`
	// Testable types make providers that can check their settings
	// (contracts.Tester), through POST /api/v1/providers/{id}/test.
	Testable bool `json:"testable,omitempty"`
	// Zones types make solvers that tell whether their account holds a
	// domain's zone (contracts.ZoneHolder), through POST
	// /api/v1/providers/{id}/zones.
	Zones bool `json:"zones,omitempty"`
	// NameServers are, for a solver type of DNS, the authoritative servers
	// of the zones its providers can change: names or their ends, as
	// ns.cloudflare.com, or the start of a label, ending in a dash, as
	// awsdns-. GET /api/v1/providers/{id}/zones tells by them whether the
	// zone of a domain is elsewhere. Empty when they are not known.
	NameServers  []string `json:"nameServers,omitempty"`
	Experimental bool     `json:"experimental,omitempty"`
	// Untested is set on a type tested only against a stand-in for the
	// service's API, never with a real account: it may not work.
	Untested bool `json:"untested,omitempty"`
	// Texts are the type's own messages, by language tag and key, for keys
	// the locale files lack, as a plugin brings its translations along. The
	// web UI adds them to its messages. Their keys start with the module ID
	// and a dot.
	Texts map[string]map[i18n.Key]string `json:"texts,omitempty"`
	// Credential is set on a type that can take some of its fields from a
	// credential that instances share, such as a cloud's AccessKey.
	// CredentialKind and CredentialFields tell the UI the same: the kind,
	// and the type's fields that a credential fills.
	Credential       *CredentialUse `json:"-"`
	CredentialKind   string         `json:"credentialKind,omitempty"`
	CredentialFields []string       `json:"credentialFields,omitempty"`
}

// Challenges: how a solver proves control of a domain.
const (
	ChallengeDNS  = "dns"  // records in the domain's DNS, by a contracts.Solver
	ChallengeHTTP = "http" // files on the domain's web server, by a contracts.FileSolver
	// ChallengeRecord is what a source asks for whose validation has DNS
	// records of any type: a solver of DNS that sets Records.
	ChallengeRecord = "record"
)

var (
	challenges       = []string{ChallengeDNS, ChallengeHTTP}
	sourceChallenges = []string{ChallengeDNS, ChallengeHTTP, ChallengeRecord}
)

// Solves returns how the providers of solver type t prove control of a
// domain.
func (t Type) Solves() string { return cmp.Or(t.Challenge, ChallengeDNS) }

// Accepts reports whether source type t can have control of its domains
// proven by a solver whose challenge is c.
func (t Type) Accepts(c string) bool {
	if len(t.Challenges) == 0 {
		return c == ChallengeDNS
	}
	return slices.Contains(t.Challenges, c)
}

// Takes reports whether source type t can have control of its domains
// proven by a solver of type solver: one of its challenge, or one that adds
// any record, for a source that asks for records.
func (t Type) Takes(solver Type) bool {
	c := solver.Solves()
	return t.Accepts(c) || (c == ChallengeDNS && solver.Records && t.Accepts(ChallengeRecord))
}

// Instance is a configured provider with its secrets opened, as a Factory
// gets it.
type Instance struct {
	ID      int64
	Type    string
	Name    string
	Config  map[string]string
	Secrets map[string]string
	// Credential is the credential that fills the fields the type takes
	// from one, 0 for none. A Factory finds its values in Config and
	// Secrets.
	Credential int64
}

// Value returns the setting key, plain or secret.
func (in Instance) Value(key string) string {
	if v, ok := in.Secrets[key]; ok {
		return v
	}
	return in.Config[key]
}

// Factory makes a working provider out of an instance, such as a
// contracts.Solver. The caller closes it when that is part of its contract.
type Factory func(ctx context.Context, in Instance) (any, error)

// Errors of the registry.
var (
	ErrNotFound    = errors.New("no such provider")
	ErrUnavailable = errors.New("the provider type is not available: unknown, or its module is off")
)

// InUseError says that a module still uses an instance.
type InUseError struct{ Module string }

func (e *InUseError) Error() string { return "the provider is in use by module " + e.Module }

// FieldError says what is wrong with a setting.
type FieldError struct {
	Location string // body.config.<key> or body.secrets.<key>
	Code     i18n.Key
}

// ValidationError lists what is wrong with a configuration.
type ValidationError struct{ Fields []FieldError }

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Location+": "+string(f.Code))
	}
	return "invalid provider settings: " + strings.Join(parts, ", ")
}

var (
	typeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	keyPattern      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
)

type entry struct {
	Type
	factory Factory
}

// Registry holds the types modules register and stores the instances.
type Registry struct {
	db      *sql.DB
	box     *secrets.Scope
	enabled func(module string) bool
	now     func() time.Time

	mu     sync.RWMutex
	sealed bool
	types  map[string]*entry
	users  []user
}

// user is a module that may refuse to see an instance deleted.
type user struct {
	module string
	uses   func(ctx context.Context, id int64) (bool, error)
}

// New returns a registry storing instances in db, sealing their secrets
// with box. enabled reports whether a module is on.
func New(db *sql.DB, box *secrets.Scope, enabled func(module string) bool) *Registry {
	return &Registry{db: db, box: box, enabled: enabled, now: time.Now, types: map[string]*entry{}}
}

// Seal ends the time for adding types. The kernel seals the registry once
// each module has registered.
func (r *Registry) Seal() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sealed = true
}

// Scope returns the handle through which module registers its types and
// opens instances.
func (r *Registry) Scope(module string) *Scope { return &Scope{reg: r, module: module} }

// Scope is a module's access to the registry.
type Scope struct {
	reg    *Registry
	module string
}

// Add registers a provider type, whose ID must start with the module ID
// and a dot.
func (s *Scope) Add(t Type, f Factory) error {
	name, ok := strings.CutPrefix(t.ID, s.module+".")
	switch {
	case !ok || !typeNamePattern.MatchString(name):
		return fmt.Errorf("provider type %q: want %s.<name> in lowercase letters, digits, dots, underscores and hyphens", t.ID, s.module)
	case !slices.Contains(kinds, t.Kind):
		return fmt.Errorf("provider type %s: unknown kind %q", t.ID, t.Kind)
	case t.Name == "" && t.Brand == "":
		return fmt.Errorf("provider type %s needs a name or a brand", t.ID)
	case f == nil:
		return fmt.Errorf("provider type %s has no factory", t.ID)
	case t.Challenge != "" && (t.Kind != Solver || !slices.Contains(challenges, t.Challenge)):
		return fmt.Errorf("provider type %s: challenge %q is for solvers, one of %v", t.ID, t.Challenge, challenges)
	case len(t.Challenges) > 0 && (t.Kind != Source || !t.NeedsSolver || slices.ContainsFunc(t.Challenges, func(c string) bool { return !slices.Contains(sourceChallenges, c) })):
		return fmt.Errorf("provider type %s: challenges %v are for sources that need a solver, out of %v", t.ID, t.Challenges, sourceChallenges)
	case t.Records && (t.Kind != Solver || t.Solves() != ChallengeDNS):
		return fmt.Errorf("provider type %s: records are for solvers of DNS", t.ID)
	case len(t.NameServers) > 0 && (t.Kind != Solver || t.Solves() != ChallengeDNS):
		return fmt.Errorf("provider type %s: name servers are for solvers of DNS", t.ID)
	case t.OwnKey && (t.Kind != Source || t.NeedsSolver):
		return fmt.Errorf("provider type %s: own keys are for sources that need no solver", t.ID)
	case t.Holds && (t.Kind != Source || !t.OwnKey):
		return fmt.Errorf("provider type %s: holding certificates is for sources with keys of their own", t.ID)
	case t.Revocable && t.Kind != Source:
		return fmt.Errorf("provider type %s: revoking is for sources", t.ID)
	case t.Backup && (t.Kind != Source || t.OwnKey):
		return fmt.Errorf("provider type %s: backups are for sources whose keys JEC makes", t.ID)
	}
	texts := make(map[string]map[i18n.Key]string, len(t.Texts))
	for tag, messages := range t.Texts {
		if _, err := language.Parse(tag); err != nil {
			return fmt.Errorf("provider type %s: texts in %q: %w", t.ID, tag, err)
		}
		texts[tag] = make(map[i18n.Key]string, len(messages))
		for key, text := range messages {
			if !strings.HasPrefix(string(key), s.module+".") {
				return fmt.Errorf("provider type %s: the key of text %s does not start with the module ID %s", t.ID, key, s.module)
			}
			texts[tag][key] = text
		}
	}
	seen, plain := map[string]bool{}, map[string]bool{}
	for _, field := range t.Fields {
		switch {
		case !keyPattern.MatchString(field.Key) || seen[field.Key]:
			return fmt.Errorf("provider type %s: field key %q is malformed or repeated", t.ID, field.Key)
		case !slices.Contains(fieldKinds, field.Kind):
			return fmt.Errorf("provider type %s: field %s has unknown kind %q", t.ID, field.Key, field.Kind)
		case field.Kind == Select && len(field.Options) == 0:
			return fmt.Errorf("provider type %s: select field %s has no options", t.ID, field.Key)
		case field.When != nil && !plain[field.When.Key]:
			return fmt.Errorf("provider type %s: field %s depends on %q, which is not a plain field before it", t.ID, field.Key, field.When.Key)
		}
		seen[field.Key] = true
		plain[field.Key] = !field.Secret
	}
	t.CredentialKind, t.CredentialFields = "", nil
	if t.Credential != nil {
		if err := checkCredential(t); err != nil {
			return err
		}
		t.CredentialKind, t.CredentialFields = t.Credential.Kind.ID, t.Credential.filled()
	}
	t.Module = s.module
	t.Fields = append([]Field{}, t.Fields...) // never nil: the API sends a list
	t.Tags = slices.Clone(t.Tags)
	t.Challenges = slices.Clone(t.Challenges)
	t.Texts = nil
	if len(texts) > 0 {
		t.Texts = texts
	}

	r := s.reg
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.sealed:
		return fmt.Errorf("provider type %s: add types while the module registers", t.ID)
	case r.types[t.ID] != nil:
		return fmt.Errorf("provider type %s exists already", t.ID)
	}
	if t.Credential != nil {
		for _, other := range r.types {
			if other.Credential != nil && other.Credential.Kind.ID == t.Credential.Kind.ID && !sameKind(other.Credential.Kind, t.Credential.Kind) {
				return fmt.Errorf("provider type %s: credential kind %s differs from that of %s", t.ID, t.Credential.Kind.ID, other.ID)
			}
		}
	}
	r.types[t.ID] = &entry{Type: t, factory: f}
	return nil
}

// UsedBy lets the module refuse the deletion of an instance it still
// uses: uses reports whether it does.
func (s *Scope) UsedBy(uses func(ctx context.Context, id int64) (bool, error)) {
	r := s.reg
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users = append(r.users, user{module: s.module, uses: uses})
}

// Open makes the working provider of instance id, through its type's
// Factory. It fails with ErrNotFound for no such instance and with
// ErrUnavailable when the type is unknown or its module is off.
func (s *Scope) Open(ctx context.Context, id int64) (any, Instance, error) {
	in, err := s.reg.load(ctx, id)
	if err != nil {
		return nil, Instance{}, err
	}
	e := s.reg.available(in.Type)
	if e == nil {
		return nil, Instance{}, fmt.Errorf("provider %d (%s): %w", id, in.Type, ErrUnavailable)
	}
	if err := s.reg.fill(ctx, &in, e.Type); err != nil {
		return nil, Instance{}, err
	}
	p, err := e.factory(ctx, in)
	if err != nil {
		return nil, Instance{}, fmt.Errorf("provider %d (%s): %w", id, in.Type, err)
	}
	return p, in, nil
}

// List returns the instances of kind as listed, for a module that works
// with each of them, such as the one sending notifications.
func (s *Scope) List(ctx context.Context, kind Kind) ([]Summary, error) { return s.reg.List(ctx, kind) }

// Get returns instance id as listed, for a module naming it.
func (s *Scope) Get(ctx context.Context, id int64) (Summary, error) { return s.reg.Get(ctx, id) }

// Type returns the type of instance id, for a module deciding what the
// instance can do.
func (s *Scope) Type(ctx context.Context, id int64) (Type, error) {
	var typ string
	err := s.reg.db.QueryRowContext(ctx, "SELECT type FROM kernel_providers WHERE id = ?", id).Scan(&typ)
	if errors.Is(err, sql.ErrNoRows) {
		return Type{}, ErrNotFound
	}
	if err != nil {
		return Type{}, err
	}
	e := s.reg.available(typ)
	if e == nil {
		return Type{}, ErrUnavailable
	}
	return e.Type, nil
}

// available returns the type id if it is registered and its module on.
func (r *Registry) available(id string) *entry {
	r.mu.RLock()
	e := r.types[id]
	r.mu.RUnlock()
	if e == nil || !r.enabled(e.Module) {
		return nil
	}
	return e
}

// Type returns type id when it is registered and its module on.
func (r *Registry) Type(id string) (Type, bool) {
	e := r.available(id)
	if e == nil {
		return Type{}, false
	}
	return e.Type, true
}

// Types returns the available types of kind, or of every kind for "",
// ordered by ID.
func (r *Registry) Types(kind Kind) []Type {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Type
	for _, e := range r.types {
		if (kind == "" || e.Kind == kind) && r.enabled(e.Module) {
			out = append(out, e.Type)
		}
	}
	slices.SortFunc(out, func(a, b Type) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// stored is an instance as stored, its secrets still sealed.
type stored struct {
	id                int64
	typ, name, config string
	secrets           []byte
	created, updated  int64
	credential        sql.NullInt64
	testedAt          sql.NullInt64
	testResult        sql.NullString
}

const providerColumns = "id, type, name, config, secrets, created_at, updated_at, credential_id, tested_at, test_result"

func scanStored(row scanner) (stored, error) {
	var s stored
	err := row.Scan(&s.id, &s.typ, &s.name, &s.config, &s.secrets, &s.created, &s.updated, &s.credential, &s.testedAt, &s.testResult)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

func (r *Registry) context(id int64) string { return "provider:" + strconv.FormatInt(id, 10) }

// load returns instance id with its secrets opened.
func (r *Registry) load(ctx context.Context, id int64) (Instance, error) {
	s, err := scanStored(r.db.QueryRowContext(ctx, "SELECT "+providerColumns+" FROM kernel_providers WHERE id = ?", id))
	if err != nil {
		return Instance{}, err
	}
	return r.open(s)
}

func (r *Registry) open(s stored) (Instance, error) {
	in := Instance{ID: s.id, Type: s.typ, Name: s.name, Config: map[string]string{}, Secrets: map[string]string{}, Credential: s.credential.Int64}
	if err := json.Unmarshal([]byte(s.config), &in.Config); err != nil {
		return Instance{}, fmt.Errorf("provider %d: config: %w", s.id, err)
	}
	plain, err := r.box.Open(s.secrets, r.context(s.id))
	if err != nil {
		return Instance{}, fmt.Errorf("provider %d: secrets: %w", s.id, err)
	}
	if err := json.Unmarshal(plain, &in.Secrets); err != nil {
		return Instance{}, fmt.Errorf("provider %d: secrets: %w", s.id, err)
	}
	return in, nil
}

// validate checks config and secrets against the fields of t, fills in
// defaults, and drops empty values. With credential, the fields a
// credential fills keep no value of their own.
func validate(t Type, config, secrets map[string]string, credential bool) error {
	var filled []string
	if credential && t.Credential != nil {
		filled = t.Credential.filled()
	}
	return validateFields(t.Fields, config, secrets, filled)
}

// validateFields checks config and secrets against fields, as validate
// does; the fields filled keep no value.
func validateFields(fields []Field, config, secrets map[string]string, filled []string) error {
	inferChoices(fields, config, secrets, filled)
	var errs []FieldError
	byKey := map[string]Field{}
	for _, f := range fields {
		byKey[f.Key] = f
	}
	for key := range config {
		if f, ok := byKey[key]; !ok || f.Secret {
			errs = append(errs, FieldError{"body.config." + key, i18n.KernelErrorInvalidField})
		}
	}
	for key := range secrets {
		if f, ok := byKey[key]; !ok || !f.Secret {
			errs = append(errs, FieldError{"body.secrets." + key, i18n.KernelErrorInvalidField})
		}
	}
	for _, f := range fields {
		values, where := config, "body.config."
		if f.Secret {
			values, where = secrets, "body.secrets."
		}
		// The field f depends on came before it, so it holds its final value.
		if (f.When != nil && config[f.When.Key] != f.When.Value) || slices.Contains(filled, f.Key) {
			delete(values, f.Key)
			continue
		}
		v := strings.TrimSpace(values[f.Key])
		if v == "" {
			delete(values, f.Key)
			if f.Default != "" && !f.Secret {
				values[f.Key] = f.Default
			} else if f.Required {
				errs = append(errs, FieldError{where + f.Key, i18n.KernelErrorFieldRequired})
			}
			continue
		}
		if f.Kind != Multiline {
			values[f.Key] = v
		}
		if !valid(f, v) {
			errs = append(errs, FieldError{where + f.Key, i18n.KernelErrorInvalidField})
		}
	}
	if len(errs) > 0 {
		slices.SortFunc(errs, func(a, b FieldError) int { return strings.Compare(a.Location, b.Location) })
		return &ValidationError{errs}
	}
	return nil
}

// inferChoices sets the select fields left without a value, which other
// fields depend on, to the first option whose fields hold a value: the
// settings of a way to sign in name the way, as did those stored before
// the choice was offered.
func inferChoices(fields []Field, config, secrets map[string]string, filled []string) {
	for _, f := range fields {
		if f.Kind != Select || strings.TrimSpace(config[f.Key]) != "" || slices.Contains(filled, f.Key) {
			continue
		}
		for _, o := range f.Options {
			if slices.ContainsFunc(fields, func(g Field) bool {
				values := config
				if g.Secret {
					values = secrets
				}
				return g.When != nil && *g.When == When{Key: f.Key, Value: o.Value} && strings.TrimSpace(values[g.Key]) != ""
			}) {
				config[f.Key] = o.Value
				break
			}
		}
	}
}

func valid(f Field, v string) bool {
	switch f.Kind {
	case Number:
		_, err := strconv.ParseFloat(v, 64)
		return err == nil
	case URL:
		return strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://")
	case Email:
		at := strings.LastIndex(v, "@")
		return at > 0 && at < len(v)-1 && !strings.ContainsAny(v, " \t")
	case Switch:
		return v == "true" || v == "false"
	case Select:
		return slices.ContainsFunc(f.Options, func(o Option) bool { return o.Value == v })
	case Locale:
		return slices.Contains(localeTags(), v)
	case Text:
		return !strings.ContainsAny(v, "\r\n")
	}
	return true
}

// localeTags are the languages a Locale field may name.
var localeTags = sync.OnceValue(func() []string {
	c, err := i18n.Default()
	if err != nil {
		return nil
	}
	var out []string
	for _, t := range c.Tags() {
		out = append(out, t.String())
	}
	return out
})

// Create stores a new instance of type typ.
func (r *Registry) Create(ctx context.Context, typ, name string, config, secrets map[string]string) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	id, err := r.CreateTx(ctx, tx, typ, name, config, secrets)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// CreateTx stores a new instance of type typ in tx, as Create does.
func (r *Registry) CreateTx(ctx context.Context, tx *sql.Tx, typ, name string, config, secrets map[string]string) (int64, error) {
	return r.CreateWithTx(ctx, tx, typ, name, config, secrets, 0)
}

// CreateWithTx stores a new instance of type typ in tx, as CreateTx does,
// whose credential, unless 0, fills the fields its type takes from one.
func (r *Registry) CreateWithTx(ctx context.Context, tx *sql.Tx, typ, name string, config, secrets map[string]string, credential int64) (int64, error) {
	config, secrets, err := r.NormalizeWith(typ, config, secrets, credential != 0)
	if err != nil {
		return 0, err
	}
	if credential != 0 {
		if err := credentialFor(ctx, tx, r.available(typ).Type, credential); err != nil {
			return 0, err
		}
	}
	now := r.now().UnixMilli()
	plain, _ := json.Marshal(config)
	var id int64
	err = tx.QueryRowContext(ctx,
		"INSERT INTO kernel_providers (type, name, config, secrets, created_at, updated_at, credential_id) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id",
		typ, strings.TrimSpace(name), string(plain), []byte{}, now, now, nullID(credential)).Scan(&id)
	if err != nil {
		return 0, err
	}
	if err := r.sealSecrets(ctx, tx, id, secrets); err != nil {
		return 0, err
	}
	return id, nil
}

// Normalize checks config and secrets against the fields of type typ and
// returns them as an instance would keep them: with the defaults filled
// in, and without empty values. The maps given stay as they are.
func (r *Registry) Normalize(typ string, config, secrets map[string]string) (map[string]string, map[string]string, error) {
	return r.NormalizeWith(typ, config, secrets, false)
}

// NormalizeWith is Normalize for an instance with a credential, or not.
func (r *Registry) NormalizeWith(typ string, config, secrets map[string]string, credential bool) (map[string]string, map[string]string, error) {
	e := r.available(typ)
	if e == nil {
		return nil, nil, ErrUnavailable
	}
	config, secrets = cloneMap(config), cloneMap(secrets)
	if err := validate(e.Type, config, secrets, credential); err != nil {
		return nil, nil, err
	}
	return config, secrets, nil
}

// nullID is id for a column that is NULL for none.
func nullID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id != 0} }

func (r *Registry) sealSecrets(ctx context.Context, tx *sql.Tx, id int64, secrets map[string]string) error {
	plain, _ := json.Marshal(secrets)
	_, err := tx.ExecContext(ctx, "UPDATE kernel_providers SET secrets = ? WHERE id = ?", r.box.Seal(plain, r.context(id)), id)
	return err
}

// Update changes instance id. A nil config keeps the plain settings; a
// non-nil one replaces them. Secrets merge: a key set to a value replaces
// that secret, a key set to "" removes it, and missing keys stay.
func (r *Registry) Update(ctx context.Context, id int64, name *string, config, secrets map[string]string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.UpdateTx(ctx, tx, id, name, config, secrets); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateTx changes instance id in tx, as Update does.
func (r *Registry) UpdateTx(ctx context.Context, tx *sql.Tx, id int64, name *string, config, secrets map[string]string) error {
	return r.UpdateWithTx(ctx, tx, id, name, config, secrets, nil)
}

// UpdateWithTx changes instance id in tx, as UpdateTx does, and its
// credential, unless nil: 0 for none, so that the fields a credential
// filled want values of their own.
func (r *Registry) UpdateWithTx(ctx context.Context, tx *sql.Tx, id int64, name *string, config, secrets map[string]string, credential *int64) error {
	s, err := scanStored(tx.QueryRowContext(ctx, "SELECT "+providerColumns+" FROM kernel_providers WHERE id = ?", id))
	if err != nil {
		return err
	}
	in, err := r.open(s)
	if err != nil {
		return err
	}
	e := r.available(in.Type)
	if e == nil {
		return ErrUnavailable
	}
	if name != nil {
		in.Name = strings.TrimSpace(*name)
	}
	if config != nil {
		in.Config = cloneMap(config)
	}
	for k, v := range secrets {
		in.Secrets[k] = v // "" removes it in validate
	}
	if credential != nil {
		if *credential != 0 && *credential != in.Credential {
			if err := credentialFor(ctx, tx, e.Type, *credential); err != nil {
				return err
			}
		}
		in.Credential = *credential
	}
	if err := validate(e.Type, in.Config, in.Secrets, in.Credential != 0); err != nil {
		return err
	}
	plain, _ := json.Marshal(in.Config)
	if _, err := tx.ExecContext(ctx, "UPDATE kernel_providers SET name = ?, config = ?, updated_at = ?, credential_id = ? WHERE id = ?",
		in.Name, string(plain), r.now().UnixMilli(), nullID(in.Credential), id); err != nil {
		return err
	}
	// What the last test said of other settings tells nothing of these.
	if config != nil || len(secrets) > 0 || credential != nil {
		if err := forgetTest(ctx, tx, id); err != nil {
			return err
		}
	}
	return r.sealSecrets(ctx, tx, id, in.Secrets)
}

// Load returns instance id with its secrets opened, for the kernel to
// write them out where a person asked for them, as into a configuration
// file.
func (r *Registry) Load(ctx context.Context, id int64) (Instance, error) { return r.load(ctx, id) }

// Delete removes instance id, unless a module still uses it.
func (r *Registry) Delete(ctx context.Context, id int64) error {
	r.mu.RLock()
	users := slices.Clone(r.users)
	r.mu.RUnlock()
	for _, u := range users {
		if !r.enabled(u.module) {
			continue
		}
		used, err := u.uses(ctx, id)
		if err != nil {
			return err
		}
		if used {
			return &InUseError{u.module}
		}
	}
	res, err := r.db.ExecContext(ctx, "DELETE FROM kernel_providers WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Summary is an instance as listed: no secret values, only which secrets
// are set.
type Summary struct {
	ID         int64
	Type       string
	Kind       Kind // "" when the type is not available
	Name       string
	Config     map[string]string
	SecretKeys []string
	Credential int64 // 0 for none
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// LastTest is what the last test said, nil before one and once the
	// settings changed.
	LastTest *TestResult
}

// TestResult is what a test of an instance said, when: its message of
// success, or its error, by message key.
type TestResult struct {
	At     time.Time
	OK     bool
	Key    i18n.Key
	Params map[string]any
}

// storedTest is a TestResult as test_result holds it.
type storedTest struct {
	OK     bool           `json:"ok"`
	Key    i18n.Key       `json:"key"`
	Params map[string]any `json:"params,omitempty"`
}

// RecordTest keeps what a test of instance id said: ok, and the message of
// key with params.
func (r *Registry) RecordTest(ctx context.Context, id int64, ok bool, key i18n.Key, params map[string]any) error {
	data, err := json.Marshal(storedTest{OK: ok, Key: key, Params: params})
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, "UPDATE kernel_providers SET tested_at = ?, test_result = ? WHERE id = ?", r.now().UnixMilli(), string(data), id)
	return err
}

// forgetTest drops the last test of instance id.
func forgetTest(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, "UPDATE kernel_providers SET tested_at = NULL, test_result = NULL WHERE id = ?", id)
	return err
}

// forgetCredentialTests drops the last tests of the instances whose fields
// credential fills.
func forgetCredentialTests(ctx context.Context, tx *sql.Tx, credential int64) error {
	_, err := tx.ExecContext(ctx, "UPDATE kernel_providers SET tested_at = NULL, test_result = NULL WHERE credential_id = ?", credential)
	return err
}

// List returns the instances whose type is of kind, or every instance for
// "", ordered by name.
func (r *Registry) List(ctx context.Context, kind Kind) ([]Summary, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+providerColumns+" FROM kernel_providers ORDER BY name, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		s, err := scanStored(rows)
		if err != nil {
			return nil, err
		}
		sum, err := r.summary(s)
		if err != nil {
			return nil, err
		}
		if kind == "" || sum.Kind == kind {
			out = append(out, sum)
		}
	}
	return out, rows.Err()
}

// Get returns instance id as listed.
func (r *Registry) Get(ctx context.Context, id int64) (Summary, error) {
	s, err := scanStored(r.db.QueryRowContext(ctx, "SELECT "+providerColumns+" FROM kernel_providers WHERE id = ?", id))
	if err != nil {
		return Summary{}, err
	}
	return r.summary(s)
}

func (r *Registry) summary(s stored) (Summary, error) {
	in, err := r.open(s)
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{
		ID: s.id, Type: s.typ, Name: s.name, Config: in.Config,
		// Never nil, so that the API sends a list even when there are none.
		SecretKeys: append([]string{}, slices.Sorted(maps.Keys(in.Secrets))...),
		Credential: in.Credential,
		CreatedAt:  time.UnixMilli(s.created).UTC(), UpdatedAt: time.UnixMilli(s.updated).UTC(),
	}
	var t storedTest
	if s.testedAt.Valid && json.Unmarshal([]byte(s.testResult.String), &t) == nil {
		sum.LastTest = &TestResult{At: time.UnixMilli(s.testedAt.Int64).UTC(), OK: t.OK, Key: t.Key, Params: t.Params}
	}
	if e := r.available(s.typ); e != nil {
		sum.Kind = e.Kind
	}
	return sum, nil
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}
