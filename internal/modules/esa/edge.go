package esa

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// routineSource is the code of the routine that answers at the edge.
//
//go:embed routine.js
var routineSource string

// What JEC sets up in an account to answer at the edge (PLAN.md 5.4). The
// names are fixed: the routine is JEC's own, replaced whenever its code
// changes, and installations of JEC on one account share it all.
const (
	kvNamespace = "jec_validation"
	routineName = "jec-validation"
	routeName   = "jec_validation"
	// routeRule sends the requests under /.well-known/ to the routine, on
	// every domain of a site.
	routeRule = `(starts_with(http.request.uri.path, "/.well-known/"))`
	// setupDescription is what ESA's console says of the namespace and the
	// routine.
	setupDescription = "Just Enough Certs: answers domain validations"
	// fileTTL bounds how long a file stays in Edge KV, should JEC never
	// remove it.
	fileTTL = 30 * 24 * time.Hour
	// maxKey is the length of the longest key of Edge KV.
	maxKey = 512
	// reportMargin is kept of a deadline to report a wait that ran out.
	reportMargin = 5 * time.Second
)

func edgeType() providers.Type {
	return providers.Type{
		ID: "esa.edge", Kind: providers.Solver, Challenge: providers.ChallengeHTTP,
		Name: i18n.EsaEdgeName, Description: i18n.EsaEdgeDescription,
		Tags: []string{"china", "http"}, Testable: true,
		Fields: []providers.Field{
			{Key: "region", Label: i18n.EsaFieldRegion, Kind: providers.Select, Required: true, Default: regionChina, Options: regions},
			{Key: "access_key_id", Label: i18n.EsaFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "access_key_secret", Label: i18n.EsaFieldAccessKeySecret, Help: i18n.EsaFieldEdgeSecretHelp,
				Kind: providers.Text, Secret: true, Required: true},
			{Key: "domain", Label: i18n.EsaFieldDomain, Help: i18n.EsaFieldDomainHelp, Kind: providers.Text},
		},
	}
}

// routineCode returns the code of the routine, and the description of its
// version, which tells it apart from the code of other versions of JEC.
func routineCode() ([]byte, string) {
	code := []byte(strings.Replace(routineSource, "__NAMESPACE__", kvNamespace, 1))
	sum := sha256.Sum256(code)
	return code, "jec " + hex.EncodeToString(sum[:8])
}

// kvKey is the key in Edge KV of the file at path on host. Keys hold
// letters, digits, "-" and "_" only, so "_" escapes the other characters
// of host names and of the paths contracts.File allows, as routine.js
// does. It fails for a path of other characters, or a key too long.
func kvKey(host, path string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToLower(host) + path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_':
			b.WriteString("__")
		case r == '.':
			b.WriteString("_d")
		case r == '/':
			b.WriteString("_s")
		case r == '~':
			b.WriteString("_t")
		default:
			return "", false
		}
	}
	if b.Len() == 0 || b.Len() > maxKey {
		return "", false
	}
	return b.String(), true
}

// edge is the solver that answers at ESA's edge: the files go to Edge KV,
// and JEC's routine answers the requests for them on the sites of the
// account, set up as the files need it.
type edge struct {
	m      *Module
	api    edgeAPI
	domain string // to test with

	mu     sync.Mutex
	sites  []site         // of the account, once listed
	coded  bool           // the namespace is there, and the routine runs JEC's code
	routed map[int64]bool // sites whose route to the routine is set, by ID
}

var (
	_ contracts.FileSolver = (*edge)(nil)
	_ contracts.Tester     = (*edge)(nil)
)

func (m *Module) edgeFactory(_ context.Context, in providers.Instance) (any, error) {
	api, err := m.newEdge(in)
	if err != nil {
		return nil, err
	}
	domain := strings.ToLower(strings.TrimSpace(in.Config["domain"]))
	return &edge{m: m, api: api, domain: domain, routed: map[int64]bool{}}, nil
}

// Close has nothing to release.
func (*edge) Close() error { return nil }

// PutFile stores f in Edge KV, setting up what answers with it first, and
// returns once the website answers with it from the public internet.
func (e *edge) PutFile(ctx context.Context, f contracts.File) error {
	return e.put(ctx, f, e.m.propagate)
}

func (e *edge) put(ctx context.Context, f contracts.File, wait time.Duration) error {
	domain := strings.ToLower(f.Domain)
	key, ok := kvKey(domain, f.Path)
	if !f.Valid() || !ok {
		return retry.Permanent(&contracts.Problem{Key: i18n.EsaErrorFilePath, Params: map[string]any{"path": f.Path}})
	}
	if err := e.setUp(ctx, domain); err != nil {
		return err
	}
	if err := e.api.putKV(ctx, kvNamespace, key, f.Content, fileTTL); err != nil {
		return e.explain(err)
	}
	return e.served(ctx, "http://"+domain+f.Path, f.Content, wait)
}

// RemoveFile removes f from Edge KV, where it may be gone already.
func (e *edge) RemoveFile(ctx context.Context, f contracts.File) error {
	key, ok := kvKey(f.Domain, f.Path)
	if !ok {
		return nil // never stored
	}
	if err := e.api.deleteKV(ctx, kvNamespace, key); err != nil {
		return e.explain(err)
	}
	return nil
}

// Test puts a file for the domain to test with, which sets up what answers
// at the edge, waits for the website to answer with it from the public
// internet, and removes it.
func (e *edge) Test(ctx context.Context) (contracts.Message, error) {
	if e.domain == "" {
		return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.EsaErrorNoDomain})
	}
	token := make([]byte, 16)
	_, _ = rand.Read(token)
	f := contracts.File{Domain: e.domain, Path: "/.well-known/jec-check/" + hex.EncodeToString(token), Content: []byte(hex.EncodeToString(token))}
	defer func() { _ = e.RemoveFile(context.WithoutCancel(ctx), f) }()
	if err := e.put(ctx, f, e.m.propagate); err != nil {
		return contracts.Message{}, err
	}
	return contracts.Message{Key: i18n.EsaTestEdgeOk, Params: map[string]any{"url": "http://" + e.domain + f.Path}}, nil
}

// setUp makes sure the site of domain sends the requests under
// /.well-known/ to JEC's routine, which reads Edge KV: whatever is missing
// is set up, and what is found in place is not looked at again by this
// instance.
func (e *edge) setUp(ctx context.Context, domain string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, err := e.site(ctx, domain)
	if err != nil {
		return err
	}
	if !e.coded {
		if err := e.setUpCode(ctx); err != nil {
			return err
		}
		e.coded = true
	}
	if !e.routed[s.ID] {
		if err := e.setUpRoute(ctx, s); err != nil {
			return err
		}
		e.routed[s.ID] = true
	}
	return nil
}

// site finds the site of the account domain belongs to: the one with the
// longest name that domain is or ends with.
func (e *edge) site(ctx context.Context, domain string) (site, error) {
	if e.sites == nil {
		sites, err := e.api.sites(ctx)
		if err != nil {
			return site{}, e.explain(err)
		}
		e.sites = sites
	}
	var found site
	for _, s := range e.sites {
		if (domain == s.Name || strings.HasSuffix(domain, "."+s.Name)) && len(s.Name) > len(found.Name) {
			found = s
		}
	}
	if found.Name == "" {
		return site{}, retry.Permanent(&contracts.Problem{Key: i18n.EsaErrorNoSite, Params: map[string]any{"domain": domain}})
	}
	return found, nil
}

// setUpCode makes the namespace, and the routine with JEC's code deployed
// to production. ESA does not say how it reports what does not exist, so
// whatever fails to be read is created, and read again when creating fails:
// it may be there after all.
func (e *edge) setUpCode(ctx context.Context) error {
	if err := e.api.namespace(ctx, kvNamespace); err != nil {
		if cerr := e.api.createNamespace(ctx, kvNamespace, setupDescription); cerr != nil && e.api.namespace(ctx, kvNamespace) != nil {
			return e.explain(cerr)
		}
	}
	deployed, err := e.api.routine(ctx, routineName)
	if err != nil {
		if cerr := e.api.createRoutine(ctx, routineName, setupDescription); cerr != nil {
			if deployed, err = e.api.routine(ctx, routineName); err != nil {
				return e.explain(cerr)
			}
		}
	}
	code, description := routineCode()
	if slices.ContainsFunc(deployed, func(v codeVersion) bool { return sameDescription(v.Description, description) }) {
		return nil
	}

	versions, err := e.api.codeVersions(ctx, routineName)
	if err != nil {
		return e.explain(err)
	}
	version := ""
	for _, v := range versions {
		if sameDescription(v.Description, description) {
			version = v.Version // uploaded before, but not deployed
		}
	}
	if version == "" {
		// A routine keeps ten versions at most. They are all JEC's: those
		// not in production make room.
		for _, v := range versions {
			if !slices.ContainsFunc(deployed, func(d codeVersion) bool { return d.Version == v.Version }) {
				_ = e.api.deleteCode(ctx, routineName, v.Version)
			}
		}
		if version, err = e.api.uploadCode(ctx, routineName, description, code); err != nil {
			return e.explain(err)
		}
	}
	if err := e.api.deployCode(ctx, routineName, version); err != nil {
		return e.explain(err)
	}
	for _, v := range deployed {
		if v.Version != version {
			// Replaced. ESA may hold on to it a while: the next time
			// makes room then.
			_ = e.api.deleteCode(ctx, routineName, v.Version)
		}
	}
	return nil
}

// sameDescription tells whether ESA gives description back as got, which
// it may do in Base64, as it does the descriptions of routines.
func sameDescription(got, description string) bool {
	if got == description {
		return true
	}
	decoded, err := base64.StdEncoding.DecodeString(got)
	return err == nil && string(decoded) == description
}

// setUpRoute sends the requests of s under /.well-known/ to the routine,
// through a route of its own: enabled, and not bypassed, as a bypassed
// routine only lets requests through or not.
func (e *edge) setUpRoute(ctx context.Context, s site) error {
	routes, err := e.api.routes(ctx, routineName)
	if err != nil {
		return e.explain(err)
	}
	want := route{SiteID: s.ID, Name: routeName, Routine: routineName, Rule: routeRule, Enabled: true}
	if i := slices.IndexFunc(routes, func(r route) bool { return r.SiteID == s.ID }); i >= 0 {
		r := routes[i]
		if sameRule(r.Rule, routeRule) && r.Enabled && !r.Bypass {
			return nil
		}
		want.ConfigID = r.ConfigID
	}
	if err := e.api.setRoute(ctx, want); err != nil {
		return e.explain(err)
	}
	return nil
}

// sameRule compares rules as ESA may write them back: spaces and escapes
// aside.
func sameRule(a, b string) bool {
	strip := strings.NewReplacer(" ", "", "\t", "", "\n", "", `\`, "")
	return strip.Replace(a) == strip.Replace(b)
}

// served waits for address to answer with content from the public
// internet, as the CA will fetch it. Edge KV takes seconds to reach every
// node of ESA, five minutes at most, and a new routine or route a while to
// take effect. A deadline of ctx shortens the wait, with time left to
// report.
func (e *edge) served(ctx context.Context, address string, content []byte, wait time.Duration) error {
	if deadline, ok := ctx.Deadline(); ok {
		wait = min(wait, time.Until(deadline)-reportMargin)
	}
	wctx, cancel := context.WithTimeout(ctx, max(wait, time.Second))
	defer cancel()
	var last error
	for {
		got, err := e.m.fetch(wctx, address)
		if err == nil && bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(content)) {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("got %q", truncate(got))
		}
		if last == nil || wctx.Err() == nil {
			last = err // rather than the error of running out of time
		}
		select {
		case <-wctx.Done():
			if err := ctx.Err(); err != nil {
				return err
			}
			return &contracts.Problem{Key: i18n.EsaErrorNotServed, Params: map[string]any{"url": address}, Err: last}
		case <-time.After(e.m.poll):
		}
	}
}

// fetch GETs address from the public internet as a CA does.
func (m *Module) fetch(ctx context.Context, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.check.Do(req)
	if err != nil {
		return nil, httpx.Cause(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %q", resp.StatusCode, truncate(data))
	}
	return data, nil
}

func truncate(data []byte) string {
	if len(data) > 200 {
		return string(data[:200]) + "…"
	}
	return string(data)
}

// explain turns an error of ESA's API into a Problem the UI can show,
// marked for retrying or not.
func (e *edge) explain(err error) error {
	code, _ := apiErrorCode(err)
	switch {
	case strings.Contains(code, "NotOpen"), strings.Contains(code, "Halt"):
		// The service is not activated, or stopped for an overdue payment.
		return retry.Permanent(&contracts.Problem{Key: i18n.EsaErrorService, Params: map[string]any{"code": code}, Err: err})
	case isQuota(err):
		return retry.Permanent(&contracts.Problem{Key: i18n.EsaErrorLimit, Params: map[string]any{"code": code}, Err: err})
	}
	return apiProblem(err)
}
