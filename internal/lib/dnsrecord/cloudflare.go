package dnsrecord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// cloudflareAPI is the address of Cloudflare's API.
const cloudflareAPI = "https://api.cloudflare.com/client/v4"

// cloudflare is Cloudflare's DNS, through its v4 API, with a token or the
// global key, as lego takes them.
type cloudflare struct {
	client *http.Client
	base   string
	// dns authorizes the calls on records; zone those that look zones up,
	// with a token of its own when one is set.
	dns, zone func(*http.Request)
	ttl       int
}

func openCloudflare(settings map[string]string) (API, error) {
	token := first(settings, "CF_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN")
	zoneToken := first(settings, "CF_ZONE_API_TOKEN", "CLOUDFLARE_ZONE_API_TOKEN")
	email, key := first(settings, "CF_API_EMAIL", "CLOUDFLARE_EMAIL"), first(settings, "CF_API_KEY", "CLOUDFLARE_API_KEY")
	bearer := func(t string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+t) }
	}
	c := &cloudflare{base: strings.TrimSuffix(first(settings, "CLOUDFLARE_BASE_URL"), "/"), ttl: ttl(settings, "CLOUDFLARE_TTL", 120)}
	switch {
	case token != "":
		c.dns, c.zone = bearer(token), bearer(token)
		if zoneToken != "" {
			c.zone = bearer(zoneToken)
		}
	case email != "" && key != "":
		c.dns = func(r *http.Request) {
			r.Header.Set("X-Auth-Email", email)
			r.Header.Set("X-Auth-Key", key)
		}
		c.zone = c.dns
	default:
		return nil, ErrUnsupported
	}
	if c.base == "" {
		c.base = cloudflareAPI
	}
	// Cloudflare's own address is public; one set in its place, as a
	// proxy, may not be.
	client, err := httpx.Client(httpx.Options{PublicOnly: c.base == cloudflareAPI})
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.client = client
	return c, nil
}

// call sends a request to path, with body as JSON unless nil, and decodes
// the result into out unless nil.
func (c *cloudflare) call(ctx context.Context, auth func(*http.Request), method, path string, body, out any) error {
	_, err := c.send(ctx, auth, method, path, body, out)
	return err
}

// send is call, and returns how many results there are in all, as the
// information of a listing says.
func (c *cloudflare) send(ctx context.Context, auth func(*http.Request), method, path string, body, out any) (int, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return 0, err
	}
	auth(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("dnsrecord: Cloudflare: %w", httpx.Cause(err))
	}
	defer func() { _ = resp.Body.Close() }()
	var env struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result     json.RawMessage `json:"result"`
		ResultInfo struct {
			TotalCount int `json:"total_count"`
		} `json:"result_info"`
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, fmt.Errorf("dnsrecord: Cloudflare: %w", err)
	}
	if json.Unmarshal(data, &env) != nil || !env.Success || resp.StatusCode >= 300 {
		msg := http.StatusText(resp.StatusCode)
		if len(env.Errors) > 0 {
			msg = fmt.Sprintf("[%d] %s", env.Errors[0].Code, env.Errors[0].Message)
		}
		return 0, fmt.Errorf("dnsrecord: Cloudflare %s %s: HTTP %d: %s", method, path, resp.StatusCode, msg)
	}
	if out == nil {
		return env.ResultInfo.TotalCount, nil
	}
	return env.ResultInfo.TotalCount, json.Unmarshal(env.Result, out)
}

// Domains lists the zones the token sees.
func (c *cloudflare) Domains(ctx context.Context) ([]string, int, error) {
	var zones []struct {
		Name string `json:"name"`
	}
	total, err := c.send(ctx, c.zone, http.MethodGet, "/zones?per_page="+strconv.Itoa(listed), nil, &zones)
	if err != nil {
		return nil, 0, err
	}
	names := make([]string, 0, len(zones))
	for _, z := range zones {
		names = append(names, z.Name)
	}
	return names, max(total, len(names)), nil
}

// Holds looks zone up among the zones the token sees.
func (c *cloudflare) Holds(ctx context.Context, zone string) (bool, error) {
	var zones []struct {
		Name string `json:"name"`
	}
	if _, err := c.send(ctx, c.zone, http.MethodGet, "/zones?name="+url.QueryEscape(zone), nil, &zones); err != nil {
		return false, err
	}
	for _, z := range zones {
		if strings.EqualFold(z.Name, zone) {
			return true, nil
		}
	}
	return false, nil
}

func (c *cloudflare) Add(ctx context.Context, r Record) (string, error) {
	if err := valid(r); err != nil {
		return "", err
	}
	zone, _, err := split(ctx, r.Name)
	if err != nil {
		return "", err
	}
	var zones []struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, c.zone, http.MethodGet, "/zones?name="+url.QueryEscape(zone), nil, &zones); err != nil {
		return "", err
	}
	if len(zones) == 0 {
		return "", fmt.Errorf("dnsrecord: Cloudflare has no zone %s for the token", zone)
	}
	var made struct {
		ID string `json:"id"`
	}
	body := map[string]any{"type": r.Type, "name": r.Name, "content": r.Value, "ttl": c.ttl}
	if err := c.call(ctx, c.dns, http.MethodPost, "/zones/"+zones[0].ID+"/dns_records", body, &made); err != nil {
		return "", err
	}
	if made.ID == "" {
		return "", errors.New("dnsrecord: Cloudflare answered no record ID")
	}
	return zones[0].ID + "/" + made.ID, nil
}

func (c *cloudflare) Remove(ctx context.Context, _ Record, id string) error {
	zone, record, ok := strings.Cut(id, "/")
	if !ok {
		return fmt.Errorf("dnsrecord: no Cloudflare record %q", id)
	}
	return c.call(ctx, c.dns, http.MethodDelete, "/zones/"+url.PathEscape(zone)+"/dns_records/"+url.PathEscape(record), nil, nil)
}
