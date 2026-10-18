package aws

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// fakeACM answers as ACM's JSON API does, for what JEC uses.
type fakeACM struct {
	t       *testing.T
	mu      sync.Mutex
	certs   map[string]map[string]any // by ARN: the last import, and Type
	imports []map[string]any
	// fail, when it gives a status, answers the action instead.
	fail func(action string) (int, string)
}

func (f *fakeACM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") || !strings.Contains(auth, "/eu-central-1/acm/aws4_request") ||
		r.Header.Get("X-Amz-Date") == "" || r.Header.Get("Content-Type") != "application/x-amz-json-1.1" {
		f.t.Errorf("signed %q, %v", auth, r.Header)
	}
	action := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "CertificateManager.")
	if f.fail != nil {
		if status, body := f.fail(action); status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
			return
		}
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	notFound := func(arn any) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"__type": "ResourceNotFoundException", "message": "Could not find certificate with %v."}`, arn)
	}
	switch action {
	case "ListCertificates":
		_, _ = io.WriteString(w, `{"CertificateSummaryList": []}`)
	case "DescribeCertificate":
		c := f.certs[fmt.Sprint(body["CertificateArn"])]
		if c == nil {
			notFound(body["CertificateArn"])
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Certificate": map[string]any{
			"CertificateArn": body["CertificateArn"], "DomainName": "example.com", "Status": "ISSUED", "Type": c["Type"], "InUseBy": []string{"arn:cloudfront"},
		}})
	case "ImportCertificate":
		f.imports = append(f.imports, body)
		arn, ok := body["CertificateArn"].(string)
		if !ok {
			arn = fmt.Sprintf("arn:aws:acm:eu-central-1:123456789012:certificate/%d", len(f.imports))
		} else if f.certs[arn] == nil {
			notFound(arn)
			return
		}
		body["Type"] = "IMPORTED"
		f.certs[arn] = body
		_, _ = fmt.Fprintf(w, `{"CertificateArn": %q}`, arn)
	default:
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"__type": "com.amazon.coral.service#UnknownOperationException"}`)
	}
}

func serve(t *testing.T, f *fakeACM, config map[string]string) *acm {
	t.Helper()
	f.t = t
	if f.certs == nil {
		f.certs = map[string]map[string]any{}
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg := map[string]string{"region": "eu-central-1"}
	for k, v := range config {
		cfg[k] = v
	}
	p, err := factory(t.Context(), providers.Instance{Config: cfg, Secrets: map[string]string{"access_key_id": "AKIDEXAMPLE", "secret_access_key": "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	a := p.(*acm)
	a.client, a.endpoint = srv.Client(), srv.URL+"/"
	return a
}

// request makes a certificate for example.com, logging into lines.
func request(t *testing.T, lines *[]contracts.LogLine) contracts.DeployRequest {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "example.com")
	return contracts.DeployRequest{CertificateID: 7, Domains: []string{"example.com"}, Chain: []*x509.Certificate{leaf, ca.Intermediate}, Key: key,
		Log: func(l contracts.LogLine) { *lines = append(*lines, l) }}
}

// checkImport checks that in holds the certificate of req, as Base64 blobs.
func checkImport(t *testing.T, in map[string]any, req contracts.DeployRequest) {
	t.Helper()
	blob := func(k string) []byte {
		b, err := base64.StdEncoding.DecodeString(fmt.Sprint(in[k]))
		if err != nil {
			t.Errorf("%s: %v", k, err)
		}
		return b
	}
	b, err := cryptox.ParseBundle(append(blob("Certificate"), blob("CertificateChain")...), blob("PrivateKey"))
	if err != nil || !b.Leaf.Equal(req.Chain[0]) || len(b.Chain) != 1 {
		t.Errorf("imported %v: %v", in, err)
	}
}

func TestDeploy(t *testing.T) {
	f := &fakeACM{}
	a := serve(t, f, nil)
	var lines []contracts.LogLine
	req := request(t, &lines)
	res, err := a.Deploy(t.Context(), req)
	if err != nil || res.Ref != "arn:aws:acm:eu-central-1:123456789012:certificate/1" {
		t.Fatalf("first: %+v %v", res, err)
	}
	checkImport(t, f.imports[0], req)
	if fmt.Sprint(f.imports[0]["Tags"]) != "[map[Key:Name Value:jec-7] map[Key:managed-by Value:jec]]" || lines[0].Key != i18n.AwsLogImport {
		t.Errorf("tags %v, logged %+v", f.imports[0]["Tags"], lines)
	}

	// The next version goes over it, with no tags, which ACM keeps.
	lines = nil
	next := request(t, &lines)
	next.Previous = []string{"", res.Ref}
	if res, err = a.Deploy(t.Context(), next); err != nil || res.Ref != "arn:aws:acm:eu-central-1:123456789012:certificate/1" || len(f.certs) != 1 {
		t.Fatalf("second: %+v %v, %d certificates", res, err, len(f.certs))
	}
	checkImport(t, f.imports[1], next)
	if f.imports[1]["Tags"] != nil || f.imports[1]["CertificateArn"] != res.Ref || lines[0].Key != i18n.AwsLogReimport {
		t.Errorf("reimported %v, logged %+v", f.imports[1], lines)
	}

	// Gone meanwhile: a new one comes.
	lines = nil
	next.Previous = []string{"arn:aws:acm:eu-central-1:123456789012:certificate/9"}
	if res, err = a.Deploy(t.Context(), next); err != nil || res.Ref != "arn:aws:acm:eu-central-1:123456789012:certificate/3" {
		t.Fatalf("gone: %+v %v", res, err)
	}
	if lines[0].Key != i18n.AwsLogGone || lines[1].Key != i18n.AwsLogImport {
		t.Errorf("logged %+v", lines)
	}
}

func TestDeploySet(t *testing.T) {
	const arn = "arn:aws:acm:eu-central-1:123456789012:certificate/mine"
	f := &fakeACM{certs: map[string]map[string]any{arn: {"Type": "IMPORTED"}, "arn:issued": {"Type": "AMAZON_ISSUED"}}}
	var lines []contracts.LogLine
	req := request(t, &lines)
	res, err := serve(t, f, map[string]string{"certificate_arn": arn}).Deploy(t.Context(), req)
	if err != nil || res.Ref != "" || f.imports[0]["CertificateArn"] != arn {
		t.Fatalf("%+v %v %v", res, err, f.imports)
	}
	var pr *contracts.Problem
	_, err = serve(t, f, map[string]string{"certificate_arn": "arn:issued"}).Deploy(t.Context(), req)
	if !errors.As(err, &pr) || pr.Key != i18n.AwsErrorNotImported || !retry.IsPermanent(err) {
		t.Errorf("one ACM issued: %v", err)
	}
	_, err = serve(t, f, map[string]string{"certificate_arn": "arn:gone"}).Deploy(t.Context(), req)
	if !errors.As(err, &pr) || pr.Key != i18n.AwsErrorNoCertificate || !retry.IsPermanent(err) {
		t.Errorf("gone: %v", err)
	}
	msg, err := serve(t, f, map[string]string{"certificate_arn": arn}).Test(t.Context())
	if err != nil || msg.Key != i18n.AwsTestFound || msg.Params["count"] != 1 {
		t.Errorf("test: %+v %v", msg, err)
	}
	msg, err = serve(t, f, nil).Test(t.Context())
	if err != nil || msg.Key != i18n.AwsTestKeys || msg.Params["region"] != "eu-central-1" {
		t.Errorf("test without an ARN: %+v %v", msg, err)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		status    int
		body      string
		key       i18n.Key
		permanent bool
	}{
		{400, `{"__type": "UnrecognizedClientException", "message": "The security token included in the request is invalid."}`, i18n.AwsErrorKeys, true},
		{400, `{"__type": "AccessDeniedException", "Message": "User is not authorized to perform: acm:ImportCertificate"}`, i18n.AwsErrorDenied, true},
		{400, `{"__type": "com.amazonaws.acm#ValidationException", "message": "The private key is not supported."}`, i18n.AwsErrorApi, true},
		{400, `{"__type": "ThrottlingException", "message": "Rate exceeded"}`, i18n.AwsErrorApi, false},
		{500, `{"__type": "InternalFailure"}`, i18n.AwsErrorApi, false},
		{502, `<html>bad gateway</html>`, i18n.AwsErrorStatus, false},
	} {
		f := &fakeACM{fail: func(action string) (int, string) {
			if action == "ImportCertificate" {
				return c.status, c.body
			}
			return 0, ""
		}}
		var lines []contracts.LogLine
		_, err := serve(t, f, nil).Deploy(t.Context(), request(t, &lines))
		var pr *contracts.Problem
		if !errors.As(err, &pr) || pr.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
	}
	for _, r := range []string{"Frankfurt", "us-east-1a", ""} {
		_, err := factory(t.Context(), providers.Instance{Config: map[string]string{"region": r}})
		if r == "" {
			if err != nil {
				t.Errorf("no region, the default: %v", err)
			}
			continue
		}
		if err == nil || !retry.IsPermanent(err) {
			t.Errorf("region %q: %v", r, err)
		}
	}
	p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"region": "cn-north-1"}})
	if err != nil || p.(*acm).endpoint != "https://acm.cn-north-1.amazonaws.com.cn/" {
		t.Errorf("China: %v", err)
	}
}
