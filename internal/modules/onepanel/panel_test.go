package onepanel

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// servePanel is serve for the panel's own certificate.
func servePanel(t *testing.T, f *fakePanel, config map[string]string) *panelSSL {
	t.Helper()
	pl := serve(t, f, config)
	s := &panelSSL{panel: pl, version: f.version, restart: config["restart"] != "false"}
	return s
}

func TestPanelDeploy(t *testing.T) {
	for _, tt := range []struct {
		version, path, ssl, restart string
	}{
		{apiV2, "/api/v2/core/settings/ssl/update", "Enable", "true"},
		{apiV1, "/api/v1/settings/ssl/update", "enable", "false"},
	} {
		f := &fakePanel{version: tt.version}
		s := servePanel(t, f, map[string]string{"restart": tt.restart})
		req := request(t)
		var lines []i18n.Key
		req.Log = func(l contracts.LogLine) { lines = append(lines, l.Key) }
		if _, err := s.Deploy(t.Context(), req); err != nil {
			t.Fatalf("%s: %v", tt.version, err)
		}
		key, _ := cryptox.EncodeKey(req.Key)
		if len(f.updates) != 1 || !slices.Contains(f.paths, tt.path) {
			t.Fatalf("%s: paths %v", tt.version, f.paths)
		}
		up := f.updates[0]
		if up["cert"] != string(cryptox.EncodeCertificates(req.Chain...)) || up["key"] != string(key) || up["ssl"] != tt.ssl ||
			up["sslType"] != "import-paste" || up["autoRestart"] != tt.restart {
			t.Errorf("%s: set %v", tt.version, up)
		}
		// Reached over HTTP, it tells to go over HTTPS from then on.
		want := []i18n.Key{i18n.OnepanelLogPanel, i18n.OnepanelLogRestart, i18n.OnepanelLogUseHttps}
		if tt.restart == "false" {
			want = []i18n.Key{i18n.OnepanelLogPanel, i18n.OnepanelLogUseHttps}
		}
		if !slices.Equal(lines, want) {
			t.Errorf("%s: log %v", tt.version, lines)
		}
	}
}

func TestPanelTest(t *testing.T) {
	msg, err := servePanel(t, &fakePanel{panel: "panel.example"}, map[string]string{}).Test(t.Context())
	if err != nil || msg.Key != i18n.OnepanelTestPanel || msg.Params["domain"] != "panel.example" || msg.Params["until"] != "2027-01-15 08:00:00" {
		t.Errorf("with HTTPS: %+v %v", msg, err)
	}
	if msg, err = servePanel(t, &fakePanel{}, map[string]string{}).Test(t.Context()); err != nil || msg.Key != i18n.OnepanelTestPlain {
		t.Errorf("without HTTPS: %+v %v", msg, err)
	}
	// Its HTTPS on, as its answer to plain HTTP says.
	s := servePanel(t, &fakePanel{reply: func(string) (int, string) {
		return http.StatusBadRequest, "Client sent an HTTP request to an HTTPS server.\n"
	}}, map[string]string{})
	var p *contracts.Problem
	if _, err := s.Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.OnepanelErrorHttpsNow {
		t.Errorf("HTTPS on: %v", err)
	}
}

func TestAddressFor(t *testing.T) {
	for _, tt := range []struct {
		host  string
		names []string
		want  string
	}{
		{"192.168.1.10:10086", []string{"*.example.com", "panel.example.com"}, "https://panel.example.com:10086"},
		{"192.168.1.10:10086", []string{"*.example.com"}, "https://192.168.1.10:10086"},
		{"panel.lan", []string{"panel.example.com"}, "https://panel.example.com"},
	} {
		if got := addressFor(tt.host, tt.names); got != tt.want {
			t.Errorf("%s %v: %s", tt.host, tt.names, got)
		}
	}
}
