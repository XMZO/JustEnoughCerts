package lookup

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// RDAP (RFC 9083): IANA's bootstrap file (RFC 9224) names the server of
// most TLDs; data/rdap-extra.json, from Whoice, adds the registries it
// leaves out (.ch, .de, .io and others), and wins where both name one.
const (
	bootstrapURL = "https://data.iana.org/rdap/dns.json"
	bootstrapTTL = 24 * time.Hour
	maxRDAP      = 4 << 20
	// userAgent is how JEC introduces itself to registries.
	userAgent = "JEC (+https://getjec.dev)"
)

//go:embed data/rdap-extra.json
var rdapExtra []byte

type rdap struct {
	client    *http.Client
	bootstrap string
	extra     map[string]string // base URL by suffix
	now       func() time.Time

	mu      sync.Mutex
	iana    map[string]string
	fetched time.Time
}

func newRDAP(client *http.Client, now func() time.Time) *rdap {
	extra := map[string]string{}
	if err := json.Unmarshal(rdapExtra, &extra); err != nil {
		panic("whoice: data/rdap-extra.json: " + err.Error())
	}
	return &rdap{client: client, bootstrap: bootstrapURL, extra: extra, now: now}
}

// server returns the base URL of the RDAP server for domain, and the
// suffix it serves: the longest suffix of domain that either list names,
// the extra list first.
func (r *rdap) server(ctx context.Context, domain string) (base, suffix string, ok bool) {
	iana := r.ianaServers(ctx)
	labels := strings.Split(domain, ".")
	for i := 1; i < len(labels); i++ {
		suffix := strings.Join(labels[i:], ".")
		if base := r.extra[suffix]; base != "" {
			return base, suffix, true
		}
		if base := iana[suffix]; base != "" {
			return base, suffix, true
		}
	}
	return "", "", false
}

// ianaServers returns the servers of IANA's bootstrap file, fetched once a
// day; when it cannot be had, the last one, or none, and the extra list
// still serves.
func (r *rdap) ianaServers(ctx context.Context) map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.iana != nil && r.now().Sub(r.fetched) < bootstrapTTL {
		return r.iana
	}
	servers, err := r.fetchBootstrap(ctx)
	if err == nil {
		r.iana, r.fetched = servers, r.now()
	}
	return r.iana
}

func (r *rdap) fetchBootstrap(ctx context.Context) (map[string]string, error) {
	var file struct {
		Services [][][]string `json:"services"`
	}
	status, body, err := r.get(ctx, r.bootstrap)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("the RDAP bootstrap file: HTTP %d", status)
	}
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, fmt.Errorf("the RDAP bootstrap file: %w", err)
	}
	servers := map[string]string{}
	for _, service := range file.Services {
		if len(service) < 2 {
			continue
		}
		base := firstURL(service[1])
		for _, suffix := range service[0] {
			if suffix = strings.Trim(strings.ToLower(suffix), "."); suffix != "" && base != "" {
				servers[suffix] = base
			}
		}
	}
	if len(servers) == 0 {
		return nil, errors.New("the RDAP bootstrap file names no servers")
	}
	return servers, nil
}

// firstURL prefers an HTTPS server to a plain one.
func firstURL(urls []string) string {
	for _, prefix := range []string{"https://", "http://"} {
		for _, u := range urls {
			if strings.HasPrefix(u, prefix) {
				return u
			}
		}
	}
	return ""
}

// lookup asks the RDAP server of q's TLD about it; errSkip without one,
// or when the only one is that of a TLD whose registry does not hold the
// names under q's private suffix (eu.org, github.io), whose 404 would
// wrongly read as "not registered".
func (r *rdap) lookup(ctx context.Context, q model.NormalizedQuery) (*model.RawResponse, error) {
	base, suffix, ok := r.server(ctx, q.Query)
	if !ok || q.PrivateSuffix && len(suffix) < len(q.Suffix) {
		return nil, errSkip
	}
	endpoint := strings.TrimRight(base, "/") + "/domain/" + url.PathEscape(q.Query)
	start := time.Now()
	status, body, err := r.get(ctx, endpoint)
	// Some registries' front ends (ZDNS) turn bursts away with 429 and
	// answer the same request a moment later.
	if err == nil && status == http.StatusTooManyRequests {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			if s, b, err := r.get(ctx, endpoint); err == nil {
				status, body = s, b
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("RDAP %s: %w", hostOf(endpoint), err)
	}
	return &model.RawResponse{
		Source:     model.SourceRDAP,
		Server:     endpoint,
		Query:      q.Query,
		Body:       string(body),
		StatusCode: status,
		ElapsedMs:  time.Since(start).Milliseconds(),
	}, nil
}

func (r *rdap) get(ctx context.Context, endpoint string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/rdap+json, application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRDAP))
	return resp.StatusCode, body, err
}

func hostOf(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		return u.Host
	}
	return endpoint
}
