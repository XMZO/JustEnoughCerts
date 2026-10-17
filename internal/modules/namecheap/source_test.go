package namecheap

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

func problemKey(err error) i18n.Key {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return p.Key
	}
	return ""
}

func (f *fixture) logKeys() []i18n.Key {
	f.logMu.Lock()
	defer f.logMu.Unlock()
	var keys []i18n.Key
	for _, l := range f.logged {
		keys = append(keys, l.Key)
	}
	return keys
}

func TestIssue(t *testing.T) {
	f := newFixture(t)
	key := rsaKeys()[0]
	f.vendor.add("c1", "example.com", stateAwaitingCSR, nil)

	// Register, send the CSR, put the validation in place, report it.
	chain, wait, err := f.issue(key, nil)
	if err != nil || chain != nil || wait == nil || wait.After != poll {
		t.Fatalf("first attempt: %v %v %v", chain, wait, err)
	}
	if !slices.Equal(f.vendor.called()[:4], []string{"/integrations/create", "/integrations/updateMeta", "/integration-token/refresh", "/integrations/status"}) {
		t.Errorf("calls %v", f.vendor.called())
	}
	f.vendor.with(func() {
		if c := f.vendor.find("c1"); c.csr == nil || !c.marked || !f.vendor.meta {
			t.Fatalf("at the vendor: %+v, meta %v", c, f.vendor.meta)
		}
	})
	if got, ok := f.site.file("example.com" + challenge1); !ok || string(got) != "abc123\ncomodoca.com" {
		t.Errorf("challenge file %q", got)
	}
	if mark, ok := f.site.file("example.com/.well-known/ssl-manager/installed.txt"); !ok || !strings.HasSuffix(string(mark), " example.com") {
		t.Errorf("installed.txt %q", mark)
	}
	if mark, _ := f.site.file("example.com/.well-known/ssl-manager/installed.b64.json"); !strings.Contains(string(decode(t, mark)), `"domain":"example.com"`) {
		t.Errorf("installed.b64.json %q", mark)
	}
	var o order
	if err := json.Unmarshal(wait.State, &o); err != nil || o.Cert != "c1" || len(o.Files) != 1 || !slices.Equal(o.Marked, []string{"ch-c1"}) {
		t.Fatalf("state %s", wait.State)
	}

	// JEC's own calls changed the status, so the next attempt looks again;
	// after that, with nothing new, it asks for the status only.
	_, wait2, err := f.issue(key, wait.State)
	if err != nil || wait2 == nil || !slices.Contains(f.logKeys(), i18n.NamecheapLogState) {
		t.Fatalf("an attempt after JEC's own calls: %v %v", err, f.logKeys())
	}
	before := len(f.vendor.called())
	if _, wait2, err = f.issue(key, wait2.State); err != nil || wait2 == nil || !slices.Equal(f.vendor.called()[before:], []string{"/integrations/status"}) {
		t.Fatalf("an attempt with nothing new: %v, calls %v", err, f.vendor.called()[before:])
	}

	// Issued: download, clean up, tell the vendor.
	f.vendor.issue("c1")
	chain, _, err = f.issue(key, wait2.State)
	if err != nil || len(chain) != 2 {
		t.Fatalf("download: %v %v", chain, err)
	}
	if !key.PublicKey.Equal(chain[0].PublicKey) || chain[1].Subject.CommonName != "Test Intermediate" {
		t.Errorf("chain %v", chain)
	}
	if _, ok := f.site.file("example.com" + challenge1); ok {
		t.Error("the challenge file stayed")
	}
	if _, ok := f.site.file("example.com/.well-known/ssl-manager/installed.txt"); !ok {
		t.Error("the mark went")
	}
	f.vendor.with(func() {
		if !slices.Equal(f.vendor.confirmed, []string{"jec-" + chain[0].SerialNumber.Text(16)}) {
			t.Errorf("confirmed %v", f.vendor.confirmed)
		}
	})
	keys := f.logKeys()
	for _, k := range []i18n.Key{i18n.NamecheapLogRegister, i18n.NamecheapLogRequest, i18n.NamecheapLogPutFile, i18n.NamecheapLogValidate, i18n.NamecheapLogRemoveFile, i18n.NamecheapLogIssued} {
		if !slices.Contains(keys, k) {
			t.Errorf("log %v lacks %s", keys, k)
		}
	}
	if slices.Contains(keys, i18n.NamecheapLogNotServed) {
		t.Errorf("the website served the file, yet the log says not: %v", keys)
	}
}

func decode(t *testing.T, data []byte) []byte {
	t.Helper()
	out, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIssueNextCertificate(t *testing.T) {
	f := newFixture(t)
	old, key := rsaKeys()[1], rsaKeys()[0]
	// The vendor's certificate is the one before, for another key.
	f.vendor.add("c0", "example.com", stateIssued, f.vendor.ca.Leaf(t, old, time.Now().Add(-time.Hour), time.Now().AddDate(0, 0, 20), "example.com"))
	_, wait, err := f.issue(key, nil)
	if err != nil || wait == nil || !slices.Contains(f.logKeys(), i18n.NamecheapLogNext) {
		t.Fatalf("with the old certificate only: %v %v", err, f.logKeys())
	}
	if slices.Contains(f.vendor.called(), "/certificates/readPEMFormatted") {
		t.Error("downloaded a certificate whose modulus is another key's")
	}

	// The next one comes, and the issuance goes on with it.
	f.vendor.add("c1", "example.com", stateAwaitingCSR, nil)
	if _, wait, err = f.issue(key, wait.State); err != nil || wait == nil {
		t.Fatalf("with the next certificate: %v", err)
	}
	f.vendor.with(func() {
		if f.vendor.find("c1").csr == nil {
			t.Fatal("no CSR for the next certificate")
		}
	})
	f.vendor.issue("c1")
	chain, _, err := f.issue(key, wait.State)
	if err != nil || !key.PublicKey.Equal(chain[0].PublicKey) {
		t.Fatalf("issued: %v", err)
	}
}

func TestIssueWaitsForSomething(t *testing.T) {
	f := newFixture(t)
	key := rsaKeys()[0]

	// No certificate at all.
	_, wait, err := f.issue(key, nil)
	if err != nil || wait == nil || !slices.Contains(f.logKeys(), i18n.NamecheapLogNone) {
		t.Fatalf("no certificate: %v %v", err, f.logKeys())
	}

	// One whose CSR came from elsewhere.
	f.vendor.add("c1", "example.com", stateAwaitingChallenges, nil)
	if _, wait, err = f.issue(key, wait.State); err != nil || wait == nil || !slices.Contains(f.logKeys(), i18n.NamecheapLogNotOurs) {
		t.Fatalf("not ours: %v %v", err, f.logKeys())
	}
	if slices.Contains(f.vendor.called(), "/certificates/listChallenges") {
		t.Error("validated a certificate for a CSR from elsewhere")
	}

	// A state the official client waits through, logged once.
	f.vendor.with(func() {
		f.vendor.find("c1").state = "Pending Validation"
		f.vendor.fingerprint++
	})
	for range 2 {
		if _, wait, err = f.issue(key, wait.State); err != nil || wait == nil {
			t.Fatal(err)
		}
		f.vendor.with(func() { f.vendor.fingerprint++ })
	}
	n := 0
	for _, k := range f.logKeys() {
		if k == i18n.NamecheapLogState {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d state lines", n)
	}
}

func TestIssueWithoutStatus(t *testing.T) {
	f := newFixture(t)
	key := rsaKeys()[0]
	f.vendor.add("c1", "example.com", stateAwaitingCSR, nil)
	if _, err := f.source().Test(t.Context()); err != nil { // registered, so the hash is known
		t.Fatal(err)
	}
	// The status call is gone: every attempt lists the certificates.
	if err := f.m.save(t.Context(), func() *integration {
		it, _ := f.m.load(t.Context(), f.id)
		it.hash = "unknown"
		return it
	}()); err != nil {
		t.Fatal(err)
	}
	_, wait, err := f.issue(key, nil)
	if err != nil || wait == nil {
		t.Fatal(err)
	}
	before := len(f.vendor.called())
	if _, _, err := f.issue(key, wait.State); err != nil || !slices.Contains(f.vendor.called()[before:], "/certificates/list") {
		t.Errorf("without a status: %v, calls %v", err, f.vendor.called()[before:])
	}
}

func TestIssueFailures(t *testing.T) {
	f := newFixture(t)
	key := rsaKeys()[0]

	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.issue(ec, nil); problemKey(err) != i18n.NamecheapErrorKeyType || !retry.IsPermanent(err) {
		t.Errorf("ECDSA key: %v", err)
	}

	f.vendor.add("c1", "example.com", stateFailed, nil)
	if _, _, err := f.issue(key, nil); problemKey(err) != i18n.NamecheapErrorOrderFailed || !retry.IsPermanent(err) {
		t.Errorf("failed order: %v", err)
	}
	f.vendor.add("c2", "example.com", stateRevoked, nil)
	if _, _, err := f.issue(key, nil); problemKey(err) != i18n.NamecheapErrorRevoked || !retry.IsPermanent(err) {
		t.Errorf("revoked: %v", err)
	}

	// The vendor forgets JEC's access token.
	f.vendor.with(func() { f.vendor.access = "gone" })
	if _, _, err := f.issue(key, nil); problemKey(err) != i18n.NamecheapErrorAuth || !retry.IsPermanent(err) {
		t.Errorf("access refused: %v", err)
	}
}

func TestRegistration(t *testing.T) {
	f := newFixture(t)
	key := rsaKeys()[0]

	// A token the vendor does not take is no use retrying.
	f.vendor.with(func() { f.vendor.oneTime = "another" })
	if _, _, err := f.issue(key, nil); problemKey(err) != i18n.NamecheapErrorToken || !retry.IsPermanent(err) {
		t.Fatalf("refused token: %v", err)
	}
	if it, err := f.m.load(t.Context(), f.id); it != nil || err != nil {
		t.Fatalf("stored %+v %v", it, err)
	}

	// Tested, the instance registers once, and lists the certificates.
	f.vendor.with(func() { f.vendor.oneTime = oneTime })
	f.vendor.add("c1", "example.com", stateAwaitingCSR, nil)
	for range 2 {
		msg, err := f.source().Test(t.Context())
		if err != nil || msg.Key != i18n.NamecheapTestOk || msg.Params["count"] != 1 {
			t.Fatalf("test: %v %v", msg, err)
		}
	}
	if n := strings.Count(strings.Join(f.vendor.called(), " "), "/integrations/create"); n != 2 {
		t.Errorf("%d registrations, counting the refused one", n)
	}

	// A new one-time token registers again.
	if err := f.reg.Update(t.Context(), f.id, nil, nil, map[string]string{"token": "one-time-2"}); err != nil {
		t.Fatal(err)
	}
	f.vendor.with(func() { f.vendor.oneTime = "one-time-2" })
	if _, err := f.source().Test(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(f.vendor.called(), " "), "/integrations/create"); n != 3 {
		t.Errorf("%d registrations after the new token", n)
	}
}

func TestRefresh(t *testing.T) {
	f := newFixture(t)
	if _, err := f.source().Test(t.Context()); err != nil {
		t.Fatal(err)
	}
	it, err := f.m.load(t.Context(), f.id)
	var access string
	f.vendor.with(func() { access = f.vendor.access })
	if err != nil || it.access != access || it.expiresAt == 0 {
		t.Fatalf("after registering: %+v %v", it, err)
	}
	refreshes := func() int { return strings.Count(strings.Join(f.vendor.called(), " "), "/integration-token/refresh") }

	// Fresh enough: not refreshed.
	if _, err := f.source().Test(t.Context()); err != nil || refreshes() != 1 {
		t.Fatalf("%d refreshes, %v", refreshes(), err)
	}
	// Ten days or less left: refreshed.
	f.now = time.Unix(it.expiresAt, 0).Add(-refreshBefore + time.Hour)
	if _, err := f.source().Test(t.Context()); err != nil || refreshes() != 2 {
		t.Fatalf("%d refreshes, %v", refreshes(), err)
	}

	// The API does not refresh: try again a day later, keeping the token.
	it, _ = f.m.load(t.Context(), f.id)
	f.now = time.Unix(it.expiresAt, 0).Add(-time.Hour)
	f.vendor.with(func() { f.vendor.refreshFail = http.StatusNotFound })
	for range 2 {
		if _, err := f.source().Test(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if refreshes() != 3 {
		t.Errorf("%d refreshes while held back", refreshes())
	}
	if held, _ := f.m.load(t.Context(), f.id); held.refreshAfter != f.now.Add(refreshPause).Unix() || held.access != it.access {
		t.Errorf("held back %+v", held)
	}

	// Rate limited: as long as the API asks.
	f.now = f.now.Add(refreshPause + time.Minute)
	f.vendor.with(func() { f.vendor.refreshFail, f.vendor.retryAfter = http.StatusTooManyRequests, "120" })
	if _, err := f.source().Test(t.Context()); err != nil {
		t.Fatal(err)
	}
	if held, _ := f.m.load(t.Context(), f.id); held.refreshAfter != f.now.Add(2*time.Minute).Unix() {
		t.Errorf("after a 429: %+v", held)
	}
}

func TestRefreshAll(t *testing.T) {
	f := newFixture(t)
	if _, err := f.source().Test(t.Context()); err != nil {
		t.Fatal(err)
	}
	it, _ := f.m.load(t.Context(), f.id)
	f.now = time.Unix(it.expiresAt, 0).Add(-time.Hour)
	if err := f.m.refreshAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := f.m.load(t.Context(), f.id); fresh.access == it.access {
		t.Error("not refreshed")
	}

	// The instance is gone: so is its registration.
	if err := f.reg.Delete(t.Context(), f.id); err != nil {
		t.Fatal(err)
	}
	if err := f.m.refreshAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if gone, err := f.m.load(t.Context(), f.id); gone != nil || err != nil {
		t.Errorf("left %+v %v", gone, err)
	}
}

func TestExplain(t *testing.T) {
	for _, tt := range []struct {
		err       error
		key       i18n.Key
		permanent bool
		after     time.Duration
	}{
		{errors.New("connection refused"), i18n.NamecheapErrorConnect, false, 0},
		{&apiError{Status: http.StatusUnauthorized}, i18n.NamecheapErrorAuth, true, 0},
		{&apiError{Status: http.StatusForbidden}, i18n.NamecheapErrorAuth, true, 0},
		{&apiError{Status: http.StatusTooManyRequests, RetryAfter: time.Minute}, i18n.NamecheapErrorApi, false, time.Minute},
		{&apiError{Status: http.StatusBadGateway}, i18n.NamecheapErrorApi, false, 0},
		{&apiError{Status: http.StatusOK, Code: "INVALID"}, i18n.NamecheapErrorApi, true, 0},
		{&apiError{Status: http.StatusNotFound}, i18n.NamecheapErrorApi, true, 0},
	} {
		err := explain(tt.err)
		after, _ := retry.Delay(err)
		if problemKey(err) != tt.key || retry.IsPermanent(err) != tt.permanent || after != tt.after {
			t.Errorf("%v: %v (permanent %v, after %v)", tt.err, err, retry.IsPermanent(err), after)
		}
	}
}

func TestAPIAddress(t *testing.T) {
	m := New()
	if _, err := m.client(providersInstance("http://example.com/api")); problemKey(err) != i18n.NamecheapErrorUrl {
		t.Errorf("plain HTTP: %v", err)
	}
	c, err := m.client(providersInstance(""))
	if err != nil || c.user("/x") != defaultURL+"/x" || c.vendor("/vendor/y") != strings.TrimSuffix(defaultURL, "/user")+"/vendor/y" {
		t.Errorf("default: %+v %v", c, err)
	}
}
