package certs

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

// publicOnly stands in for the key of a CSR, which the CA signs for
// without having it.
type publicOnly struct{ pub crypto.PublicKey }

func (p publicOnly) Public() crypto.PublicKey { return p.pub }
func (publicOnly) Sign(io.Reader, []byte, crypto.SignerOpts) ([]byte, error) {
	panic("a CA does not have the key it signs for")
}

// issueFor plays the CA: it issues a certificate for the CSR in PEM, that
// ends after days, with the intermediate.
func (f *fixture) issueFor(csrPEM string, days int) string {
	f.t.Helper()
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		f.t.Fatalf("no CSR in %q", csrPEM)
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		f.t.Fatalf("a CSR that does not check: %v", err)
	}
	end := f.now.Add(time.Duration(days) * day)
	leaf := f.ca.Leaf(f.t, publicOnly{csr.PublicKey}, end.Add(-90*day), end, csr.DNSNames...)
	return cryptoxtest.CertPEM(leaf, f.ca.Intermediate)
}

func TestRequest(t *testing.T) {
	f := newFixture(t)

	// A certificate to buy: it waits with a CSR for its names.
	var c CertificateBody
	f.call(f.api.Post("/certificates/request", map[string]any{"domains": []string{"Shop.Example.com", "www.shop.example.com"}}), http.StatusCreated, &c)
	path := "/certificates/" + strconv.FormatInt(c.ID, 10)
	if c.State != stateAwaiting || c.Source != "upload" || c.KeyType != "rsa-2048" || c.Current != nil {
		t.Fatalf("requested %+v", c)
	}
	var d struct {
		Certificate CertificateBody `json:"certificate"`
		Request     *RequestBody    `json:"request"`
	}
	f.call(f.api.Get(path), http.StatusOK, &d)
	if d.Request == nil || d.Request.KeyType != "rsa-2048" || !strings.HasPrefix(d.Request.CSR, "-----BEGIN CERTIFICATE REQUEST-----") {
		t.Fatalf("the request: %+v", d.Request)
	}

	// The certificate the CA issued comes without a key, here through the
	// form for a new certificate, and becomes its first version.
	issued := f.issueFor(d.Request.CSR, 90)
	var got CertificateBody
	f.call(f.api.Post("/certificates", map[string]any{"certificate": issued}), http.StatusCreated, &got)
	if got.ID != c.ID || got.State != stateOK || got.Current == nil || strings.Join(got.Current.Names, ",") != "shop.example.com,www.shop.example.com" {
		t.Fatalf("the issued certificate: %+v", got)
	}
	d.Request = nil
	f.call(f.api.Get(path), http.StatusOK, &d)
	if d.Request != nil {
		t.Errorf("the request stayed: %+v", d.Request)
	}

	// A renewal: a new key and CSR, of another type; the certificate for
	// it comes on the certificate's page.
	var r RequestBody
	f.call(f.api.Post(path+"/csr", map[string]any{"keyType": "ecdsa-p256"}), http.StatusOK, &r)
	if r.KeyType != "ecdsa-p256" {
		t.Fatalf("renewal request %+v", r)
	}
	f.call(f.api.Post(path+"/versions", map[string]any{"certificate": f.issueFor(r.CSR, 180)}), http.StatusCreated, &got)
	if got.KeyType != "ecdsa-p256" || got.Current == nil || !got.Current.NotAfter.Equal(f.now.Add(180*day)) {
		t.Fatalf("the renewed certificate: %+v", got)
	}

	// A CA's reissue for the same CSR has the key of the version in use.
	f.call(f.api.Post(path+"/versions", map[string]any{"certificate": f.issueFor(r.CSR, 200)}), http.StatusCreated, &got)
	if !got.Current.NotAfter.Equal(f.now.Add(200 * day)) {
		t.Errorf("the reissue: %+v", got.Current)
	}

	// A certificate of another key, without it, fits nothing.
	other := f.leaf(300, "shop.example.com")
	e := f.apiError(f.api.Post(path+"/versions", map[string]any{"certificate": other.cert}), http.StatusUnprocessableEntity)
	if len(e.Fields) != 1 || e.Fields[0].Location != "body.privateKey" {
		t.Errorf("a certificate of another key: %+v", e)
	}
	e = f.apiError(f.api.Post("/certificates", map[string]any{"certificate": other.cert}), http.StatusUnprocessableEntity)
	if len(e.Fields) != 1 || e.Fields[0].Location != "body.privateKey" {
		t.Errorf("a new certificate without its key: %+v", e)
	}

	// Forgetting a request; a certificate with a source takes none.
	f.call(f.api.Post(path+"/csr", map[string]any{}), http.StatusOK, &r)
	if r.KeyType != "ecdsa-p256" {
		t.Errorf("a request of the certificate's own key type: %+v", r)
	}
	f.call(f.api.Delete(path+"/csr"), http.StatusNoContent, nil)
	f.apiError(f.api.Delete(path+"/csr"), http.StatusNotFound)
	auto := f.issueNew("auto.example.com")
	f.apiError(f.api.Post("/certificates/"+strconv.FormatInt(auto.ID, 10)+"/csr", map[string]any{}), http.StatusConflict)

	// Bad domains are told of.
	e = f.apiError(f.api.Post("/certificates/request", map[string]any{"domains": []string{"not a domain"}}), http.StatusUnprocessableEntity)
	if len(e.Fields) != 1 || e.Fields[0].Location != "body.domains[0]" {
		t.Errorf("a bad domain: %+v", e)
	}
}

func TestReminder(t *testing.T) {
	f := newFixture(t)
	c := f.add(f.leaf(90, "shop.example.com"))
	path := "/certificates/" + strconv.FormatInt(c.ID, 10)
	day := f.now.UTC().AddDate(0, 0, 2).Format(time.DateOnly)
	var got CertificateBody
	f.call(f.api.Patch(path, map[string]any{"reminder": map[string]any{"on": day, "note": "Renew the subscription"}}), http.StatusOK, &got)
	if got.Reminder == nil || got.Reminder.On != day || got.Reminder.Note != "Renew the subscription" || got.Reminder.Sent {
		t.Fatalf("set %+v", got.Reminder)
	}
	reminders := func() int {
		n := 0
		for _, e := range f.events() {
			if e.typ == contracts.CertificateReminder {
				n++
			}
		}
		return n
	}
	// Before 9:00 of the day, nothing; after, once.
	due, _ := time.ParseInLocation(time.DateOnly, day, time.UTC)
	f.now = due.Add(8 * time.Hour)
	if err := f.svc.checkReminders(t.Context()); err != nil || reminders() != 0 {
		t.Fatalf("before 9:00: %v, %d", err, reminders())
	}
	f.now = due.Add(9 * time.Hour)
	for range 2 {
		if err := f.svc.checkReminders(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if reminders() != 1 {
		t.Errorf("%d reminders sent", reminders())
	}
	f.call(f.api.Get(path), http.StatusOK, &struct {
		Certificate *CertificateBody `json:"certificate"`
	}{&got})
	if got.Reminder == nil || !got.Reminder.Sent {
		t.Errorf("after: %+v", got.Reminder)
	}
	// Another day is to be sent again; an empty one removes it; a day that
	// is none is refused.
	got = CertificateBody{} // fields left out of an answer are not reset
	f.call(f.api.Patch(path, map[string]any{"reminder": map[string]any{"on": "2030-01-01", "note": "Again"}}), http.StatusOK, &got)
	if got.Reminder == nil || got.Reminder.Sent {
		t.Errorf("moved %+v", got.Reminder)
	}
	got = CertificateBody{}
	f.call(f.api.Patch(path, map[string]any{"reminder": map[string]any{"on": ""}}), http.StatusOK, &got)
	if got.Reminder != nil {
		t.Errorf("removed %+v", got.Reminder)
	}
	f.apiError(f.api.Patch(path, map[string]any{"reminder": map[string]any{"on": "next week"}}), http.StatusUnprocessableEntity)
}
