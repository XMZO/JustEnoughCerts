package lookup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// The web lookups of registries that have no WHOIS a script can use, from
// Whoice: each answers JSON, written here as WHOIS lines for the parsers.
// .dz tells no end, only the registration.
type webModule struct {
	name     string
	base     string
	supports func(suffix string) bool
	lookup   func(ctx context.Context, client *http.Client, base string, q model.NormalizedQuery) (body string, status int, err error)
}

var webModules = []webModule{
	{name: "dz", base: "https://api.nic.dz/v1", supports: func(s string) bool { return under(s, "dz") || under(s, "xn--lgbbat1ad8j") }, lookup: lookupDZ},
	{name: "lk", base: "https://register.domains.lk", supports: func(s string) bool { return under(s, "lk") }, lookup: lookupLK},
	{name: "ni", base: "https://apiecommercenic.uni.edu.ni/api/v1", supports: func(s string) bool { return under(s, "ni") }, lookup: lookupNI},
	{name: "pa", base: "https://nic.pa:8080", supports: func(s string) bool { return under(s, "pa") }, lookup: lookupPA},
	// DNSHE's free zones, whose WHOIS server no longer resolves.
	{name: "dnshe", base: "https://www.dnshe.com", supports: func(s string) bool { return s == "cc.cd" || s == "us.ci" || s == "de5.net" }, lookup: lookupDNSHE},
}

// web asks the registry's web lookup about q; errSkip where there is none.
func (l *Lookup) web(ctx context.Context, q model.NormalizedQuery) (*model.RawResponse, error) {
	i := slices.IndexFunc(l.webModules, func(m webModule) bool { return m.supports(q.Suffix) })
	// A web lookup is for names below a suffix, not the suffix itself.
	if i < 0 || q.Query == q.Suffix {
		return nil, errSkip
	}
	m := l.webModules[i]
	start := time.Now()
	body, status, err := m.lookup(ctx, l.webClient, strings.TrimRight(m.base, "/"), q)
	if err != nil {
		return nil, fmt.Errorf("the web lookup of .%s: %w", m.name, err)
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("the web lookup of .%s answered nothing", m.name)
	}
	return &model.RawResponse{
		Source:     model.SourceWHOISWeb,
		Server:     m.base,
		Query:      q.Query,
		Body:       body,
		StatusCode: status,
		ElapsedMs:  time.Since(start).Milliseconds(),
	}, nil
}

// under reports whether suffix is tld or a registry suffix below it, as
// com.pa below pa.
func under(suffix, tld string) bool {
	return suffix == tld || strings.HasSuffix(suffix, "."+tld)
}

func lookupDZ(ctx context.Context, client *http.Client, base string, q model.NormalizedQuery) (string, int, error) {
	segment := "/"
	if under(q.Suffix, "xn--lgbbat1ad8j") {
		segment = "/arabic/"
	}
	var r struct {
		Title        string `json:"title"`
		DomainName   string `json:"domainName"`
		Registrar    string `json:"registrar"`
		CreationDate string `json:"creationDate"`
		Organization string `json:"orgName"`
	}
	// A domain found comes with 302 (and no Location), one missing with
	// 404, both with the JSON.
	status, err := getJSON(ctx, client, base+segment+"domains/"+url.PathEscape(q.Query), &r, http.StatusFound, http.StatusNotFound)
	if err != nil {
		return "", status, err
	}
	if t := strings.TrimSpace(r.Title); t != "" {
		return t, status, nil
	}
	return lines("Domain Name", r.DomainName, "Registrar", r.Registrar, "Creation Date", r.CreationDate, "Registrant Organization", r.Organization), status, nil
}

func lookupLK(ctx context.Context, client *http.Client, base string, q model.NormalizedQuery) (string, int, error) {
	var r struct {
		Result struct {
			Availability *struct {
				Message    string `json:"message"`
				DomainName string `json:"domainName"`
				Info       *struct {
					ExpireDate   string `json:"expireDate"`
					RegisteredTo string `json:"registeredTo"`
				} `json:"domainInfo"`
			} `json:"domainAvailability"`
		} `json:"result"`
	}
	status, err := getJSON(ctx, client, base+"/proxy/domains/single-search?keyword="+url.QueryEscape(q.Query), &r)
	if err != nil || r.Result.Availability == nil {
		return "", status, err
	}
	a := r.Result.Availability
	message := strings.TrimSpace(a.Message)
	if strings.EqualFold(message, "Domain name you searched is restricted") {
		message = "Domain name is restricted"
	}
	name := cmpOr(a.DomainName, q.Query)
	if a.Info == nil {
		return lines("Message", message, "Domain Name", name), status, nil
	}
	return lines("Message", message, "Domain Name", name, "Registry Expiry Date", lkDate(a.Info.ExpireDate), "Registrant Name", a.Info.RegisteredTo), status, nil
}

var lkOrdinal = regexp.MustCompile(`\b(\d{1,2})(st|nd|rd|th)\b`)

// lkDate reads "Friday, 3rd October, 2026" and the like.
func lkDate(value string) string {
	value = strings.TrimSpace(value)
	cleaned := lkOrdinal.ReplaceAllString(value, "$1")
	for _, layout := range []string{"Monday, 2 January, 2006", "2 January, 2006", "2006-01-02"} {
		if t, err := time.Parse(layout, cleaned); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return value
}

func lookupNI(ctx context.Context, client *http.Client, base string, q model.NormalizedQuery) (string, int, error) {
	var r struct {
		Data *struct {
			ExpirationDate string `json:"fechaExpiracion"`
			Client         string `json:"cliente"`
		} `json:"datos"`
	}
	status, err := getJSON(ctx, client, base+"/dominios/whois?dominio="+url.QueryEscape(q.Query), &r, http.StatusNotFound)
	if status == http.StatusNotFound {
		return "Domain not found", status, nil
	}
	if err != nil {
		return "", status, err
	}
	if r.Data == nil {
		return lines("Domain Name", q.Query), status, nil
	}
	return lines("Domain Name", q.Query, "Registry Expiry Date", r.Data.ExpirationDate, "Registrant Name", r.Data.Client), status, nil
}

func lookupPA(ctx context.Context, client *http.Client, base string, q model.NormalizedQuery) (string, int, error) {
	var r struct {
		Message string `json:"mensaje"`
		Payload *struct {
			DomainName     string   `json:"Dominio"`
			UpdatedDate    string   `json:"fecha_actualizacion"`
			CreationDate   string   `json:"fecha_creacion"`
			ExpirationDate string   `json:"fecha_expiracion"`
			Status         string   `json:"Estatus"`
			Nameservers    []string `json:"NS"`
		} `json:"payload"`
	}
	status, err := getJSON(ctx, client, base+"/whois/"+url.PathEscape(q.Query), &r, http.StatusNotFound)
	if status == http.StatusNotFound {
		return "Domain not found", status, nil
	}
	if err != nil {
		return "", status, err
	}
	if r.Payload == nil {
		return strings.TrimSpace(r.Message), status, nil
	}
	p := r.Payload
	pairs := []string{"Domain Name", cmpOr(p.DomainName, q.Query), "Updated Date", p.UpdatedDate, "Creation Date", p.CreationDate,
		"Registry Expiry Date", p.ExpirationDate, "Domain Status", p.Status}
	for _, ns := range p.Nameservers {
		pairs = append(pairs, "Name Server", ns)
	}
	return lines(pairs...), status, nil
}

func lookupDNSHE(ctx context.Context, client *http.Client, base string, q model.NormalizedQuery) (string, int, error) {
	var r struct {
		Domain       string `json:"domain"`
		Registered   bool   `json:"registered"`
		Status       string `json:"status"`
		CreatedAt    string `json:"created_at"`
		RegisteredAt string `json:"registered_at"`
		ExpiresAt    string `json:"expires_at"`
		Registrar    string `json:"registrar"`
	}
	domain := cmpOr(q.RegisteredDomain, q.Query)
	status, err := getJSON(ctx, client, base+"/whois.php?domain="+url.QueryEscape(domain), &r)
	if err != nil {
		return "", status, err
	}
	name := cmpOr(r.Domain, domain)
	if !r.Registered {
		return lines("Domain Name", name) + "\nDomain not registered", status, nil
	}
	return lines("Domain Name", name, "Domain Status", r.Status, "Registrar", r.Registrar,
		"Creation Date", dnsheDate(cmpOr(r.CreatedAt, r.RegisteredAt)), "Registry Expiry Date", dnsheDate(r.ExpiresAt)), status, nil
}

// dnsheDate writes "2026-02-14 11:07" to the second; DNSHE tells no zone.
func dnsheDate(value string) string {
	value = strings.TrimSpace(value)
	if t, err := time.Parse("2006-01-02 15:04", value); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	return value
}

// getJSON reads the JSON answer of endpoint into out, under a 2xx status or
// one of also, which some registries send it with.
func getJSON(ctx context.Context, client *http.Client, endpoint string, out any, also ...int) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWhois))
	if err != nil {
		return resp.StatusCode, err
	}
	if (resp.StatusCode < 200 || resp.StatusCode > 299) && !slices.Contains(also, resp.StatusCode) {
		return resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil && resp.StatusCode != http.StatusNotFound {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

// lines writes pairs of a key and a value as WHOIS lines, leaving out the
// empty values.
func lines(pairs ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		if v := strings.TrimSpace(pairs[i+1]); v != "" {
			b.WriteString(pairs[i] + ": " + v + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func cmpOr(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
