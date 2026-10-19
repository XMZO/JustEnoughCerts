package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// fakeAPI answers as JEC's API does, for two certificates; without
// monitor, as when that module is off.
func fakeAPI(monitor bool) func(context.Context, string, string, string, any, any) error {
	answers := map[string]string{
		"/manifest": `{"version": "0.1.0"}`,
		"/certs/certificates": `{"certificates": [
			{"id": 2, "domains": ["b.example.com"], "state": "deploy_failed", "current": {"notAfter": "2027-01-01T00:00:00Z"},
			 "renewAt": "2026-12-01T00:00:00Z", "targets": [5, 6],
			 "deployments": [{"state": "failed"}, {"state": "pending", "error": {"key": "x"}}, {"state": "done"}]},
			{"id": 1, "domains": ["a\"b.example.com"], "state": "issuing", "targets": []}]}`,
		"/monitor/states": `{"certificates": [{"certificateId": 2, "old": 1, "unreachable": 2}]}`,
		"/watch/sites":    `{"sites": [{"id": 1, "host": "partner.example.com", "port": 8443, "state": "ok", "notAfter": "2027-01-01T00:00:00Z"}]}`,
		"/watch/domains":  `{"domains": [{"name": "shop.com", "state": "expiring", "expiresAt": "2027-01-01T00:00:00Z"}, {"name": "shop.de", "state": "unknown"}]}`,
		"/system/health":  `{"jobs": [{"name": "certs.renew"}, {"name": "watch.domains", "error": "timeout"}], "disk": {"free": 1000, "total": 4000}}`,
	}
	return func(_ context.Context, via, method, path string, _, out any) error {
		if via != "Prometheus" || method != http.MethodGet {
			panic(via + " " + method)
		}
		if (path == "/monitor/states" || strings.HasPrefix(path, "/watch/")) && !monitor {
			return &module.CallError{Status: http.StatusNotFound}
		}
		return json.Unmarshal([]byte(answers[path]), out)
	}
}

func TestRender(t *testing.T) {
	m := &Module{call: fakeAPI(true)}
	out, err := m.render(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{
		`jec_build_info{version="0.1.0"} 1`,
		`jec_certificates{state="deploy_failed"} 1`,
		`jec_certificates{state="ok"} 0`,
		`jec_certificate_state{id="2",domain="b.example.com",state="deploy_failed"} 1`,
		`jec_certificate_state{id="1",domain="a\"b.example.com",state="issuing"} 1`,
		`jec_certificate_expiry_timestamp_seconds{id="2",domain="b.example.com"} 1798761600`,
		`jec_certificate_renewal_timestamp_seconds{id="2",domain="b.example.com"} 1796083200`,
		`jec_certificate_targets{id="2",domain="b.example.com"} 2`,
		`jec_certificate_deployments_failed{id="2",domain="b.example.com"} 2`,
		`jec_certificate_served_old{id="2",domain="b.example.com"} 1`,
		`jec_certificate_unreachable{id="1",domain="a\"b.example.com"} 0`,
		"# TYPE jec_certificate_state gauge",
		`jec_site_expiry_timestamp_seconds{host="partner.example.com",port="8443"} 1798761600`,
		`jec_site_state{host="partner.example.com",port="8443",state="ok"} 1`,
		`jec_domain_expiry_timestamp_seconds{domain="shop.com"} 1798761600`,
		`jec_domain_state{domain="shop.com",state="expiring"} 1`,
		`jec_domain_state{domain="shop.de",state="unknown"} 1`,
		`jec_job_failed{job="certs.renew"} 0`,
		`jec_job_failed{job="watch.domains"} 1`,
		`jec_data_free_bytes 1000`,
		`jec_data_size_bytes 4000`,
	} {
		if !strings.Contains(text, want+"\n") {
			t.Errorf("no %s in:\n%s", want, text)
		}
	}
	// By ID, the certificate without a version has no expiry.
	if strings.Index(text, `id="1"`) > strings.Index(text, `id="2"`) || strings.Contains(text, `expiry_timestamp_seconds{id="1"`) {
		t.Errorf("order or expiry:\n%s", text)
	}

	m = &Module{call: fakeAPI(false)}
	out, err = m.render(t.Context())
	if err != nil || strings.Contains(string(out), "served_old") {
		t.Errorf("without monitor: %v\n%s", err, out)
	}
}
