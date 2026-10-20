package notify

import (
	"cmp"
	"context"
	"net/http"
	"slices"
)

// digestExtra is what else the weekly digest tells of, beyond the
// certificates: the providers that fail their test, and, while the watch
// module is on, the sites and domain registrations to look at and the
// certificates found in transparency logs that someone should see. What
// cannot be asked for, as of a module that is off, is left out.
type digestExtra struct {
	providers, sites, domains []string
	spotted                   int
}

func (s *service) digestExtra(ctx context.Context) digestExtra {
	var out digestExtra
	if s.call == nil {
		return out
	}
	var providers struct {
		Providers []struct {
			Name     string `json:"name"`
			Kind     string `json:"kind"`
			LastTest *struct {
				OK bool `json:"ok"`
			} `json:"lastTest"`
		} `json:"providers"`
	}
	if s.call(ctx, "notify", http.MethodGet, "/providers", nil, &providers) == nil {
		for _, p := range providers.Providers {
			if p.Kind != "notifier" && p.LastTest != nil && !p.LastTest.OK {
				out.providers = append(out.providers, p.Name)
			}
		}
	}
	var sites struct {
		Sites []struct {
			Host       string `json:"host"`
			Name       string `json:"name"`
			State      string `json:"state"`
			Incomplete bool   `json:"incomplete"`
		} `json:"sites"`
	}
	if s.call(ctx, "notify", http.MethodGet, "/watch/sites", nil, &sites) == nil {
		for _, x := range sites.Sites {
			if slices.Contains([]string{"expiring", "expired", "invalid", "unreachable"}, x.State) || x.Incomplete {
				out.sites = append(out.sites, cmp.Or(x.Name, x.Host))
			}
		}
	}
	var domains struct {
		Domains []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"domains"`
	}
	if s.call(ctx, "notify", http.MethodGet, "/watch/domains", nil, &domains) == nil {
		for _, d := range domains.Domains {
			if d.State == "expiring" || d.State == "expired" {
				out.domains = append(out.domains, d.Name)
			}
		}
	}
	var ct struct {
		Certificates []struct {
			State string `json:"state"`
		} `json:"certificates"`
	}
	if s.call(ctx, "notify", http.MethodGet, "/watch/transparency", nil, &ct) == nil {
		for _, c := range ct.Certificates {
			if c.State == "unknown" {
				out.spotted++
			}
		}
	}
	return out
}
