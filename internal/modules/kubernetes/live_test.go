package kubernetes

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// TestLive deploys to a real API server. CI does not run it: JEC_TEST_K8S
// is the server's address, JEC_TEST_K8S_CA a file with its CA, and
// JEC_TEST_K8S_TOKEN a token that may get, create and patch secrets in
// the namespace JEC_TEST_K8S_NAMESPACE, and nothing in default.
func TestLive(t *testing.T) {
	base, caFile := os.Getenv("JEC_TEST_K8S"), os.Getenv("JEC_TEST_K8S_CA")
	if base == "" || caFile == "" {
		t.Skip("JEC_TEST_K8S and JEC_TEST_K8S_CA are not set")
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]string{"url": base, "ca": string(ca), "namespace": os.Getenv("JEC_TEST_K8S_NAMESPACE"),
		"secret": fmt.Sprintf("jec-live-%d", time.Now().UnixNano()), "allow_private": "true"}
	open := func() *cluster {
		p, err := factory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"token": os.Getenv("JEC_TEST_K8S_TOKEN")}})
		if err != nil {
			t.Fatal(err)
		}
		return p.(*cluster)
	}
	c := open()
	if msg, err := c.Test(t.Context()); err != nil || msg.Key != i18n.KubernetesTestNew {
		t.Fatalf("test: %+v %v", msg, err)
	}
	var lines []contracts.LogLine
	for i, want := range []i18n.Key{i18n.KubernetesLogCreate, i18n.KubernetesLogPatch} {
		lines = nil
		req := request(t, &lines)
		if _, err := c.Deploy(t.Context(), req); err != nil {
			t.Fatal(err)
		}
		if len(lines) != 1 || lines[0].Key != want {
			t.Errorf("deployment %d logged %+v", i, lines)
		}
		var s struct {
			Type string            `json:"type"`
			Data map[string]string `json:"data"`
		}
		if err := c.call(t.Context(), c.token, http.MethodGet, nil, "", nil, &s, "api", "v1", "namespaces", c.namespace, "secrets", c.secret); err != nil {
			t.Fatal(err)
		}
		crt, _ := base64.StdEncoding.DecodeString(s.Data["tls.crt"])
		key, _ := base64.StdEncoding.DecodeString(s.Data["tls.key"])
		if b, err := cryptox.ParseBundle(crt, key); s.Type != tlsType || err != nil || !b.Leaf.Equal(req.Chain[0]) || len(b.Chain) != 1 {
			t.Errorf("deployment %d: the Secret holds %s %v", i, s.Type, err)
		}
	}
	if msg, err := c.Test(t.Context()); err != nil || msg.Key != i18n.KubernetesTestFound {
		t.Errorf("test: %+v %v", msg, err)
	}

	// Elsewhere the token may do nothing.
	config["namespace"] = "default"
	_, err = open().Test(t.Context())
	var pr *contracts.Problem
	if !errors.As(err, &pr) || pr.Key != i18n.KubernetesErrorVerbs || pr.Params["verbs"] != "get, create, patch" {
		t.Errorf("default: %v", err)
	}
	_, err = open().Deploy(t.Context(), request(t, &lines))
	if !errors.As(err, &pr) || pr.Key != i18n.KubernetesErrorForbidden {
		t.Errorf("deploying to default: %v", err)
	}
}
