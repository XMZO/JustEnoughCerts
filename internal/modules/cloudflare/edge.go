package cloudflare

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// workerSource is the code of the Worker that answers at the edge.
//
//go:embed worker.js
var workerSource []byte

// What JEC sets up in an account to answer at Cloudflare's edge, as it does
// at ESA's (PLAN.md 5.3.2). The names are fixed, so that installations of
// JEC on one account share them.
const (
	kvTitle    = "jec_validation"
	scriptName = "jec-validation"
	// binding is the name the Worker reads Workers KV by.
	binding = "FILES"
	// ruleRef names the rule that lets the requests for validation files
	// past the zone's security, whatever else it challenges.
	ruleRef         = "jec_validation"
	ruleDescription = "Just Enough Certs: let certificate authorities fetch domain validation files"
	// fileTTL bounds how long a file stays in Workers KV, should JEC never
	// remove it.
	fileTTL = 30 * 24 * time.Hour
	// reportMargin is kept of a deadline to report a wait that ran out.
	reportMargin = 5 * time.Second
)

// skipped are the features of a zone the rule lets validation files past;
// each try leaves out what a plan may not allow.
var skipped = []map[string]any{
	{
		"ruleset":  "current",
		"phases":   []string{"http_ratelimit", "http_request_firewall_managed", "http_request_sbfm"},
		"products": []string{"bic", "hot", "rateLimit", "securityLevel", "uaBlock", "waf", "zoneLockdown"},
	},
	{"ruleset": "current", "products": []string{"bic", "hot", "securityLevel", "uaBlock", "zoneLockdown"}},
	{"ruleset": "current"},
}

func edgeType() providers.Type {
	return providers.Type{
		ID: "cloudflare.edge", Kind: providers.Solver, Challenge: providers.ChallengeHTTP,
		Name: i18n.CloudflareEdgeName, Description: i18n.CloudflareEdgeDescription,
		Tags: []string{"http"}, Testable: true,
		Fields: []providers.Field{
			credentials.CloudflareAuth,
			{Key: "api_token", Label: i18n.CloudflareFieldApiToken, Help: i18n.CloudflareFieldEdgeTokenHelp, Kind: providers.Text, Secret: true, Required: true, When: credentials.CloudflareToken},
			{Key: "api_email", Label: i18n.CloudflareFieldApiEmail, Help: i18n.CloudflareFieldApiEmailHelp, Kind: providers.Text, Required: true, When: credentials.CloudflareGlobal},
			{Key: "api_key", Label: i18n.CloudflareFieldApiKey, Help: i18n.CloudflareFieldApiKeyHelp, Kind: providers.Text, Secret: true, Required: true, When: credentials.CloudflareGlobal},
			{Key: "zone", Label: i18n.CloudflareFieldZone, Help: i18n.CloudflareFieldEdgeZoneHelp, Kind: providers.Text},
			{Key: "domain", Label: i18n.CloudflareFieldDomain, Help: i18n.CloudflareFieldDomainHelp, Kind: providers.Text},
			httpx.ProxyField(),
		},
		Credential: credentials.Use(credentials.Cloudflare),
	}
}

// edge answers the HTTP validations of the websites behind a zone from
// Cloudflare's edge: the files go to Workers KV, and JEC's Worker, routed
// the requests under /.well-known/ of their hosts, answers with them. It
// sets up what it needs the first time it puts a file for a host.
type edge struct {
	rest
	m *Module
	// zone is the zone's name or ID as set; "" finds the zone of each
	// domain by its name.
	zone   string
	domain string // to test with

	mu      sync.Mutex
	zones   map[string]zoneInfo // by domain, once found
	ns      map[string]string   // the ID of the namespace, by account
	scripts map[string]bool     // accounts whose Worker runs JEC's code
	routed  map[string]bool     // domains the Worker answers for
	opened  map[string]bool     // domains the zone's security lets through
	// closed tells why the zone's security may still stop the requests for
	// validation files: the rule letting them through could not be made.
	closed error
}

// zoneInfo is a zone, with the account it belongs to.
type zoneInfo struct {
	ID, Name, Account string
}

// apiZone is a zone as the API gives it.
type apiZone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}

var (
	_ contracts.FileSolver = (*edge)(nil)
	_ contracts.Tester     = (*edge)(nil)
)

func (m *Module) edgeFactory(_ context.Context, in providers.Instance) (any, error) {
	client, err := httpx.ClientFor(httpx.Options{PublicOnly: true}, in.Config)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r, err := signIn(in, client, m.base)
	if err != nil {
		return nil, err
	}
	return &edge{
		rest:   r,
		m:      m,
		zone:   strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Config["zone"]), ".")),
		domain: strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Config["domain"]), ".")),
		zones:  map[string]zoneInfo{}, ns: map[string]string{}, scripts: map[string]bool{},
		routed: map[string]bool{}, opened: map[string]bool{},
	}, nil
}

// Close lets the connections go.
func (e *edge) Close() error {
	e.client.CloseIdleConnections()
	return nil
}

// PutFile stores f in Workers KV, setting up what answers with it first,
// and returns once the website answers with it from the public internet.
func (e *edge) PutFile(ctx context.Context, f contracts.File) error {
	domain := strings.ToLower(f.Domain)
	if !f.Valid() {
		return retry.Permanent(&contracts.Problem{Key: i18n.CloudflareErrorFilePath, Params: map[string]any{"path": f.Path}})
	}
	z, err := e.setUp(ctx, domain)
	if err != nil {
		return err
	}
	path := "/accounts/" + z.Account + "/storage/kv/namespaces/" + e.namespaceOf(z.Account) + "/values/" + url.PathEscape(domain+f.Path) +
		"?expiration_ttl=" + strconv.Itoa(int(fileTTL/time.Second))
	if _, err := e.send(ctx, http.MethodPut, path, "application/octet-stream", bytes.NewReader(f.Content), nil); err != nil {
		return explain(err)
	}
	return e.served(ctx, "http://"+domain+f.Path, f.Content)
}

// RemoveFile removes f from Workers KV, where it may be gone already.
func (e *edge) RemoveFile(ctx context.Context, f contracts.File) error {
	domain := strings.ToLower(f.Domain)
	e.mu.Lock()
	z, ok := e.zones[domain]
	ns := e.ns[z.Account]
	e.mu.Unlock()
	if !ok || ns == "" {
		return nil // never stored by this instance; it expires
	}
	path := "/accounts/" + z.Account + "/storage/kv/namespaces/" + ns + "/values/" + url.PathEscape(domain+f.Path)
	var e2 *apiError
	if _, err := e.call(ctx, http.MethodDelete, path, nil, nil); err != nil && (!errors.As(err, &e2) || e2.status != http.StatusNotFound) {
		return explain(err)
	}
	return nil
}

// Test puts a file for the domain to test with, which sets up what answers
// at the edge, waits for the website to answer with it from the public
// internet, and removes it.
func (e *edge) Test(ctx context.Context) (contracts.Message, error) {
	if e.domain == "" {
		return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.CloudflareErrorNoDomain})
	}
	token := make([]byte, 16)
	_, _ = rand.Read(token)
	f := contracts.File{Domain: e.domain, Path: "/.well-known/jec-check/" + hex.EncodeToString(token), Content: []byte(hex.EncodeToString(token))}
	defer func() { _ = e.RemoveFile(context.WithoutCancel(ctx), f) }()
	if err := e.PutFile(ctx, f); err != nil {
		return contracts.Message{}, err
	}
	return contracts.Message{Key: i18n.CloudflareTestEdgeOk, Params: map[string]any{"url": "http://" + e.domain + f.Path}}, nil
}

func (e *edge) namespaceOf(account string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ns[account]
}

// setUp makes sure the zone of domain sends its requests under
// /.well-known/ to JEC's Worker, past the zone's security: what is
// missing is set up, and what is found in place is not looked at again by
// this instance.
func (e *edge) setUp(ctx context.Context, domain string) (zoneInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	z, err := e.zoneOf(ctx, domain)
	if err != nil {
		return zoneInfo{}, err
	}
	if e.ns[z.Account] == "" {
		ns, err := e.namespace(ctx, z.Account)
		if err != nil {
			return zoneInfo{}, err
		}
		e.ns[z.Account] = ns
	}
	if !e.scripts[z.Account] {
		if err := e.script(ctx, z.Account, e.ns[z.Account]); err != nil {
			return zoneInfo{}, err
		}
		e.scripts[z.Account] = true
	}
	if !e.routed[domain] {
		if err := e.route(ctx, z, domain); err != nil {
			return zoneInfo{}, err
		}
		e.routed[domain] = true
	}
	if !e.opened[domain] {
		// The rule only matters to zones that challenge: without it, the
		// file may still be served.
		if err := e.open(ctx, z, domain); err != nil {
			e.closed = err
		} else {
			e.opened[domain] = true
		}
	}
	return z, nil
}

// zoneOf finds the zone that serves domain: the one set, or else the zone
// named as domain or a domain above it.
func (e *edge) zoneOf(ctx context.Context, domain string) (zoneInfo, error) {
	if z, ok := e.zones[domain]; ok {
		return z, nil
	}
	var names []string
	if e.zone != "" {
		names = []string{e.zone}
	} else {
		labels := strings.Split(domain, ".")
		for i := range len(labels) - 1 {
			names = append(names, strings.Join(labels[i:], "."))
		}
	}
	for _, name := range names {
		var found []apiZone
		if zoneID.MatchString(name) {
			var one apiZone
			if _, err := e.call(ctx, http.MethodGet, "/zones/"+name, nil, &one); err != nil {
				return zoneInfo{}, explain(err)
			}
			found = append(found, one)
		} else if _, err := e.call(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(name), nil, &found); err != nil {
			return zoneInfo{}, explain(err)
		}
		if len(found) > 0 {
			z := zoneInfo{ID: found[0].ID, Name: found[0].Name, Account: found[0].Account.ID}
			e.zones[domain] = z
			return z, nil
		}
	}
	return zoneInfo{}, retry.Permanent(&contracts.Problem{Key: i18n.CloudflareErrorEdgeZone, Params: map[string]any{"domain": domain}})
}

// namespace returns the ID of JEC's namespace of Workers KV in account,
// made if missing.
func (e *edge) namespace(ctx context.Context, account string) (string, error) {
	type namespace struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	for page := 1; page <= 20; page++ {
		var found []namespace
		env, err := e.call(ctx, http.MethodGet, "/accounts/"+account+"/storage/kv/namespaces?per_page=100&page="+strconv.Itoa(page), nil, &found)
		if err != nil {
			return "", explain(err)
		}
		if i := slices.IndexFunc(found, func(n namespace) bool { return n.Title == kvTitle }); i >= 0 {
			return found[i].ID, nil
		}
		if len(found) < 100 || page*100 >= env.ResultInfo.TotalCount {
			break
		}
	}
	var made namespace
	if _, err := e.call(ctx, http.MethodPost, "/accounts/"+account+"/storage/kv/namespaces", map[string]string{"title": kvTitle}, &made); err != nil {
		return "", explain(err)
	}
	return made.ID, nil
}

// script uploads JEC's Worker to account, bound to the namespace ns.
func (e *edge) script(ctx context.Context, account, ns string) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	metadata, err := json.Marshal(map[string]any{
		"main_module":        "worker.js",
		"compatibility_date": "2025-09-01",
		"bindings":           []map[string]string{{"type": "kv_namespace", "name": binding, "namespace_id": ns}},
	})
	if err != nil {
		return retry.Permanent(err)
	}
	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="metadata"`},
		"Content-Type":        {"application/json"},
	})
	if err == nil {
		_, err = part.Write(metadata)
	}
	if err == nil {
		part, err = w.CreatePart(textproto.MIMEHeader{
			"Content-Disposition": {`form-data; name="worker.js"; filename="worker.js"`},
			"Content-Type":        {"application/javascript+module"},
		})
	}
	if err == nil {
		_, err = part.Write(workerSource)
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		return retry.Permanent(err)
	}
	if _, err := e.send(ctx, http.MethodPut, "/accounts/"+account+"/workers/scripts/"+scriptName, w.FormDataContentType(), &body, nil); err != nil {
		return explain(err)
	}
	return nil
}

// route sends the requests for /.well-known/ of domain to the Worker,
// through a route of its own; when the zone does not take the domain, as
// with the custom hostnames of Cloudflare for SaaS, through a route of the
// path on every host of the zone. A route another Worker has is left alone.
func (e *edge) route(ctx context.Context, z zoneInfo, domain string) error {
	type route struct {
		ID      string `json:"id"`
		Pattern string `json:"pattern"`
		Script  string `json:"script"`
	}
	var routes []route
	if _, err := e.call(ctx, http.MethodGet, "/zones/"+z.ID+"/workers/routes", nil, &routes); err != nil {
		return explain(err)
	}
	var tried error
	for _, pattern := range []string{domain + "/.well-known/*", "*/.well-known/*"} {
		if i := slices.IndexFunc(routes, func(r route) bool { return r.Pattern == pattern }); i >= 0 {
			if routes[i].Script == scriptName {
				return nil
			}
			return retry.Permanent(&contracts.Problem{Key: i18n.CloudflareErrorRouteTaken, Params: map[string]any{"pattern": pattern, "script": routes[i].Script}})
		}
		_, err := e.call(ctx, http.MethodPost, "/zones/"+z.ID+"/workers/routes", map[string]string{"pattern": pattern, "script": scriptName}, nil)
		var refused *apiError
		if err == nil {
			return nil
		}
		if !errors.As(err, &refused) || refused.status != http.StatusBadRequest {
			return explain(err)
		}
		tried = err // the zone does not take this pattern: try the next
	}
	return explain(tried)
}

// hostSet is the hosts a rule's expression names.
var hostSet = regexp.MustCompile(`http\.host in \{([^}]*)\}`)

// open adds domain to JEC's rule of the zone's custom rules, the first of
// them, which skips the rest of them and the zone's other security for the
// requests under /.well-known/ of its hosts, so that a zone challenging
// strangers lets certificate authorities fetch the files.
func (e *edge) open(ctx context.Context, z zoneInfo, domain string) error {
	type rule struct {
		ID         string `json:"id,omitempty"`
		Ref        string `json:"ref,omitempty"`
		Expression string `json:"expression"`
	}
	var entry struct {
		ID    string `json:"id"`
		Rules []rule `json:"rules"`
	}
	phase := "/zones/" + z.ID + "/rulesets/phases/http_request_firewall_custom/entrypoint"
	_, err := e.call(ctx, http.MethodGet, phase, nil, &entry)
	var missing *apiError
	switch {
	case errors.As(err, &missing) && missing.status == http.StatusNotFound:
		entry.ID = ""
	case err != nil:
		return explain(err)
	}
	hosts := []string{domain}
	i := slices.IndexFunc(entry.Rules, func(r rule) bool { return r.Ref == ruleRef })
	if i >= 0 {
		if m := hostSet.FindStringSubmatch(entry.Rules[i].Expression); m != nil {
			for _, h := range strings.Fields(m[1]) {
				if h = strings.Trim(h, `"`); h != "" && !slices.Contains(hosts, h) {
					hosts = append(hosts, h)
				}
			}
		}
	}
	slices.Sort(hosts)
	quoted := make([]string, len(hosts))
	for j, h := range hosts {
		quoted[j] = strconv.Quote(h)
	}
	expression := `(http.host in {` + strings.Join(quoted, " ") + `} and starts_with(http.request.uri.path, "/.well-known/"))`
	if i >= 0 && entry.Rules[i].Expression == expression {
		return nil
	}
	for _, parameters := range skipped {
		body := map[string]any{
			"ref": ruleRef, "description": ruleDescription, "expression": expression,
			"action": "skip", "action_parameters": parameters, "logging": map[string]bool{"enabled": false}, "enabled": true,
		}
		switch {
		case entry.ID == "":
			_, err = e.call(ctx, http.MethodPut, phase, map[string]any{"rules": []any{body}}, nil)
		case i >= 0:
			_, err = e.call(ctx, http.MethodPatch, "/zones/"+z.ID+"/rulesets/"+entry.ID+"/rules/"+entry.Rules[i].ID, body, nil)
		default:
			body["position"] = map[string]int{"index": 1}
			_, err = e.call(ctx, http.MethodPost, "/zones/"+z.ID+"/rulesets/"+entry.ID+"/rules", body, nil)
		}
		var refused *apiError
		if err == nil || !errors.As(err, &refused) || refused.status != http.StatusBadRequest {
			break // done, or refused for another reason than what it skips
		}
		// What the plan may not allow: try skipping less.
	}
	if err != nil {
		return explain(err)
	}
	return nil
}

// served waits for address to answer with content from the public
// internet, as the CA will fetch it: Workers KV takes up to a minute to
// reach every location, and a new route a moment to take effect. A
// deadline of ctx shortens the wait, with time left to report.
func (e *edge) served(ctx context.Context, address string, content []byte) error {
	wait := e.m.propagate
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
			e.mu.Lock()
			closed := e.closed
			e.mu.Unlock()
			if closed != nil {
				// The zone's security may be what stops the fetches.
				return &contracts.Problem{Key: i18n.CloudflareErrorNotServedClosed, Params: map[string]any{"url": address}, Err: errors.Join(last, closed)}
			}
			return &contracts.Problem{Key: i18n.CloudflareErrorNotServed, Params: map[string]any{"url": address}, Err: last}
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
		mitigated := ""
		if v := resp.Header.Get("Cf-Mitigated"); v != "" {
			mitigated = " (cf-mitigated: " + v + ")"
		}
		return nil, fmt.Errorf("status %d%s: %q", resp.StatusCode, mitigated, truncate(data))
	}
	return data, nil
}

func truncate(data []byte) string {
	if len(data) > 200 {
		return string(data[:200]) + "…"
	}
	return string(data)
}
