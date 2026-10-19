package aws

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// fakeCloudFront answers as ACM in us-east-1 and as CloudFront do, for a
// distribution served CloudFront's own certificate at first.
type fakeCloudFront struct {
	t      *testing.T
	mu     sync.Mutex
	config string
	etag   int
	puts   []string
	arns   []string // imported, in order
}

const startConfig = `<?xml version="1.0" encoding="UTF-8"?>
<DistributionConfig xmlns="http://cloudfront.amazonaws.com/doc/2020-05-31/"><CallerReference>x</CallerReference>` +
	`<Aliases><Quantity>1</Quantity><Items><CNAME>example.com</CNAME></Items></Aliases>` +
	`<ViewerCertificate><CloudFrontDefaultCertificate>true</CloudFrontDefaultCertificate><MinimumProtocolVersion>TLSv1</MinimumProtocolVersion>` +
	`<CertificateSource>cloudfront</CertificateSource></ViewerCertificate><Enabled>true</Enabled></DistributionConfig>`

func (f *fakeCloudFront) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	auth := r.Header.Get("Authorization")
	switch {
	case strings.HasPrefix(r.URL.Path, "/acm/"):
		if !strings.Contains(auth, "/us-east-1/acm/aws4_request") {
			f.t.Errorf("ACM signed %q", auth)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "CertificateManager.") {
		case "DescribeCertificate":
			_, _ = io.WriteString(w, `{"Certificate": {"CertificateArn": "`+body["CertificateArn"].(string)+`", "Type": "IMPORTED"}}`)
		case "ImportCertificate":
			arn, _ := body["CertificateArn"].(string)
			if arn == "" {
				arn = "arn:aws:acm:us-east-1:111:certificate/new"
			}
			f.arns = append(f.arns, arn)
			_, _ = io.WriteString(w, `{"CertificateArn": "`+arn+`"}`)
		}
	case r.URL.Path == "/2020-05-31/distribution/E2QWRUHAPOMQZL/config":
		if !strings.Contains(auth, "/us-east-1/cloudfront/aws4_request") {
			f.t.Errorf("CloudFront signed %q", auth)
		}
		if r.Method == http.MethodPut {
			if r.Header.Get("If-Match") != "E"+string(rune('0'+f.etag)) {
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = io.WriteString(w, `<ErrorResponse><Error><Code>PreconditionFailed</Code><Message>stale</Message></Error></ErrorResponse>`)
				return
			}
			body, _ := io.ReadAll(r.Body)
			f.config = string(body)
			f.puts = append(f.puts, f.config)
			f.etag++
		}
		w.Header().Set("ETag", "E"+string(rune('0'+f.etag)))
		_, _ = io.WriteString(w, f.config)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<ErrorResponse><Error><Code>NoSuchDistribution</Code><Message>none</Message></Error></ErrorResponse>`)
	}
}

func TestCloudFront(t *testing.T) {
	f := &fakeCloudFront{t: t, config: startConfig}
	srv := httptest.NewServer(f)
	defer srv.Close()
	open := func(id string) *cloudFront {
		t.Helper()
		p, err := cloudFrontFactory(t.Context(), providers.Instance{
			Config:  map[string]string{"distribution": id, "access_key_id": "AKIDEXAMPLE"},
			Secrets: map[string]string{"secret_access_key": "secret"},
		})
		if err != nil {
			t.Fatal(err)
		}
		c := p.(*cloudFront)
		c.acm.endpoint, c.endpoint = srv.URL+"/acm/", srv.URL+"/2020-05-31/distribution/"
		c.acm.client = srv.Client()
		c.acm.now = func() time.Time { return time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC) }
		return c
	}
	if _, err := cloudFrontFactory(t.Context(), providers.Instance{Config: map[string]string{"distribution": "not one"}}); err == nil {
		t.Error("took a malformed distribution ID")
	}
	c := open("E2QWRUHAPOMQZL")
	msg, err := c.Test(t.Context())
	if err != nil || msg.Params["aliases"] != "example.com" || msg.Params["certificate"] != "CloudFront" {
		t.Fatalf("test %+v: %v", msg, err)
	}

	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	now := time.Now()
	req := contracts.DeployRequest{CertificateID: 3, Key: key, Chain: []*x509.Certificate{ca.Leaf(t, key, now, now.Add(90*24*time.Hour), "example.com"), ca.Intermediate}}
	res, err := c.Deploy(t.Context(), req)
	if err != nil || res.Ref != "arn:aws:acm:us-east-1:111:certificate/new" {
		t.Fatalf("first deployment %+v: %v", res, err)
	}
	if len(f.puts) != 1 || !strings.Contains(f.puts[0], "<ACMCertificateArn>arn:aws:acm:us-east-1:111:certificate/new</ACMCertificateArn>") ||
		!strings.Contains(f.puts[0], "<SSLSupportMethod>sni-only</SSLSupportMethod><MinimumProtocolVersion>TLSv1.2_2021</MinimumProtocolVersion>") ||
		strings.Contains(f.puts[0], "CloudFrontDefaultCertificate>true") || !strings.Contains(f.puts[0], "<Enabled>true</Enabled>") {
		t.Errorf("put %v", f.puts)
	}

	// The next version goes over the same ARN, which the distribution
	// serves already: nothing to change there.
	req.Previous = []string{res.Ref}
	if res, err := c.Deploy(t.Context(), req); err != nil || res.Ref != "arn:aws:acm:us-east-1:111:certificate/new" {
		t.Fatalf("second deployment %+v: %v", res, err)
	}
	if len(f.puts) != 1 || len(f.arns) != 2 || f.arns[1] != "arn:aws:acm:us-east-1:111:certificate/new" {
		t.Errorf("after the second: puts %d, imports %v", len(f.puts), f.arns)
	}

	if _, err := open("E000NOTHERE").Test(t.Context()); err == nil || !strings.Contains(err.Error(), "NoSuchDistribution") {
		t.Errorf("a distribution not there: %v", err)
	}
}
