package providers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

// CredentialKind is a kind of credential that the instances of several
// types can share, such as a cloud's AccessKey: the fields it holds. A type
// takes one through Type.Credential; a kind is available while such a type
// is. Every type that takes a kind declares it alike, so the kinds that
// modules share live in a library of theirs.
type CredentialKind struct {
	// ID names the kind, such as aliyun, in lowercase letters, digits,
	// dots, underscores and hyphens. Credentials refer to it.
	ID string `json:"id"`
	// Name is the message key of the kind's name, and Description of where
	// to make such a credential.
	Name        i18n.Key `json:"name"`
	Description i18n.Key `json:"description,omitempty"`
	Fields      []Field  `json:"fields"`
	// Hint is a plain field that tells credentials apart, shown in part,
	// such as an AccessKey ID.
	Hint string `json:"hint,omitempty"`
}

// CredentialUse is how a type takes a credential of Kind: Fields maps the
// key of a field of the kind to that of the type's field it fills, where
// they differ, as for the environment variables of lego's providers.
type CredentialUse struct {
	Kind   CredentialKind
	Fields map[string]string
}

// field returns the key of the type's field that the kind's field key
// fills.
func (u *CredentialUse) field(key string) string {
	if k, ok := u.Fields[key]; ok {
		return k
	}
	return key
}

// filled returns the keys of the type's fields that a credential fills.
func (u *CredentialUse) filled() []string {
	out := make([]string, 0, len(u.Kind.Fields))
	for _, f := range u.Kind.Fields {
		out = append(out, u.field(f.Key))
	}
	return out
}

// checkCredential tells what is wrong with how t takes a credential.
func checkCredential(t Type) error {
	u := t.Credential
	k := u.Kind
	switch {
	case !typeNamePattern.MatchString(k.ID):
		return fmt.Errorf("provider type %s: credential kind %q is malformed", t.ID, k.ID)
	case k.Name == "":
		return fmt.Errorf("provider type %s: credential kind %s has no name", t.ID, k.ID)
	case len(k.Fields) == 0:
		return fmt.Errorf("provider type %s: credential kind %s has no fields", t.ID, k.ID)
	}
	fields := map[string]Field{}
	for _, f := range t.Fields {
		fields[f.Key] = f
	}
	// A kind may offer ways of signing in, as a select whose fields depend
	// on it; the type's fields it fills depend on the same.
	seen, plain := map[string]bool{}, map[string]bool{}
	for _, f := range k.Fields {
		target, ok := fields[u.field(f.Key)]
		switch {
		case !keyPattern.MatchString(f.Key) || seen[f.Key]:
			return fmt.Errorf("provider type %s: credential kind %s: field key %q is malformed or repeated", t.ID, k.ID, f.Key)
		case !slices.Contains(fieldKinds, f.Kind) || f.Kind == Locale || (f.Kind == Select && (f.Secret || len(f.Options) == 0)):
			return fmt.Errorf("provider type %s: credential kind %s: field %s is of a kind credentials do not hold", t.ID, k.ID, f.Key)
		case f.When != nil && !plain[f.When.Key]:
			return fmt.Errorf("provider type %s: credential kind %s: field %s depends on %q, which is not a plain field before it", t.ID, k.ID, f.Key, f.When.Key)
		case !ok:
			return fmt.Errorf("provider type %s: credential kind %s: field %s fills no field of the type", t.ID, k.ID, f.Key)
		case f.Secret && !target.Secret:
			return fmt.Errorf("provider type %s: credential kind %s: secret field %s fills the plain field %s", t.ID, k.ID, f.Key, target.Key)
		case f.Kind == Select && (target.Kind != Select || !reflect.DeepEqual(f.Options, target.Options)):
			return fmt.Errorf("provider type %s: credential kind %s: select field %s fills %s, which offers other options", t.ID, k.ID, f.Key, target.Key)
		case f.When != nil && (target.When == nil || *target.When != When{Key: u.field(f.When.Key), Value: f.When.Value}):
			return fmt.Errorf("provider type %s: credential kind %s: field %s fills %s, which does not depend on the same", t.ID, k.ID, f.Key, target.Key)
		}
		seen[f.Key] = true
		plain[f.Key] = !f.Secret
	}
	for from := range u.Fields {
		if !seen[from] {
			return fmt.Errorf("provider type %s: credential kind %s has no field %s", t.ID, k.ID, from)
		}
	}
	if k.Hint != "" && !slices.ContainsFunc(k.Fields, func(f Field) bool { return f.Key == k.Hint && !f.Secret }) {
		return fmt.Errorf("provider type %s: credential kind %s: the hint %s is not a plain field", t.ID, k.ID, k.Hint)
	}
	return nil
}

// sameKind reports whether kinds a and b are declared alike.
func sameKind(a, b CredentialKind) bool { return reflect.DeepEqual(a, b) }

// Kinds returns the kinds of credential that available types take,
// ordered by ID.
func (r *Registry) Kinds() []CredentialKind {
	byID := map[string]CredentialKind{}
	for _, t := range r.Types("") {
		if t.Credential != nil {
			byID[t.Credential.Kind.ID] = t.Credential.Kind
		}
	}
	return slices.SortedFunc(maps.Values(byID), func(a, b CredentialKind) int { return strings.Compare(a.ID, b.ID) })
}

// Kind returns kind id, when an available type takes it.
func (r *Registry) Kind(id string) (CredentialKind, bool) {
	i := slices.IndexFunc(r.Kinds(), func(k CredentialKind) bool { return k.ID == id })
	if i < 0 {
		return CredentialKind{}, false
	}
	return r.Kinds()[i], true
}

// Errors about credentials.
var (
	ErrCredentialNotFound = errors.New("no such credential")
	ErrKindUnavailable    = errors.New("the kind of credential is not available: no type that takes it is")
)

// CredentialInUseError says that instances use a credential, which may
// then not be deleted.
type CredentialInUseError struct{ Names []string }

func (e *CredentialInUseError) Error() string {
	return "the credential is in use by " + strings.Join(e.Names, ", ")
}

// Credential is a credential with its secrets opened.
type Credential struct {
	ID      int64
	Kind    string
	Name    string
	Config  map[string]string
	Secrets map[string]string
}

// CredentialSummary is a credential as listed: no secret values, only
// which secrets are set, and the instances that use it.
type CredentialSummary struct {
	ID         int64
	Kind       string
	Available  bool // an available type takes its kind
	Name       string
	Config     map[string]string
	SecretKeys []string
	UsedBy     []Summary
	CreatedAt  time.Time
	UpdatedAt  time.Time
	UsedAt     time.Time // zero before an instance opened it
}

func (r *Registry) credentialContext(id int64) string {
	return "credential:" + strconv.FormatInt(id, 10)
}

// maxCredentialName bounds a credential's name, as the API does.
const maxCredentialName = 64

type storedCredential struct {
	id                 int64
	kind, name, config string
	secrets            []byte
	created, updated   int64
	used               sql.NullInt64
}

const credentialColumns = "id, kind, name, config, secrets, created_at, updated_at, used_at" //nolint:gosec // G101: column names, no credential

type scanner interface{ Scan(dest ...any) error }

func scanCredential(row scanner) (storedCredential, error) {
	var s storedCredential
	err := row.Scan(&s.id, &s.kind, &s.name, &s.config, &s.secrets, &s.created, &s.updated, &s.used)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrCredentialNotFound
	}
	return s, err
}

func (r *Registry) openCredential(s storedCredential) (Credential, error) {
	c := Credential{ID: s.id, Kind: s.kind, Name: s.name, Config: map[string]string{}, Secrets: map[string]string{}}
	if err := json.Unmarshal([]byte(s.config), &c.Config); err != nil {
		return Credential{}, fmt.Errorf("credential %d: config: %w", s.id, err)
	}
	plain, err := r.box.Open(s.secrets, r.credentialContext(s.id))
	if err != nil {
		return Credential{}, fmt.Errorf("credential %d: secrets: %w", s.id, err)
	}
	if err := json.Unmarshal(plain, &c.Secrets); err != nil {
		return Credential{}, fmt.Errorf("credential %d: secrets: %w", s.id, err)
	}
	return c, nil
}

// querier is a database or a transaction.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (r *Registry) loadCredential(ctx context.Context, q querier, id int64) (Credential, error) {
	s, err := scanCredential(q.QueryRowContext(ctx, "SELECT "+credentialColumns+" FROM kernel_credentials WHERE id = ?", id))
	if err != nil {
		return Credential{}, err
	}
	return r.openCredential(s)
}

// LoadCredential returns credential id with its secrets opened, for the
// kernel to write them out where a person asked for them, as into a
// configuration file.
func (r *Registry) LoadCredential(ctx context.Context, id int64) (Credential, error) {
	return r.loadCredential(ctx, r.db, id)
}

// checkName tells what is wrong with name, a credential's: it must be
// there, and not too long.
func checkName(name string) error {
	if name == "" || utf8.RuneCountInString(name) > maxCredentialName {
		return &ValidationError{[]FieldError{{"body.name", i18n.KernelErrorInvalidField}}}
	}
	return nil
}

// nameTaken tells whether another credential than id has name.
func nameTaken(ctx context.Context, q querier, name string, id int64) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM kernel_credentials WHERE name = ? AND id != ?", name, id).Scan(&n)
	return n > 0, err
}

var errNameTaken = &ValidationError{[]FieldError{{"body.name", i18n.KernelErrorCredentialName}}}

// CreateCredential stores a new credential of kind.
func (r *Registry) CreateCredential(ctx context.Context, kind, name string, config, secrets map[string]string) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	id, err := r.CreateCredentialTx(ctx, tx, kind, name, config, secrets)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// CreateCredentialTx stores a new credential of kind in tx, as
// CreateCredential does. Its name must be its own.
func (r *Registry) CreateCredentialTx(ctx context.Context, tx *sql.Tx, kind, name string, config, secrets map[string]string) (int64, error) {
	k, ok := r.Kind(kind)
	if !ok {
		return 0, ErrKindUnavailable
	}
	name = strings.TrimSpace(name)
	if err := checkName(name); err != nil {
		return 0, err
	}
	config, secrets = cloneMap(config), cloneMap(secrets)
	if err := validateFields(k.Fields, config, secrets, nil); err != nil {
		return 0, err
	}
	switch taken, err := nameTaken(ctx, tx, name, 0); {
	case err != nil:
		return 0, err
	case taken:
		return 0, errNameTaken
	}
	now := r.now().UnixMilli()
	plain, _ := json.Marshal(config)
	var id int64
	err := tx.QueryRowContext(ctx,
		"INSERT INTO kernel_credentials (kind, name, config, secrets, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) RETURNING id",
		kind, name, string(plain), []byte{}, now, now).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, r.sealCredential(ctx, tx, id, secrets)
}

func (r *Registry) sealCredential(ctx context.Context, tx *sql.Tx, id int64, secrets map[string]string) error {
	plain, _ := json.Marshal(secrets)
	_, err := tx.ExecContext(ctx, "UPDATE kernel_credentials SET secrets = ? WHERE id = ?", r.box.Seal(plain, r.credentialContext(id)), id)
	return err
}

// UpdateCredential changes credential id, as Update changes an instance:
// a nil config keeps the plain values, secrets merge.
func (r *Registry) UpdateCredential(ctx context.Context, id int64, name *string, config, secrets map[string]string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.UpdateCredentialTx(ctx, tx, id, name, config, secrets); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateCredentialTx changes credential id in tx, as UpdateCredential
// does.
func (r *Registry) UpdateCredentialTx(ctx context.Context, tx *sql.Tx, id int64, name *string, config, secrets map[string]string) error {
	c, err := r.loadCredential(ctx, tx, id)
	if err != nil {
		return err
	}
	k, ok := r.Kind(c.Kind)
	if !ok {
		return ErrKindUnavailable
	}
	if name != nil {
		c.Name = strings.TrimSpace(*name)
		if err := checkName(c.Name); err != nil {
			return err
		}
		switch taken, err := nameTaken(ctx, tx, c.Name, id); {
		case err != nil:
			return err
		case taken:
			return errNameTaken
		}
	}
	if config != nil {
		c.Config = cloneMap(config)
	}
	for key, v := range secrets {
		c.Secrets[key] = v // "" removes it in validate
	}
	if err := validateFields(k.Fields, c.Config, c.Secrets, nil); err != nil {
		return err
	}
	plain, _ := json.Marshal(c.Config)
	if _, err := tx.ExecContext(ctx, "UPDATE kernel_credentials SET name = ?, config = ?, updated_at = ? WHERE id = ?",
		c.Name, string(plain), r.now().UnixMilli(), id); err != nil {
		return err
	}
	// The tests of the instances that use it were of other values.
	if config != nil || len(secrets) > 0 {
		if err := forgetCredentialTests(ctx, tx, id); err != nil {
			return err
		}
	}
	return r.sealCredential(ctx, tx, id, c.Secrets)
}

// DeleteCredential removes credential id, unless instances use it.
func (r *Registry) DeleteCredential(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, "SELECT name FROM kernel_providers WHERE credential_id = ? ORDER BY name, id", id)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			_ = rows.Close()
			return err
		}
		names = append(names, n)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(names) > 0 {
		return &CredentialInUseError{names}
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM kernel_credentials WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrCredentialNotFound
	}
	return tx.Commit()
}

// Credentials returns the credentials, ordered by name, with the
// instances that use them.
func (r *Registry) Credentials(ctx context.Context) ([]CredentialSummary, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+credentialColumns+" FROM kernel_credentials ORDER BY name, id")
	if err != nil {
		return nil, err
	}
	var stored []storedCredential
	for rows.Next() {
		s, err := scanCredential(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		stored = append(stored, s)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	users, err := r.List(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]CredentialSummary, 0, len(stored))
	for _, s := range stored {
		sum, err := r.credentialSummary(s, users)
		if err != nil {
			return nil, err
		}
		out = append(out, sum)
	}
	return out, nil
}

// GetCredential returns credential id as listed.
func (r *Registry) GetCredential(ctx context.Context, id int64) (CredentialSummary, error) {
	s, err := scanCredential(r.db.QueryRowContext(ctx, "SELECT "+credentialColumns+" FROM kernel_credentials WHERE id = ?", id))
	if err != nil {
		return CredentialSummary{}, err
	}
	users, err := r.List(ctx, "")
	if err != nil {
		return CredentialSummary{}, err
	}
	return r.credentialSummary(s, users)
}

func (r *Registry) credentialSummary(s storedCredential, instances []Summary) (CredentialSummary, error) {
	c, err := r.openCredential(s)
	if err != nil {
		return CredentialSummary{}, err
	}
	_, available := r.Kind(s.kind)
	sum := CredentialSummary{
		ID: s.id, Kind: s.kind, Available: available, Name: s.name, Config: c.Config,
		SecretKeys: append([]string{}, slices.Sorted(maps.Keys(c.Secrets))...),
		UsedBy:     []Summary{},
		CreatedAt:  time.UnixMilli(s.created).UTC(), UpdatedAt: time.UnixMilli(s.updated).UTC(),
	}
	if s.used.Valid {
		sum.UsedAt = time.UnixMilli(s.used.Int64).UTC()
	}
	for _, in := range instances {
		if in.Credential == s.id {
			sum.UsedBy = append(sum.UsedBy, in)
		}
	}
	return sum, nil
}

// fill puts the values of the credential of in into the fields of its type
// that u says the credential fills, and notes when the credential was
// used.
func (r *Registry) fill(ctx context.Context, in *Instance, t Type) error {
	if in.Credential == 0 || t.Credential == nil {
		return nil
	}
	c, err := r.loadCredential(ctx, r.db, in.Credential)
	if err != nil {
		return fmt.Errorf("provider %d: credential %d: %w", in.ID, in.Credential, err)
	}
	if c.Kind != t.Credential.Kind.ID {
		return fmt.Errorf("provider %d: credential %d is a %s, not a %s", in.ID, c.ID, c.Kind, t.Credential.Kind.ID)
	}
	secret := map[string]bool{}
	for _, f := range t.Fields {
		secret[f.Key] = f.Secret
	}
	for _, f := range t.Credential.Kind.Fields {
		v, ok := c.Secrets[f.Key]
		if !ok {
			v, ok = c.Config[f.Key]
		}
		key := t.Credential.field(f.Key)
		delete(in.Config, key)
		delete(in.Secrets, key)
		switch {
		case !ok:
		case secret[key]:
			in.Secrets[key] = v
		default:
			in.Config[key] = v
		}
	}
	// When it was used tells people which credentials still serve.
	_, _ = r.db.ExecContext(ctx, "UPDATE kernel_credentials SET used_at = ? WHERE id = ?", r.now().UnixMilli(), c.ID)
	return nil
}

// credentialFor checks that credential id may fill the fields of type t,
// in tx: t takes a credential of its kind.
func credentialFor(ctx context.Context, tx *sql.Tx, t Type, id int64) error {
	invalid := &ValidationError{[]FieldError{{"body.credentialId", i18n.KernelErrorInvalidField}}}
	if t.Credential == nil {
		return invalid
	}
	var kind string
	err := tx.QueryRowContext(ctx, "SELECT kind FROM kernel_credentials WHERE id = ?", id).Scan(&kind)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return invalid
	case err != nil:
		return err
	case kind != t.Credential.Kind.ID:
		return invalid
	}
	return nil
}

// NewCredentialTx keeps, in tx, the values of config and secrets that the
// credential of type typ would fill as a new credential named after an
// instance's name, and returns it with the rest of config and secrets.
// The credential's name is name, or name and a number when another has it.
// What is wrong with its values is told at the type's fields.
func (r *Registry) NewCredentialTx(ctx context.Context, tx *sql.Tx, typ, name string, config, secrets map[string]string) (int64, map[string]string, map[string]string, error) {
	e := r.available(typ)
	if e == nil {
		return 0, nil, nil, ErrUnavailable
	}
	u := e.Credential
	if u == nil {
		return 0, nil, nil, &ValidationError{[]FieldError{{"body.newCredential", i18n.KernelErrorInvalidField}}}
	}
	config, secrets = cloneMap(config), cloneMap(secrets)
	secret := map[string]bool{}
	for _, f := range e.Fields {
		secret[f.Key] = f.Secret
	}
	kc, ks := map[string]string{}, map[string]string{}
	at := map[string]string{} // where the type has each of the kind's fields
	for _, f := range u.Kind.Fields {
		key := u.field(f.Key)
		values, where := config, "body.config."
		if secret[key] {
			values, where = secrets, "body.secrets."
		}
		at[f.Key] = where + key
		v := values[key]
		delete(config, key)
		delete(secrets, key)
		if f.Secret {
			ks[f.Key] = v
		} else {
			kc[f.Key] = v
		}
	}
	if err := validateFields(u.Kind.Fields, kc, ks, nil); err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			for i, fe := range ve.Fields {
				key := fe.Location[strings.LastIndex(fe.Location, ".")+1:]
				ve.Fields[i].Location = at[key]
			}
		}
		return 0, nil, nil, err
	}
	base := strings.TrimSpace(name)
	if utf8.RuneCountInString(base) > maxCredentialName-4 {
		base = string([]rune(base)[:maxCredentialName-4])
	}
	unique := base
	for n := 2; ; n++ {
		taken, err := nameTaken(ctx, tx, unique, 0)
		if err != nil {
			return 0, nil, nil, err
		}
		if !taken {
			break
		}
		unique = base + " " + strconv.Itoa(n)
	}
	id, err := r.CreateCredentialTx(ctx, tx, u.Kind.ID, unique, kc, ks)
	if err != nil {
		return 0, nil, nil, err
	}
	return id, config, secrets, nil
}

// CreateShared stores a new instance of type typ, as Create does, whose
// credential, unless 0, fills the fields its type takes from one. With
// newCredential, the values given for those fields become a new
// credential, named after the instance, which others may then share.
func (r *Registry) CreateShared(ctx context.Context, typ, name string, config, secrets map[string]string, credential int64, newCredential bool) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if newCredential {
		if credential, config, secrets, err = r.NewCredentialTx(ctx, tx, typ, name, config, secrets); err != nil {
			return 0, err
		}
	}
	id, err := r.CreateWithTx(ctx, tx, typ, name, config, secrets, credential)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// UpdateShared changes instance id, as Update does, and its credential
// unless nil: 0 for none. With newCredential, the values of the fields
// the type takes from a credential, those given or else those the
// instance has, become a new credential, named after the instance.
func (r *Registry) UpdateShared(ctx context.Context, id int64, name *string, config, secrets map[string]string, credential *int64, newCredential bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if newCredential {
		s, err := scanStored(tx.QueryRowContext(ctx, "SELECT "+providerColumns+" FROM kernel_providers WHERE id = ?", id))
		if err != nil {
			return err
		}
		in, err := r.open(s)
		if err != nil {
			return err
		}
		values, merged := in.Config, maps.Clone(in.Secrets)
		if config != nil {
			values = config
		}
		for k, v := range secrets {
			merged[k] = v // "" removes it in validate
		}
		n := in.Name
		if name != nil {
			n = *name
		}
		made, rest, restSecrets, err := r.NewCredentialTx(ctx, tx, in.Type, n, values, merged)
		if err != nil {
			return err
		}
		config, secrets, credential = rest, restSecrets, &made
	}
	if err := r.UpdateWithTx(ctx, tx, id, name, config, secrets, credential); err != nil {
		return err
	}
	return tx.Commit()
}
