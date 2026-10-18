package aliyun

import (
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// fakeOrders is the Certificate Management Service of an account, with
// the orders applied for and a test CA that issues them.
type fakeOrders struct {
	*fakeRPC
	t       *testing.T
	ca      *cryptoxtest.CA
	mu      sync.Mutex
	orders  map[string]*fakeOrder
	next    int
	deleted []string
}

type fakeOrder struct {
	state    string // as DescribeCertificateState answers its Type
	validate string // DNS or FILE
	csr      *x509.CertificateRequest
	leaf     *x509.Certificate
}

func newFakeOrders(t *testing.T) *fakeOrders {
	f := &fakeOrders{t: t, ca: cryptoxtest.NewCA(t), orders: map[string]*fakeOrder{}, next: 100}
	f.fakeRPC = &fakeRPC{answer: f.respond}
	return f
}

func (f *fakeOrders) respond(c rpcCall) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch c.action {
	case "CreateCertificateForPackageRequest":
		block, _ := pem.Decode([]byte(c.form["Csr"]))
		if block == nil {
			return nil, apiError("InvalidCsr", 400)
		}
		csr, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil {
			return nil, apiError("InvalidCsr", 400)
		}
		f.next++
		f.orders[strconv.Itoa(f.next)] = &fakeOrder{state: "domain_verify", validate: c.query["ValidateType"], csr: csr}
		return map[string]any{"OrderId": f.next}, nil
	case "DescribeCertificateState":
		id := c.query["OrderId"]
		o, ok := f.orders[id]
		if !ok {
			return nil, apiError("OrderNotFound", 404)
		}
		switch {
		case o.state == "domain_verify" && o.validate == "DNS":
			return map[string]any{"Type": o.state, "ValidateType": "DNS", "RecordDomain": "_dnsauth", "RecordType": "TXT", "RecordValue": "v-" + id}, nil
		case o.state == "domain_verify":
			return map[string]any{"Type": o.state, "ValidateType": "FILE", "Domain": "www.example.com",
				"Uri": "/.well-known/pki-validation/fileauth.txt", "Content": "token-" + id}, nil
		case o.state == "certificate":
			return map[string]any{"Type": o.state, "CertId": "77", "Certificate": cryptoxtest.CertPEM(o.leaf, f.ca.Intermediate)}, nil
		}
		return map[string]any{"Type": o.state}, nil
	case "DeleteCertificateRequest":
		f.deleted = append(f.deleted, c.query["OrderId"])
		delete(f.orders, c.query["OrderId"])
		return map[string]any{}, nil
	case "DescribePackageState":
		return map[string]any{"ProductCode": c.query["ProductCode"], "TotalCount": 20, "UsedCount": 3, "IssuedCount": 2}, nil
	}
	return nil, errors.New("unexpected " + c.action)
}

// set moves order id to state; issuing signs the key of its CSR, or key
// when not nil.
func (f *fakeOrders) set(id, state string, key crypto.Signer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.orders[id]
	o.state = state
	if state == "certificate" {
		if key == nil {
			key = publicKey{o.csr.PublicKey}
		}
		now := time.Now()
		o.leaf = f.ca.Leaf(f.t, key, now.Add(-time.Hour), now.AddDate(0, 3, 0), o.csr.DNSNames...)
	}
}

// publicKey signs nothing: it lends a CSR's key to the test CA, which
// needs only its public half.
type publicKey struct{ pub crypto.PublicKey }

func (k publicKey) Public() crypto.PublicKey { return k.pub }
func (publicKey) Sign(io.Reader, []byte, crypto.SignerOpts) ([]byte, error) {
	panic("a public key signs nothing")
}

// zone is a solver of DNS records and the authoritative servers behind
// it: what it adds, lookups see, unless they lag.
type zone struct {
	mu      sync.Mutex
	records map[contracts.Record]string
	removed []string
	lag     bool
	fail    error
}

func (z *zone) AddRecord(_ context.Context, r contracts.Record) (string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.fail != nil {
		return "", z.fail
	}
	ref := "rec-" + strconv.Itoa(len(z.records)+len(z.removed)+1)
	z.records[r] = ref
	return ref, nil
}

func (z *zone) RemoveRecord(_ context.Context, r contracts.Record, ref string) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.records[r] != ref {
		return errors.New("no such record")
	}
	delete(z.records, r)
	z.removed = append(z.removed, ref)
	return nil
}

func (*zone) Close() error { return nil }

func (z *zone) visible(_ context.Context, name, typ, value string) (bool, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	_, ok := z.records[contracts.Record{Name: name, Type: typ, Value: value}]
	return ok && !z.lag, nil
}

// site is a solver over HTTP and the web server behind it: what it puts,
// fetches see, unless it is broken.
type site struct {
	mu      sync.Mutex
	files   map[string][]byte
	removed []string
	broken  bool
}

func (s *site) PutFile(_ context.Context, f contracts.File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[f.Domain+f.Path] = f.Content
	return nil
}

func (s *site) RemoveFile(_ context.Context, f contracts.File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, f.Domain+f.Path)
	s.removed = append(s.removed, f.Domain+f.Path)
	return nil
}

func (*site) Close() error { return nil }

func (s *site) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	data, ok := s.files[r.URL.Host+r.URL.Path]
	broken := s.broken
	s.mu.Unlock()
	status := http.StatusOK
	if !ok || broken || r.URL.Scheme != "http" {
		status, data = http.StatusNotFound, []byte("not found")
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}, Request: r}, nil
}

// applyFixture is the source against fake orders, with a zone and a
// website for its validations.
type applyFixture struct {
	t      *testing.T
	orders *fakeOrders
	zone   *zone
	site   *site
	a      *applier
	key    crypto.Signer
	logged []i18n.Key
}

func newApplyFixture(t *testing.T, config map[string]string) *applyFixture {
	t.Helper()
	f := &applyFixture{t: t, orders: newFakeOrders(t), zone: &zone{records: map[contracts.Record]string{}}, site: &site{files: map[string][]byte{}}, key: cryptoxtest.NewKey(t)}
	m := &Module{
		rpc: func(endpoint, version, id, secret string) (rpc, error) {
			if endpoint != "cas.aliyuncs.com" || version != casVersion || id != "LTAI-id" || secret != "s3cret" {
				t.Errorf("opened %s %s with %s %s", endpoint, version, id, secret)
			}
			return f.orders.fakeRPC, nil
		},
		web: &http.Client{Transport: f.site}, visible: f.zone.visible,
	}
	p, err := m.applyFactory(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"access_key_id": "LTAI-id", "access_key_secret": "s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	f.a = p.(*applier)
	return f
}

// attempt runs an attempt for www.example.com with the state of the last
// one, through the zone, or the website with files set.
func (f *applyFixture) attempt(state []byte, files bool) ([]*x509.Certificate, *contracts.Wait, error) {
	f.t.Helper()
	req := contracts.IssueRequest{Domains: []string{"www.example.com"}, Key: f.key, State: state,
		Log: func(l contracts.LogLine) { f.logged = append(f.logged, l.Key) }}
	if files {
		req.Files = f.site
	} else {
		req.Records = f.zone
	}
	chain, err := f.a.Issue(f.t.Context(), req)
	var w *contracts.Wait
	if errors.As(err, &w) {
		if w.After != applyPoll {
			f.t.Errorf("waits %v", w.After)
		}
		return nil, w, nil
	}
	return chain, nil, err
}

func (f *applyFixture) count(k i18n.Key) int {
	n := 0
	for _, l := range f.logged {
		if l == k {
			n++
		}
	}
	return n
}

func TestApplyDNS(t *testing.T) {
	f := newApplyFixture(t, map[string]string{})
	f.zone.lag = true

	// Apply with the CSR of the key, and add the record, which the
	// servers do not answer yet.
	_, w, err := f.attempt(nil, false)
	if err != nil || w == nil {
		t.Fatalf("first attempt: %v", err)
	}
	create := f.orders.called("CreateCertificateForPackageRequest")
	if len(create) != 1 || create[0].query["ProductCode"] != freeProduct || create[0].query["ValidateType"] != "DNS" ||
		create[0].query["Domain"] != "www.example.com" || create[0].query["Csr"] != "" {
		t.Fatalf("applied %+v", create)
	}
	o := f.orders.orders["101"]
	if o.csr.Subject.CommonName != "www.example.com" || !f.key.Public().(interface{ Equal(crypto.PublicKey) bool }).Equal(o.csr.PublicKey) {
		t.Errorf("CSR %+v", o.csr.Subject)
	}
	want := contracts.Record{Name: "_dnsauth.www.example.com", Type: "TXT", Value: "v-101"}
	if f.zone.records[want] == "" {
		t.Fatalf("records %v", f.zone.records)
	}

	// Once the servers answer it, the CA looks for it.
	if _, w, err = f.attempt(w.State, false); err != nil || w == nil {
		t.Fatalf("still lagging: %v", err)
	}
	f.zone.lag = false
	if _, w, err = f.attempt(w.State, false); err != nil || w == nil {
		t.Fatalf("answered: %v", err)
	}
	f.orders.set("101", "process", nil)
	if _, w, err = f.attempt(w.State, false); err != nil || w == nil {
		t.Fatalf("in review: %v", err)
	}
	if f.count(i18n.AliyunLogApplyRecord) != 1 || f.count(i18n.AliyunLogApplyRecordWait) != 1 || f.count(i18n.AliyunLogApplyValidate) != 1 || f.count(i18n.AliyunLogApplyReview) != 1 {
		t.Errorf("log %v", f.logged)
	}

	// Issued: the certificate of the key, and the record goes.
	f.orders.set("101", "certificate", nil)
	chain, _, err := f.attempt(w.State, false)
	if err != nil || len(chain) != 2 || !chain[1].Equal(f.orders.ca.Intermediate) {
		t.Fatalf("download: %v %v", chain, err)
	}
	if len(f.zone.records) != 0 || !slices.Equal(f.zone.removed, []string{"rec-1"}) || f.count(i18n.AliyunLogApplyIssued) != 1 {
		t.Errorf("records %v, removed %v, log %v", f.zone.records, f.zone.removed, f.logged)
	}
	if n := len(f.orders.called("CreateCertificateForPackageRequest")); n != 1 {
		t.Errorf("applied %d times", n)
	}
}

func TestApplyFile(t *testing.T) {
	f := newApplyFixture(t, map[string]string{"product": "geotrust-dv-1-starter", "username": "Tom", "phone": "13800000000", "email": "tom@example.com"})
	_, w, err := f.attempt(nil, true)
	if err != nil || w == nil {
		t.Fatalf("first attempt: %v", err)
	}
	create := f.orders.called("CreateCertificateForPackageRequest")[0]
	if create.query["ProductCode"] != "geotrust-dv-1-starter" || create.query["ValidateType"] != "FILE" ||
		create.query["Username"] != "Tom" || create.query["Phone"] != "13800000000" || create.query["Email"] != "tom@example.com" {
		t.Errorf("applied %+v", create.query)
	}
	if got := string(f.site.files["www.example.com/.well-known/pki-validation/fileauth.txt"]); got != "token-101" {
		t.Errorf("file %q", got)
	}
	if f.count(i18n.AliyunLogApplyNotServed) != 0 || f.count(i18n.AliyunLogApplyValidate) != 1 {
		t.Errorf("log %v", f.logged)
	}
	f.orders.set("101", "certificate", nil)
	if chain, _, err := f.attempt(w.State, true); err != nil || len(chain) != 2 {
		t.Fatalf("download: %v", err)
	}
	if len(f.site.files) != 0 || len(f.site.removed) != 1 {
		t.Errorf("files %v", f.site.files)
	}

	// A website that does not serve the file gets a warning.
	f = newApplyFixture(t, map[string]string{})
	f.site.broken = true
	if _, _, err := f.attempt(nil, true); err != nil || f.count(i18n.AliyunLogApplyNotServed) != 1 {
		t.Errorf("not served: %v, log %v", err, f.logged)
	}
}

func TestApplyRestarts(t *testing.T) {
	// The CA refuses the order: it is deleted, its record removed, and the
	// next issuance applies anew.
	f := newApplyFixture(t, map[string]string{})
	_, w, err := f.attempt(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	f.orders.set("101", "verify_fail", nil)
	_, _, err = f.attempt(w.State, false)
	if !contracts.IsRestart(err) || !retry.IsPermanent(err) || problemKey(err) != i18n.AliyunErrorApplyRefused {
		t.Errorf("refused: %v", err)
	}
	if !slices.Equal(f.orders.deleted, []string{"101"}) || len(f.zone.records) != 0 {
		t.Errorf("deleted %v, records %v", f.orders.deleted, f.zone.records)
	}

	// The order is gone: apply anew, retrying.
	_, _, err = f.attempt(w.State, false)
	if !contracts.IsRestart(err) || retry.IsPermanent(err) || problemKey(err) != i18n.AliyunErrorApplyGone {
		t.Errorf("gone: %v", err)
	}

	// The certificate's solver no longer does the validation the order
	// asks for.
	_, w, err = f.attempt(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.attempt(w.State, true)
	if !contracts.IsRestart(err) || !retry.IsPermanent(err) || problemKey(err) != i18n.AliyunErrorApplyMethod {
		t.Errorf("another solver: %v", err)
	}
}

func TestApplyFailures(t *testing.T) {
	f := newApplyFixture(t, map[string]string{})
	if _, err := f.a.Issue(t.Context(), contracts.IssueRequest{Domains: []string{"www.example.com"}, Key: f.key}); err == nil || !retry.IsPermanent(err) {
		t.Errorf("no solver: %v", err)
	}
	_, w, err := f.attempt(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	// Issued for another key.
	f.orders.set("101", "certificate", cryptoxtest.NewKey(t))
	if _, _, err := f.attempt(w.State, false); problemKey(err) != i18n.AliyunErrorApplyBad || !retry.IsPermanent(err) {
		t.Errorf("another key: %v", err)
	}
	f.orders.answer = func(rpcCall) (any, error) { return nil, apiError("InvalidAccessKeyId.NotFound", 404) }
	if _, _, err := f.attempt(nil, false); problemKey(err) != i18n.AliyunErrorAuth || !retry.IsPermanent(err) {
		t.Errorf("refused key: %v", err)
	}
}

// An attempt that fails after it applied keeps the order: the next one
// goes on with it, and does not apply again.
func TestApplyKeepsOrder(t *testing.T) {
	f := newApplyFixture(t, map[string]string{})
	f.zone.fail = errors.New("quota")
	_, _, err := f.attempt(nil, false)
	if problemKey(err) != i18n.AliyunErrorApplyAdd || retry.IsPermanent(err) {
		t.Fatalf("a refused record: %v", err)
	}
	f.zone.fail = nil
	if _, w, err := f.attempt(contracts.KeptState(err), false); err != nil || w == nil {
		t.Fatalf("the attempt after: %v", err)
	}
	if n := len(f.orders.called("CreateCertificateForPackageRequest")); n != 1 || len(f.zone.records) != 1 {
		t.Errorf("applied %d times, records %v", n, f.zone.records)
	}
}

func TestApplyTest(t *testing.T) {
	f := newApplyFixture(t, map[string]string{})
	msg, err := f.a.Test(t.Context())
	if err != nil || msg.Key != i18n.AliyunTestApplyOk || msg.Params["product"] != freeProduct || msg.Params["total"] != 20 || msg.Params["used"] != 3 {
		t.Errorf("test: %+v %v", msg, err)
	}
}
