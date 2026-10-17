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
	// through a solver, as ACME does.
	NeedsSolver  bool `json:"needsSolver,omitempty"`
	Experimental bool `json:"experimental,omitempty"`
}

// Instance is a configured provider with its secrets opened, as a Factory
// gets it.
type Instance struct {
	ID      int64
	Type    string
	Name    string
	Config  map[string]string
	Secrets map[string]string
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
	t.Module = s.module
	t.Fields = append([]Field{}, t.Fields...) // never nil: the API sends a list
	t.Tags = slices.Clone(t.Tags)

	r := s.reg
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.sealed:
		return fmt.Errorf("provider type %s: add types while the module registers", t.ID)
	case r.types[t.ID] != nil:
		return fmt.Errorf("provider type %s exists already", t.ID)
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
}

func (r *Registry) context(id int64) string { return "provider:" + strconv.FormatInt(id, 10) }

// load returns instance id with its secrets opened.
func (r *Registry) load(ctx context.Context, id int64) (Instance, error) {
	var s stored
	err := r.db.QueryRowContext(ctx, "SELECT id, type, name, config, secrets, created_at, updated_at FROM kernel_providers WHERE id = ?", id).
		Scan(&s.id, &s.typ, &s.name, &s.config, &s.secrets, &s.created, &s.updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Instance{}, ErrNotFound
	}
	if err != nil {
		return Instance{}, err
	}
	return r.open(s)
}

func (r *Registry) open(s stored) (Instance, error) {
	in := Instance{ID: s.id, Type: s.typ, Name: s.name, Config: map[string]string{}, Secrets: map[string]string{}}
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
// defaults, and drops empty values.
func validate(t Type, config, secrets map[string]string) error {
	var errs []FieldError
	byKey := map[string]Field{}
	for _, f := range t.Fields {
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
	for _, f := range t.Fields {
		values, where := config, "body.config."
		if f.Secret {
			values, where = secrets, "body.secrets."
		}
		// The field f depends on came before it, so it holds its final value.
		if f.When != nil && config[f.When.Key] != f.When.Value {
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
	e := r.available(typ)
	if e == nil {
		return 0, ErrUnavailable
	}
	config, secrets = cloneMap(config), cloneMap(secrets)
	if err := validate(e.Type, config, secrets); err != nil {
		return 0, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	now := r.now().UnixMilli()
	plain, _ := json.Marshal(config)
	var id int64
	err = tx.QueryRowContext(ctx,
		"INSERT INTO kernel_providers (type, name, config, secrets, created_at, updated_at) VALUES (?, ?, ?, x'', ?, ?) RETURNING id",
		typ, strings.TrimSpace(name), string(plain), now, now).Scan(&id)
	if err != nil {
		return 0, err
	}
	if err := r.sealSecrets(ctx, tx, id, secrets); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

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
	var s stored
	err = tx.QueryRowContext(ctx, "SELECT id, type, name, config, secrets, created_at, updated_at FROM kernel_providers WHERE id = ?", id).
		Scan(&s.id, &s.typ, &s.name, &s.config, &s.secrets, &s.created, &s.updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
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
	if err := validate(e.Type, in.Config, in.Secrets); err != nil {
		return err
	}
	plain, _ := json.Marshal(in.Config)
	if _, err := tx.ExecContext(ctx, "UPDATE kernel_providers SET name = ?, config = ?, updated_at = ? WHERE id = ?",
		in.Name, string(plain), r.now().UnixMilli(), id); err != nil {
		return err
	}
	if err := r.sealSecrets(ctx, tx, id, in.Secrets); err != nil {
		return err
	}
	return tx.Commit()
}

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
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// List returns the instances whose type is of kind, or every instance for
// "", ordered by name.
func (r *Registry) List(ctx context.Context, kind Kind) ([]Summary, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id, type, name, config, secrets, created_at, updated_at FROM kernel_providers ORDER BY name, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var s stored
		if err := rows.Scan(&s.id, &s.typ, &s.name, &s.config, &s.secrets, &s.created, &s.updated); err != nil {
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
	var s stored
	err := r.db.QueryRowContext(ctx, "SELECT id, type, name, config, secrets, created_at, updated_at FROM kernel_providers WHERE id = ?", id).
		Scan(&s.id, &s.typ, &s.name, &s.config, &s.secrets, &s.created, &s.updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{}, ErrNotFound
	}
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
		CreatedAt:  time.UnixMilli(s.created).UTC(), UpdatedAt: time.UnixMilli(s.updated).UTC(),
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
