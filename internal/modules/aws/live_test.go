package aws

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// TestLive deploys to ACM, or to an emulator of it such as moto. CI does
// not run it: JEC_TEST_ACM is the endpoint, such as http://127.0.0.1:5000/
// for moto, which takes any keys.
func TestLive(t *testing.T) {
	endpoint := os.Getenv("JEC_TEST_ACM")
	if endpoint == "" {
		t.Skip("JEC_TEST_ACM is not set")
	}
	open := func(arn string) *acm {
		p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"region": "us-east-1", "certificate_arn": arn},
			Secrets: map[string]string{"access_key_id": "testing", "secret_access_key": "testing"}})
		if err != nil {
			t.Fatal(err)
		}
		a := p.(*acm)
		a.endpoint, a.client = endpoint, &http.Client{Timeout: time.Minute}
		return a
	}
	a := open("")
	if msg, err := a.Test(t.Context()); err != nil || msg.Key != i18n.AwsTestKeys {
		t.Fatalf("test: %+v %v", msg, err)
	}
	var lines []contracts.LogLine
	first := request(t, &lines)
	res, err := a.Deploy(t.Context(), first)
	if err != nil || res.Ref == "" {
		t.Fatalf("first: %+v %v", res, err)
	}
	served := func(want contracts.DeployRequest) {
		t.Helper()
		var out struct {
			Certificate, CertificateChain string
		}
		if err := a.call(t.Context(), "GetCertificate", map[string]string{"CertificateArn": res.Ref}, &out); err != nil {
			t.Fatal(err)
		}
		leaf, err := cryptox.ParseCertificates([]byte(out.Certificate))
		if err != nil || len(leaf) != 1 || !leaf[0].Equal(want.Chain[0]) {
			t.Errorf("ACM holds %s: %v", out.Certificate, err)
		}
	}
	served(first)

	second := request(t, &lines)
	second.Previous = []string{res.Ref}
	again, err := a.Deploy(t.Context(), second)
	if err != nil || again.Ref != res.Ref {
		t.Fatalf("second: %+v %v, then %q", again, err, res.Ref)
	}
	served(second)
	if msg, err := open(res.Ref).Test(t.Context()); err != nil || msg.Key != i18n.AwsTestFound || msg.Params["domain"] != "example.com" {
		t.Errorf("test with the ARN: %+v %v", msg, err)
	}
}
