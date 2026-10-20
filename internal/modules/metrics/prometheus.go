package metrics

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// via names the module in what it asks the API, as the audit log tells.
const via = "Prometheus"

// The states of a certificate, as the API tells them.
var states = []string{"ok", "expiring", "expired", "issuing", "deploying", "failed", "deploy_failed", "revoked", "awaiting"}

// What the metrics read of the API: only the fields they show.
type certificate struct {
	ID          int64        `json:"id"`
	Domains     []string     `json:"domains"`
	State       string       `json:"state"`
	RenewAt     *time.Time   `json:"renewAt"`
	Current     *version     `json:"current"`
	Targets     []int64      `json:"targets"`
	Deployments []deployment `json:"deployments"`
}

type version struct {
	NotAfter time.Time `json:"notAfter"`
}

type deployment struct {
	State string `json:"state"`
	Error *struct {
		Key string `json:"key"`
	} `json:"error"`
}

// site is a site the watch module watches.
type site struct {
	ID       int64      `json:"id"`
	Host     string     `json:"host"`
	Port     int        `json:"port"`
	State    string     `json:"state"`
	NotAfter *time.Time `json:"notAfter"`
}

// The states of a watched site, as the API tells them.
var siteStates = []string{"pending", "ok", "expiring", "expired", "invalid", "unreachable"}

// domain is a domain whose registration the watch module watches.
type domain struct {
	Name      string     `json:"name"`
	State     string     `json:"state"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

// The states of a watched domain, as the API tells them.
var domainStates = []string{"pending", "ok", "expiring", "expired", "unknown", "failed"}

type tally struct {
	CertificateID int64 `json:"certificateId"`
	Old           int   `json:"old"`
	Unreachable   int   `json:"unreachable"`
	Incomplete    int   `json:"incomplete"`
}

type promOutput struct {
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	Body         []byte
}

func register(a huma.API, m *Module) {
	huma.Register(a, huma.Operation{
		OperationID: "metrics-prometheus", Method: http.MethodGet, Path: "/prometheus",
		Metadata:    map[string]any{api.AnyClient: true},
		Summary:     "Metrics for Prometheus",
		Description: "The certificates in Prometheus's text format: when each ends and renews, its state, its failed deployments and what the online checks found. Prometheus scrapes it with an API token that may read.",
		Responses:   map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"text/plain": {}}}},
	}, func(ctx context.Context, _ *struct{}) (*promOutput, error) {
		body, err := m.render(ctx)
		if err != nil {
			return nil, err
		}
		return &promOutput{ContentType: "text/plain; version=0.0.4; charset=utf-8", CacheControl: "no-store", Body: body}, nil
	})
}

// render writes the metrics, read from the API now.
func (m *Module) render(ctx context.Context) ([]byte, error) {
	var manifest struct {
		Version string `json:"version"`
	}
	if err := m.call(ctx, via, http.MethodGet, "/manifest", nil, &manifest); err != nil {
		return nil, fmt.Errorf("metrics: the manifest: %w", err)
	}
	var list struct {
		Certificates []certificate `json:"certificates"`
	}
	if err := m.call(ctx, via, http.MethodGet, "/certs/certificates", nil, &list); err != nil {
		return nil, fmt.Errorf("metrics: the certificates: %w", err)
	}
	// The online checks, unless their module is off.
	var checks struct {
		Certificates []tally `json:"certificates"`
	}
	err := m.call(ctx, via, http.MethodGet, "/monitor/states", nil, &checks)
	var ce *module.CallError
	monitored := err == nil
	if err != nil && (!errors.As(err, &ce) || ce.Status != http.StatusNotFound) {
		return nil, fmt.Errorf("metrics: the checks: %w", err)
	}
	certs := list.Certificates
	slices.SortFunc(certs, func(a, b certificate) int { return cmp.Compare(a.ID, b.ID) })

	var b strings.Builder
	family := func(name, kind, help string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
	}
	family("jec_build_info", "gauge", "The version of JEC.")
	fmt.Fprintf(&b, "jec_build_info{version=%s} 1\n", quote(manifest.Version))

	// The background jobs, and the space left for the data.
	var health struct {
		Jobs []struct {
			Name  string `json:"name"`
			Error string `json:"error"`
		} `json:"jobs"`
		Disk *struct {
			Free  int64 `json:"free"`
			Total int64 `json:"total"`
		} `json:"disk"`
	}
	if err := m.call(ctx, via, http.MethodGet, "/system/health", nil, &health); err != nil {
		return nil, fmt.Errorf("metrics: the health: %w", err)
	}
	family("jec_job_failed", "gauge", "1 when the last run of a background job failed, else 0.")
	for _, j := range health.Jobs {
		fmt.Fprintf(&b, "jec_job_failed{job=%s} %d\n", quote(j.Name), bit(j.Error != ""))
	}
	// The providers, as their last test went: the daily check's, or a click's.
	var providers struct {
		Providers []struct {
			ID       int64  `json:"id"`
			Name     string `json:"name"`
			Kind     string `json:"kind"`
			LastTest *struct {
				OK bool `json:"ok"`
			} `json:"lastTest"`
		} `json:"providers"`
	}
	if err := m.call(ctx, via, http.MethodGet, "/providers", nil, &providers); err != nil {
		return nil, fmt.Errorf("metrics: the providers: %w", err)
	}
	family("jec_provider_test_ok", "gauge", "1 when the last test of a provider passed, 0 when it failed; providers not tested since their settings changed are left out.")
	for _, p := range providers.Providers {
		if p.LastTest != nil && p.Kind != "notifier" {
			fmt.Fprintf(&b, "jec_provider_test_ok{id=%s,name=%s,kind=%s} %d\n", quote(strconv.FormatInt(p.ID, 10)), quote(p.Name), quote(p.Kind), bit(p.LastTest.OK))
		}
	}
	if health.Disk != nil {
		family("jec_data_free_bytes", "gauge", "The bytes free on the file system of the data directory.")
		fmt.Fprintf(&b, "jec_data_free_bytes %d\n", health.Disk.Free)
		family("jec_data_size_bytes", "gauge", "The size of the file system of the data directory, in bytes.")
		fmt.Fprintf(&b, "jec_data_size_bytes %d\n", health.Disk.Total)
	}

	family("jec_certificates", "gauge", "Certificates by state.")
	count := map[string]int{}
	for _, c := range certs {
		count[c.State]++
	}
	for _, s := range states {
		fmt.Fprintf(&b, "jec_certificates{state=%s} %d\n", quote(s), count[s])
	}

	family("jec_certificate_state", "gauge", "1 for the state a certificate is in, 0 for the others.")
	for _, c := range certs {
		for _, s := range states {
			fmt.Fprintf(&b, "jec_certificate_state{%s,state=%s} %d\n", labels(c), quote(s), bit(c.State == s))
		}
	}
	family("jec_certificate_expiry_timestamp_seconds", "gauge", "When the version in use of a certificate ends, in Unix seconds.")
	for _, c := range certs {
		if c.Current != nil {
			fmt.Fprintf(&b, "jec_certificate_expiry_timestamp_seconds{%s} %d\n", labels(c), c.Current.NotAfter.Unix())
		}
	}
	family("jec_certificate_renewal_timestamp_seconds", "gauge", "When the next renewal of a certificate starts, in Unix seconds.")
	for _, c := range certs {
		if c.RenewAt != nil {
			fmt.Fprintf(&b, "jec_certificate_renewal_timestamp_seconds{%s} %d\n", labels(c), c.RenewAt.Unix())
		}
	}
	family("jec_certificate_targets", "gauge", "The deploy targets of a certificate.")
	for _, c := range certs {
		fmt.Fprintf(&b, "jec_certificate_targets{%s} %d\n", labels(c), len(c.Targets))
	}
	family("jec_certificate_deployments_failed", "gauge", "The deploy targets whose latest deployment of a certificate failed.")
	for _, c := range certs {
		failed := 0
		for _, d := range c.Deployments {
			if d.State == "failed" || d.State == "pending" && d.Error != nil {
				failed++
			}
		}
		fmt.Fprintf(&b, "jec_certificate_deployments_failed{%s} %d\n", labels(c), failed)
	}
	if monitored {
		byID := map[int64]tally{}
		for _, t := range checks.Certificates {
			byID[t.CertificateID] = t
		}
		family("jec_certificate_served_old", "gauge", "The domains of a certificate that still serve an older certificate.")
		for _, c := range certs {
			fmt.Fprintf(&b, "jec_certificate_served_old{%s} %d\n", labels(c), byID[c.ID].Old)
		}
		family("jec_certificate_unreachable", "gauge", "The domains of a certificate that answered nothing over TLS on port 443.")
		for _, c := range certs {
			fmt.Fprintf(&b, "jec_certificate_unreachable{%s} %d\n", labels(c), byID[c.ID].Unreachable)
		}
		family("jec_certificate_chain_incomplete", "gauge", "The domains of a certificate that serve it without the intermediates clients need.")
		for _, c := range certs {
			fmt.Fprintf(&b, "jec_certificate_chain_incomplete{%s} %d\n", labels(c), byID[c.ID].Incomplete)
		}
	}
	// The watched sites, unless their module is off.
	var watched struct {
		Sites []site `json:"sites"`
	}
	err = m.call(ctx, via, http.MethodGet, "/watch/sites", nil, &watched)
	if err != nil && (!errors.As(err, &ce) || ce.Status != http.StatusNotFound) {
		return nil, fmt.Errorf("metrics: the sites: %w", err)
	}
	if err == nil {
		family("jec_site_expiry_timestamp_seconds", "gauge", "When the certificate a watched site served last ends, in Unix seconds.")
		for _, s := range watched.Sites {
			if s.NotAfter != nil {
				fmt.Fprintf(&b, "jec_site_expiry_timestamp_seconds{%s} %d\n", siteLabels(s), s.NotAfter.Unix())
			}
		}
		family("jec_site_state", "gauge", "1 for the state a watched site is in, 0 for the others.")
		for _, s := range watched.Sites {
			for _, st := range siteStates {
				fmt.Fprintf(&b, "jec_site_state{%s,state=%s} %d\n", siteLabels(s), quote(st), bit(s.State == st))
			}
		}
		// And the domain registrations, of the same module.
		var registrations struct {
			Domains []domain `json:"domains"`
		}
		if err := m.call(ctx, via, http.MethodGet, "/watch/domains", nil, &registrations); err != nil {
			return nil, fmt.Errorf("metrics: the domains: %w", err)
		}
		family("jec_domain_expiry_timestamp_seconds", "gauge", "When the registration of a domain ends, as its registry told, in Unix seconds.")
		for _, d := range registrations.Domains {
			if d.ExpiresAt != nil {
				fmt.Fprintf(&b, "jec_domain_expiry_timestamp_seconds{domain=%s} %d\n", quote(d.Name), d.ExpiresAt.Unix())
			}
		}
		family("jec_domain_state", "gauge", "1 for the state a watched domain is in, 0 for the others.")
		for _, d := range registrations.Domains {
			for _, st := range domainStates {
				fmt.Fprintf(&b, "jec_domain_state{domain=%s,state=%s} %d\n", quote(d.Name), quote(st), bit(d.State == st))
			}
		}
		// And the certificates found in transparency logs, while searched.
		var ct struct {
			Enabled bool `json:"enabled"`
			Domains []struct {
				Name string `json:"name"`
			} `json:"domains"`
			Certificates []struct {
				Domain string `json:"domain"`
				State  string `json:"state"`
			} `json:"certificates"`
		}
		if err := m.call(ctx, via, http.MethodGet, "/watch/transparency", nil, &ct); err != nil {
			return nil, fmt.Errorf("metrics: certificate transparency: %w", err)
		}
		if ct.Enabled {
			unknown := map[string]int{}
			for _, d := range ct.Domains {
				unknown[d.Name] = 0
			}
			for _, c := range ct.Certificates {
				if c.State == "unknown" {
					unknown[c.Domain]++
				}
			}
			family("jec_ct_unknown_certificates", "gauge", "How many certificates found in transparency logs for a domain JEC does not hold and nobody trusted or ignored.")
			for _, name := range slices.Sorted(maps.Keys(unknown)) {
				fmt.Fprintf(&b, "jec_ct_unknown_certificates{domain=%s} %d\n", quote(name), unknown[name])
			}
		}
	}
	return []byte(b.String()), nil
}

// siteLabels are those of a watched site: its host and port.
func siteLabels(s site) string {
	return "host=" + quote(s.Host) + ",port=" + quote(strconv.Itoa(s.Port))
}

// labels are those of a certificate: its ID and its primary domain.
func labels(c certificate) string {
	domain := ""
	if len(c.Domains) > 0 {
		domain = c.Domains[0]
	}
	return "id=" + quote(strconv.FormatInt(c.ID, 10)) + ",domain=" + quote(domain)
}

// quote writes a label value as Prometheus's text format has it.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

func bit(b bool) int {
	if b {
		return 1
	}
	return 0
}
