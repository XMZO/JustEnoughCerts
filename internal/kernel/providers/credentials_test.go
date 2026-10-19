package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers/providersapi"
)

// cloudKind is the AccessKey of a cloud, which its types share.
var cloudKind = providers.CredentialKind{
	ID: "cloud", Name: "cloud.credential.name", Hint: "key_id",
	Fields: []providers.Field{
		{Key: "key_id", Kind: providers.Text, Required: true},
		{Key: "key_secret", Kind: providers.Text, Secret: true, Required: true},
	},
}

// mailKind is another kind, for a type that takes it.
var mailKind = providers.CredentialKind{
	ID: "mailbox", Name: "cloud.mailbox.name",
	Fields: []providers.Field{{Key: "pass", Kind: providers.Text, Secret: true, Required: true}},
}

// cdnType takes the cloud's credential under the same keys, storeType
// under its own, as lego's providers do; mailType takes the other kind.
var (
	cdnType = providers.Type{
		ID: "cloud.cdn", Kind: providers.Target, Brand: "Cloud CDN",
		Fields: []providers.Field{
			{Key: "key_id", Kind: providers.Text, Required: true},
			{Key: "key_secret", Kind: providers.Text, Secret: true, Required: true},
			{Key: "domains", Kind: providers.Text},
		},
		Credential: &providers.CredentialUse{Kind: cloudKind},
	}
	storeType = providers.Type{
		ID: "cloud.store", Kind: providers.Target, Brand: "Cloud store",
		Fields: []providers.Field{
			{Key: "CLOUD_KEY", Kind: providers.Text, Secret: true, Required: true},
			{Key: "CLOUD_SECRET", Kind: providers.Text, Secret: true, Required: true},
		},
		Credential: &providers.CredentialUse{Kind: cloudKind, Fields: map[string]string{"key_id": "CLOUD_KEY", "key_secret": "CLOUD_SECRET"}},
	}
	mailType = providers.Type{
		ID: "cloud.mail", Kind: providers.Notifier, Brand: "Cloud mail",
		Fields:     []providers.Field{{Key: "pass", Kind: providers.Text, Secret: true, Required: true}},
		Credential: &providers.CredentialUse{Kind: mailKind},
	}
)

func TestCredentialChecks(t *testing.T) {
	f := newFixture(t)
	factory := func(context.Context, providers.Instance) (any, error) { return nil, nil }
	reg := providers.New(f.db, f.box, func(string) bool { return true })
	s := reg.Scope("cloud")
	if err := s.Add(cdnType, factory); err != nil {
		t.Fatal(err)
	}
	fields := []providers.Field{{Key: "id", Kind: providers.Text}, {Key: "secret", Kind: providers.Text, Secret: true}}
	use := func(k providers.CredentialKind, m map[string]string) *providers.CredentialUse {
		return &providers.CredentialUse{Kind: k, Fields: m}
	}
	kind := func(fs ...providers.Field) providers.CredentialKind {
		return providers.CredentialKind{ID: "other", Name: "cloud.other.name", Fields: fs}
	}
	for name, u := range map[string]*providers.CredentialUse{
		"bad kind ID":          {Kind: providers.CredentialKind{ID: "Other", Name: "x", Fields: fields[:1]}},
		"no name":              {Kind: providers.CredentialKind{ID: "other", Fields: fields[:1]}},
		"no fields":            {Kind: providers.CredentialKind{ID: "other", Name: "x"}},
		"fills nothing":        use(kind(providers.Field{Key: "nope", Kind: providers.Text}), nil),
		"secret into plain":    use(kind(providers.Field{Key: "id", Kind: providers.Text, Secret: true}), nil),
		"a select":             use(kind(providers.Field{Key: "id", Kind: providers.Select, Options: []providers.Option{{Value: "a"}}}), nil),
		"maps a missing field": use(kind(fields[0]), map[string]string{"other": "secret"}),
		"secret hint":          {Kind: providers.CredentialKind{ID: "other", Name: "x", Fields: fields, Hint: "secret"}},
		"cloud, otherwise":     use(providers.CredentialKind{ID: "cloud", Name: "cloud.credential.name", Fields: fields}, nil),
		"other options":        {Kind: providers.CredentialKind{ID: "other", Name: "x", Fields: []providers.Field{wayOf("a")}}},
		"depends otherwise":    {Kind: providers.CredentialKind{ID: "other", Name: "x", Fields: []providers.Field{way, {Key: "token", Kind: providers.Text, Secret: true, When: &providers.When{Key: "way", Value: "key"}}}}},
	} {
		fields := fields
		if strings.HasPrefix(name, "other options") || strings.HasPrefix(name, "depends") {
			fields = wayFields
		}
		typ := providers.Type{ID: "cloud.x", Kind: providers.Target, Brand: "X", Fields: fields, Credential: u}
		if err := s.Add(typ, factory); err == nil {
			t.Errorf("%s: added", name)
		}
	}
	// A plain field of the kind may fill a secret one of the type.
	ok := providers.Type{ID: "cloud.y", Kind: providers.Target, Brand: "Y", Fields: fields,
		Credential: use(kind(providers.Field{Key: "id", Kind: providers.Text}, providers.Field{Key: "token", Kind: providers.Text}), map[string]string{"token": "secret"})}
	if err := s.Add(ok, factory); err != nil {
		t.Error(err)
	}
	if typ, _ := reg.Type("cloud.y"); typ.CredentialKind != "other" || !slices.Equal(typ.CredentialFields, []string{"id", "secret"}) {
		t.Errorf("type %+v", typ)
	}
}

// A kind with two ways of signing in, as Cloudflare's: a token, or an
// account and its key.
var (
	way       = wayOf("token", "key")
	wayFields = []providers.Field{
		way,
		{Key: "token", Kind: providers.Text, Secret: true, Required: true, When: &providers.When{Key: "way", Value: "token"}},
		{Key: "account", Kind: providers.Text, Required: true, When: &providers.When{Key: "way", Value: "key"}},
		{Key: "key", Kind: providers.Text, Secret: true, Required: true, When: &providers.When{Key: "way", Value: "key"}},
	}
	wayKind = providers.CredentialKind{ID: "ways", Name: "cloud.ways.name", Fields: wayFields}
)

func wayOf(values ...string) providers.Field {
	f := providers.Field{Key: "way", Kind: providers.Select, Default: values[0]}
	for _, v := range values {
		f.Options = append(f.Options, providers.Option{Value: v, Label: i18n.Key("cloud.way." + v)})
	}
	return f
}

// TestCredentialWays: a kind offers ways of signing in, as a select the
// fields of each way depend on. A credential without the choice takes the
// way its values are of; changing the way drops the other way's values.
func TestCredentialWays(t *testing.T) {
	f := newFixture(t)
	factory := func(context.Context, providers.Instance) (any, error) { return nil, nil }
	reg := providers.New(f.db, f.box, func(string) bool { return true })
	if err := reg.Scope("cloud").Add(providers.Type{ID: "cloud.ways", Kind: providers.Target, Brand: "W", Fields: wayFields, Credential: &providers.CredentialUse{Kind: wayKind}}, factory); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	id, err := reg.CreateCredential(ctx, "ways", "old", map[string]string{"account": "me"}, map[string]string{"key": "k"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := reg.LoadCredential(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Config["way"] != "key" || c.Secrets["key"] != "k" {
		t.Errorf("without the choice %+v %v", c.Config, slices.Collect(maps.Keys(c.Secrets)))
	}
	if err := reg.UpdateCredential(ctx, id, nil, map[string]string{"way": "token"}, map[string]string{"token": "t"}); err != nil {
		t.Fatal(err)
	}
	if c, _ = reg.LoadCredential(ctx, id); c.Config["account"] != "" || c.Secrets["key"] != "" || c.Secrets["token"] != "t" {
		t.Errorf("after the change %+v %v", c.Config, slices.Collect(maps.Keys(c.Secrets)))
	}
	var fields *providers.ValidationError
	if _, err := reg.CreateCredential(ctx, "ways", "half", map[string]string{"way": "key", "account": "me"}, nil); !errors.As(err, &fields) {
		t.Errorf("a way half filled: %v", err)
	}
}

func (f *fixture) credential(body map[string]any) providersapi.CredentialBody {
	f.t.Helper()
	resp := f.api.Post("/credentials", body)
	if resp.Code != http.StatusCreated {
		f.t.Fatalf("create credential = %d %s", resp.Code, resp.Body.String())
	}
	var out providersapi.CredentialBody
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) credentials() map[string]providersapi.CredentialBody {
	f.t.Helper()
	var out struct{ Credentials []providersapi.CredentialBody }
	if err := json.Unmarshal(f.api.Get("/credentials").Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	byName := map[string]providersapi.CredentialBody{}
	for _, c := range out.Credentials {
		byName[c.Name] = c
	}
	return byName
}

// open opens instance id, and returns the instance its factory got.
func (f *fixture) open(id int64) providers.Instance {
	f.t.Helper()
	if _, _, err := f.reg.Scope("certs").Open(f.t.Context(), id); err != nil {
		f.t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opened[len(f.opened)-1]
}

func users(c providersapi.CredentialBody) []string {
	var out []string
	for _, u := range c.UsedBy {
		out = append(out, u.Name)
	}
	return out
}

func TestCredentials(t *testing.T) {
	f := newFixture(t)
	var kinds struct{ Kinds []providers.CredentialKind }
	if err := json.Unmarshal(f.api.Get("/credentials/kinds").Body.Bytes(), &kinds); err != nil || len(kinds.Kinds) != 2 ||
		kinds.Kinds[0].ID != "cloud" || kinds.Kinds[0].Hint != "key_id" || len(kinds.Kinds[0].Fields) != 2 || kinds.Kinds[1].ID != "mailbox" {
		t.Fatalf("kinds %+v %v", kinds, err)
	}
	var full providers.Type
	if err := json.Unmarshal(f.api.Get("/providers/types/cloud.store").Body.Bytes(), &full); err != nil ||
		full.CredentialKind != "cloud" || !slices.Equal(full.CredentialFields, []string{"CLOUD_KEY", "CLOUD_SECRET"}) {
		t.Errorf("type %+v %v", full, err)
	}

	// A credential, and what is wrong with others.
	main := f.credential(map[string]any{"kind": "cloud", "name": " main ", "config": map[string]string{"key_id": "AK1234567890"}, "secrets": map[string]string{"key_secret": "s3cret"}})
	if main.Name != "main" || main.Kind != "cloud" || !main.Available || main.Config["key_id"] != "AK1234567890" || !slices.Equal(main.SecretKeys, []string{"key_secret"}) ||
		len(main.UsedBy) != 0 || main.UsedAt != nil {
		t.Errorf("credential %+v", main)
	}
	for _, c := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"kind": "cloud", "name": "main", "config": map[string]string{"key_id": "x"}, "secrets": map[string]string{"key_secret": "y"}}, "body.name=kernel.error.credential_name"},
		{map[string]any{"kind": "cloud", "name": "half", "config": map[string]string{"key_id": "x"}}, "body.secrets.key_secret=kernel.error.field_required"},
		{map[string]any{"kind": "cloud", "name": "odd", "config": map[string]string{"key_id": "x", "key_secret": "plain"}, "secrets": map[string]string{"key_secret": "y"}}, "body.config.key_secret=kernel.error.invalid_field"},
		{map[string]any{"kind": "nope", "name": "x"}, "body.kind=kernel.error.credential_kind"},
	} {
		if got := locations(f.fails(http.MethodPost, "/credentials", c.body, http.StatusUnprocessableEntity)); got != c.want {
			t.Errorf("%v: %s", c.body, got)
		}
	}

	// Instances that take their keys from it, under the type's own keys or
	// not; the listing shows none of its values.
	cdn := f.create(map[string]any{"type": "cloud.cdn", "name": "cdn", "credentialId": main.ID, "config": map[string]string{"domains": "a.example.com", "key_id": "ignored"}})
	if cdn.Credential != main.ID || len(cdn.Config) != 1 || cdn.Config["domains"] != "a.example.com" || len(cdn.SecretKeys) != 0 {
		t.Errorf("cdn %+v", cdn)
	}
	store := f.create(map[string]any{"type": "cloud.store", "name": "store", "credentialId": main.ID})
	if in := f.open(store.ID); in.Credential != main.ID || in.Secrets["CLOUD_KEY"] != "AK1234567890" || in.Secrets["CLOUD_SECRET"] != "s3cret" {
		t.Errorf("store opened as %+v", in)
	}
	if in := f.open(cdn.ID); in.Config["key_id"] != "AK1234567890" || in.Secrets["key_secret"] != "s3cret" || in.Config["domains"] != "a.example.com" {
		t.Errorf("cdn opened as %+v", in)
	}
	if c := f.credentials()["main"]; !slices.Equal(users(c), []string{"cdn", "store"}) || c.UsedAt == nil {
		t.Errorf("main %+v", c)
	}
	box := f.credential(map[string]any{"kind": "mailbox", "name": "box", "secrets": map[string]string{"pass": "p"}})
	if got := locations(f.fails(http.MethodPost, "/providers", map[string]any{"type": "cloud.cdn", "name": "x", "credentialId": box.ID}, http.StatusUnprocessableEntity)); got != "body.credentialId=kernel.error.invalid_field" {
		t.Errorf("a credential of another kind: %s", got)
	}
	if got := locations(f.fails(http.MethodPost, "/providers", map[string]any{"type": "dns.custom", "name": "x", "credentialId": main.ID}, http.StatusUnprocessableEntity)); got != "body.credentialId=kernel.error.invalid_field" {
		t.Errorf("a credential for a type that takes none: %s", got)
	}

	// A new value reaches every instance, whose last tests were of the old.
	if err := f.reg.RecordTest(t.Context(), store.ID, true, "cloud.test.ok", nil); err != nil {
		t.Fatal(err)
	}
	resp := f.api.Patch("/credentials/"+itoa(main.ID), map[string]any{"secrets": map[string]string{"key_secret": "rotated"}})
	if resp.Code != http.StatusOK {
		t.Fatalf("update %d %s", resp.Code, resp.Body.String())
	}
	if in := f.open(store.ID); in.Secrets["CLOUD_SECRET"] != "rotated" {
		t.Errorf("after the update: %+v", in.Secrets)
	}
	var stored providersapi.InstanceBody
	if err := json.Unmarshal(f.api.Get("/providers/"+itoa(store.ID)).Body.Bytes(), &stored); err != nil || stored.LastTest != nil {
		t.Errorf("the test of the old keys stayed: %+v %v", stored.LastTest, err)
	}
	if got := locations(f.fails(http.MethodPatch, "/credentials/"+itoa(box.ID), map[string]any{"name": "main"}, http.StatusUnprocessableEntity)); got != "body.name=kernel.error.credential_name" {
		t.Errorf("a taken name: %s", got)
	}

	// It may not go while instances use it.
	e := f.fails(http.MethodDelete, "/credentials/"+itoa(main.ID), nil, http.StatusConflict)
	if e.Code != "kernel.error.credential_in_use" {
		t.Errorf("delete in use: %+v", e)
	}

	// Without a credential, the fields want values of their own.
	if got := locations(f.fails(http.MethodPatch, "/providers/"+itoa(cdn.ID), map[string]any{"credentialId": 0}, http.StatusUnprocessableEntity)); got != "body.config.key_id=kernel.error.field_required body.secrets.key_secret=kernel.error.field_required" {
		t.Errorf("detached without values: %s", got)
	}
	resp = f.api.Patch("/providers/"+itoa(cdn.ID), map[string]any{"credentialId": 0, "config": map[string]string{"key_id": "AK-own"}, "secrets": map[string]string{"key_secret": "own"}})
	if resp.Code != http.StatusOK {
		t.Fatalf("detach %d %s", resp.Code, resp.Body.String())
	}
	if in := f.open(cdn.ID); in.Credential != 0 || in.Config["key_id"] != "AK-own" || in.Secrets["key_secret"] != "own" {
		t.Errorf("detached %+v", in)
	}
	if c := f.credentials()["main"]; !slices.Equal(users(c), []string{"store"}) {
		t.Errorf("main used by %v", users(c))
	}

	// The keys an instance has, or those given, become a credential of its
	// own name, or of that and a number when another has it.
	resp = f.api.Patch("/providers/"+itoa(cdn.ID), map[string]any{"newCredential": true})
	if resp.Code != http.StatusOK {
		t.Fatalf("new credential of the keys it has: %d %s", resp.Code, resp.Body.String())
	}
	made := f.credentials()["cdn"]
	if made.Kind != "cloud" || made.Config["key_id"] != "AK-own" || !slices.Equal(users(made), []string{"cdn"}) {
		t.Errorf("made %+v", made)
	}
	if in := f.open(cdn.ID); in.Credential != made.ID || in.Secrets["key_secret"] != "own" {
		t.Errorf("cdn with its credential %+v", in)
	}
	edge := f.create(map[string]any{"type": "cloud.cdn", "name": "cdn", "newCredential": true,
		"config": map[string]string{"key_id": "AK-edge", "domains": "b.example.com"}, "secrets": map[string]string{"key_secret": "edge"}})
	if c := f.credentials()["cdn 2"]; edge.Credential != c.ID || c.Config["key_id"] != "AK-edge" || edge.Config["key_id"] != "" {
		t.Errorf("edge %+v, credential %+v", edge, c)
	}
	if got := locations(f.fails(http.MethodPost, "/providers", map[string]any{"type": "cloud.store", "name": "s", "newCredential": true,
		"secrets": map[string]string{"CLOUD_KEY": "k"}}, http.StatusUnprocessableEntity)); got != "body.secrets.CLOUD_SECRET=kernel.error.field_required" {
		t.Errorf("a new credential that lacks a value: %s", got)
	}
	if _, ok := f.credentials()["s"]; ok {
		t.Error("a credential stayed of an instance that was refused")
	}

	// Once no instance uses it, it may go.
	for _, id := range []int64{store.ID} {
		if resp := f.api.Delete("/providers/" + itoa(id)); resp.Code != http.StatusNoContent {
			t.Fatalf("delete provider %d", resp.Code)
		}
	}
	if resp := f.api.Delete("/credentials/" + itoa(main.ID)); resp.Code != http.StatusNoContent {
		t.Fatalf("delete credential %d %s", resp.Code, resp.Body.String())
	}
	f.fails(http.MethodGet, "/credentials/"+itoa(main.ID), nil, http.StatusNotFound)
	f.fails(http.MethodDelete, "/credentials/"+itoa(main.ID), nil, http.StatusNotFound)

	// With the module of its types off, the kind is not available.
	f.mu.Lock()
	f.off["cloud"] = true
	f.mu.Unlock()
	if err := json.Unmarshal(f.api.Get("/credentials/kinds").Body.Bytes(), &kinds); err != nil || len(kinds.Kinds) != 0 {
		t.Errorf("kinds with the module off: %+v", kinds)
	}
	if c := f.credentials()["box"]; c.Available {
		t.Errorf("box %+v", c)
	}
	f.fails(http.MethodPost, "/credentials", map[string]any{"kind": "mailbox", "name": "y", "secrets": map[string]string{"pass": "p"}}, http.StatusUnprocessableEntity)
}

func itoa(id int64) string {
	b, _ := json.Marshal(id)
	return string(b)
}
