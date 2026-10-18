package aliyun

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// The canonical request of the example in OSS's documentation of its
// signature version 4 hashes as the documentation says.
func TestCanonicalOSS(t *testing.T) {
	req, err := http.NewRequest(http.MethodPut, "https://examplebucket.oss-cn-hangzhou.aliyuncs.com/exampleobject", nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"Content-Disposition": "attachment", "Content-Length": "3", "Content-MD5": "ICy5YqxZB1uWSwcVLSNLcA==", "Content-Type": "text/plain",
		"x-oss-content-sha256": unsignedPayload, "x-oss-date": "20250411T064124Z",
	} {
		req.Header.Set(k, v)
	}
	sum := sha256.Sum256([]byte(canonicalOSS(req, "/examplebucket/exampleobject", []string{"content-length", "content-disposition"})))
	if got := hex.EncodeToString(sum[:]); got != "c46d96390bdbc2d739ac9363293ae9d710b14e48081fcb22cd8ad54b63136eca" {
		t.Errorf("hash %s of\n%s", got, canonicalOSS(req, "/examplebucket/exampleobject", []string{"content-length", "content-disposition"}))
	}
	// The sub-resources of a bucket: names alone, sorted.
	req, _ = http.NewRequest(http.MethodPost, "https://b.oss-cn-hangzhou.aliyuncs.com/?"+ossQuery(map[string]string{"comp": "add", "cname": ""}), nil)
	if c := canonicalOSS(req, "/b/", nil); !strings.HasPrefix(c, "POST\n/b/\ncname&comp=add\n") {
		t.Errorf("canonical request\n%s", c)
	}
}

func TestSignOSS(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://b.oss-cn-shanghai.aliyuncs.com/?cname", nil)
	signOSS(req, "/b/", "cn-shanghai", "LTAI-id", "s3cret", time.Date(2026, 10, 19, 2, 3, 4, 0, time.UTC))
	auth := regexp.MustCompile(`^OSS4-HMAC-SHA256 Credential=LTAI-id/20261019/cn-shanghai/oss/aliyun_v4_request, Signature=[0-9a-f]{64}$`)
	if !auth.MatchString(req.Header.Get("Authorization")) || req.Header.Get("x-oss-date") != "20261019T020304Z" || req.Header.Get("x-oss-content-sha256") != unsignedPayload {
		t.Errorf("headers %v", req.Header)
	}
}

// fakeOSS is a bucket, b, with custom domains and the CAS identifier of
// the certificate each has.
type fakeOSS struct {
	mu      sync.Mutex
	domains map[string]string
	puts    []putCname
	status  int    // answers every request with this, unless 0
	code    string // the code of such an answer
}

func (f *fakeOSS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "OSS4-HMAC-SHA256 Credential=LTAI-id/") || r.Header.Get("x-oss-date") == "" {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<Error><Code>SignatureDoesNotMatch</Code></Error>`)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>`+f.code+`</Code><Message>refused</Message></Error>`)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.RawQuery == "cname":
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListCnameResult><Bucket>b</Bucket>`)
		for d, cert := range f.domains {
			b.WriteString(`<Cname><Domain>` + d + `</Domain><Status>Enabled</Status>`)
			if cert != "" {
				b.WriteString(`<Certificate><Type>CAS</Type><CertId>` + cert + `</CertId></Certificate>`)
			}
			b.WriteString(`</Cname>`)
		}
		b.WriteString(`</ListCnameResult>`)
		_, _ = io.WriteString(w, b.String())
	case r.Method == http.MethodPost && r.URL.RawQuery == "cname&comp=add":
		body, _ := io.ReadAll(r.Body)
		sum := md5.Sum(body)
		var p putCname
		if err := xml.Unmarshal(body, &p); err != nil || r.Header.Get("Content-MD5") != base64.StdEncoding.EncodeToString(sum[:]) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.puts = append(f.puts, p)
		f.domains[p.Cname.Domain] = p.Cname.CertificateConfiguration.CertID
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func ossFactory(m *Module) providers.Factory { return m.ossFactory }

func openOSS(t *testing.T, f *fakeOSS, cas *fakeCAS, config map[string]string) *bucket {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg := map[string]string{"region": "cn-shanghai", "bucket": "img-bucket", "domain": "img.example.com", "site": casChina}
	for k, v := range config {
		cfg[k] = v
	}
	b := openRPC(t, cas.fakeRPC, ossFactory, "cas.aliyuncs.com", cfg).(*bucket)
	if b.base != "https://img-bucket.oss-cn-shanghai.aliyuncs.com" {
		t.Errorf("the bucket is at %s", b.base)
	}
	b.http, b.base = srv.Client(), srv.URL
	return b
}

func TestOSS(t *testing.T) {
	f := &fakeOSS{domains: map[string]string{"img.example.com": "900-cn-hangzhou", "other.example.com": ""}}
	cas := newFakeCAS()
	b := openOSS(t, f, cas, nil)
	req := request(t, "img.example.com")
	req.Previous = []string{"900"}
	res, err := b.Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "1001" || len(res.Removed) != 1 || len(f.puts) != 1 {
		t.Fatalf("result %+v, %d puts", res, len(f.puts))
	}
	c := f.puts[0].Cname
	if c.Domain != "img.example.com" || c.CertificateConfiguration.CertID != "1001-cn-hangzhou" || !c.CertificateConfiguration.Force ||
		!strings.Contains(c.CertificateConfiguration.Certificate, "BEGIN CERTIFICATE") || !strings.Contains(c.CertificateConfiguration.PrivateKey, "PRIVATE KEY") {
		t.Errorf("put %+v", c)
	}
	// Again: the domain has it.
	if res, err := b.Deploy(t.Context(), req); err != nil || res.Ref != "1001" || len(f.puts) != 1 {
		t.Errorf("again: %+v, %v, %d puts", res, err, len(f.puts))
	}
	msg, err := b.Test(t.Context())
	if err != nil || msg.Key != i18n.AliyunTestOssOk || msg.Params["domain"] != "img.example.com" {
		t.Errorf("test: %+v, %v", msg, err)
	}
}

func TestOSSErrors(t *testing.T) {
	var p *contracts.Problem
	f := &fakeOSS{domains: map[string]string{"other.example.com": ""}}
	b := openOSS(t, f, newFakeCAS(), nil)
	if _, err := b.Deploy(t.Context(), request(t, "img.example.com")); !errors.As(err, &p) || p.Key != i18n.AliyunErrorCname || !retry.IsPermanent(err) {
		t.Errorf("a domain the bucket lacks: %v", err)
	}
	for code, key := range map[string]i18n.Key{"NoSuchBucket": i18n.AliyunErrorBucket, "AccessDenied": i18n.AliyunErrorAuth, "InternalError": i18n.AliyunErrorApi} {
		f.code, f.status = code, http.StatusNotFound
		if code == "AccessDenied" {
			f.status = http.StatusForbidden
		}
		if code == "InternalError" {
			f.status = http.StatusInternalServerError
		}
		_, err := b.Test(t.Context())
		if !errors.As(err, &p) || p.Key != key || retry.IsPermanent(err) != (code != "InternalError") {
			t.Errorf("%s: %v", code, err)
		}
	}
	_, err := (&Module{rpc: func(string, string, string, string) (rpc, error) { return newFakeCAS(), nil }}).ossFactory(t.Context(),
		providers.Instance{Config: map[string]string{"region": "cn-hangzhou", "bucket": "Bad_Bucket", "domain": "a.example.com"}})
	if !errors.As(err, &p) || p.Key != i18n.AliyunErrorOssName {
		t.Errorf("a bucket name OSS would not have: %v", err)
	}
}
