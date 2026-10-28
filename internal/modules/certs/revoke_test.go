package certs

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

// versionsOf returns the versions of certificate id, the newest first.
func (f *fixture) versionsOf(id int64) []VersionBody {
	f.t.Helper()
	var d struct {
		Versions []VersionBody `json:"versions"`
	}
	f.call(f.api.Get("/certificates/"+strconv.FormatInt(id, 10)), http.StatusOK, &d)
	return d.Versions
}

// issueTwice adds a certificate from the fake CA that revokes, and issues
// it again an hour later: the first version is replaced.
func (f *fixture) issueTwice() (c CertificateBody, old, current VersionBody) {
	f.t.Helper()
	f.call(f.api.Post("/certificates/issue", map[string]any{
		"domains": []string{"example.com"}, "sourceProvider": f.provider("fake.acme"), "solverProvider": f.provider("fake.dns"),
	}), http.StatusCreated, &c)
	f.run()
	f.now = f.now.Add(time.Hour)
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/issue"), http.StatusAccepted, nil)
	f.run()
	v := f.versionsOf(c.ID)
	if len(v) != 2 {
		f.t.Fatalf("versions: %+v", v)
	}
	return c, v[1], v[0]
}

func revokePath(c CertificateBody, v VersionBody) string {
	return fmt.Sprintf("/certificates/%d/versions/%d/revoke", c.ID, v.ID)
}

func TestRevoke(t *testing.T) {
	f := newFixture(t)
	c, old, current := f.issueTwice()
	if !old.Revocable || current.Revocable {
		t.Fatalf("revocable: old %v, current %v", old.Revocable, current.Revocable)
	}

	if e := f.apiError(f.api.Post(revokePath(c, old), map[string]any{"password": "wrong", "reason": "superseded"}), http.StatusUnprocessableEntity); e.Code != string(i18n.KernelErrorPasswordWrong) {
		t.Errorf("wrong password: %+v", e)
	}
	if e := f.apiError(f.api.Post(revokePath(c, current), map[string]any{"password": testPassword, "reason": "superseded"}), http.StatusConflict); e.Code != string(i18n.CertsErrorRevokeCurrent) {
		t.Errorf("the version in use: %+v", e)
	}
	f.apiError(f.api.Post(revokePath(c, old), map[string]any{"password": testPassword, "reason": "affiliation_changed"}), http.StatusUnprocessableEntity)
	f.apiError(f.api.Post(fmt.Sprintf("/certificates/%d/versions/999/revoke", c.ID), map[string]any{"password": testPassword, "reason": "superseded"}), http.StatusNotFound)
	if len(f.fake.revoked) != 0 {
		t.Fatalf("revoked %v", f.fake.revoked)
	}

	// What the CA says comes back as it is.
	f.fake.revoke = func(*x509.Certificate, contracts.RevocationReason) error {
		return &contracts.Problem{Key: i18n.AcmeErrorRevoke, Params: map[string]any{"detail": "no"}}
	}
	if e := f.apiError(f.api.Post(revokePath(c, old), map[string]any{"password": testPassword, "reason": "key_compromise"}), http.StatusBadGateway); e.Code != string(i18n.AcmeErrorRevoke) || e.Params["detail"] != "no" {
		t.Errorf("the CA refused: %+v", e)
	}
	f.fake.revoke = nil

	f.call(f.api.Post(revokePath(c, old), map[string]any{"password": testPassword, "reason": "key_compromise"}), http.StatusNoContent, nil)
	if want := []string{old.Serial + " 1"}; !slices.Equal(f.fake.revoked, want) {
		t.Errorf("revoked %v, want %v", f.fake.revoked, want)
	}
	v := f.versionsOf(c.ID)[1]
	if v.Revocable || v.RevokedAt == nil || !v.RevokedAt.Equal(f.now) || v.RevokeReason != "key_compromise" {
		t.Errorf("revoked version: %+v", v)
	}
	if e := f.apiError(f.api.Post(revokePath(c, old), map[string]any{"password": testPassword, "reason": "superseded"}), http.StatusConflict); e.Code != string(i18n.CertsErrorRevoked) {
		t.Errorf("again: %+v", e)
	}
	if f.fake.opened != 0 {
		t.Errorf("%d providers left open", f.fake.opened)
	}
}

func TestRevokeRefused(t *testing.T) {
	f := newFixture(t)
	reason := map[string]any{"password": testPassword, "reason": "unspecified"}

	// Uploaded versions, and those of a source that does not revoke.
	up := f.add(f.leaf(30, "up.example.com"))
	f.uploadVersion(up.ID, f.leaf(60, "up.example.com"))
	if v := f.versionsOf(up.ID)[1]; v.Revocable {
		t.Errorf("an uploaded version is revocable")
	} else if e := f.apiError(f.api.Post(revokePath(up, v), reason), http.StatusConflict); e.Code != string(i18n.CertsErrorRevokeSource) {
		t.Errorf("an uploaded version: %+v", e)
	}
	plain := f.issueNew("plain.example.com")
	f.run()
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(plain.ID, 10)+"/issue"), http.StatusAccepted, nil)
	f.run()
	if v := f.versionsOf(plain.ID)[1]; v.Revocable {
		t.Errorf("a version of a source that does not revoke is revocable")
	} else if e := f.apiError(f.api.Post(revokePath(plain, v), reason), http.StatusConflict); e.Code != string(i18n.CertsErrorRevokeSource) {
		t.Errorf("a source that does not revoke: %+v", e)
	}

	// A version whose source is gone.
	c, old, _ := f.issueTwice()
	if _, err := f.db.ExecContext(context.Background(), "UPDATE certs_versions SET source_provider = 999 WHERE id = ?", old.ID); err != nil {
		t.Fatal(err)
	}
	if v := f.versionsOf(c.ID)[1]; v.Revocable {
		t.Errorf("a version of a deleted source is revocable")
	} else if e := f.apiError(f.api.Post(revokePath(c, v), reason), http.StatusConflict); e.Code != string(i18n.CertsErrorRevokeSourceGone) {
		t.Errorf("a deleted source: %+v", e)
	}

	// One that has expired.
	c, old, _ = f.issueTwice()
	f.now = old.NotAfter.Add(time.Minute)
	if v := f.versionsOf(c.ID)[1]; v.Revocable {
		t.Errorf("an expired version is revocable")
	} else if e := f.apiError(f.api.Post(revokePath(c, v), reason), http.StatusConflict); e.Code != string(i18n.CertsErrorRevokeExpired) {
		t.Errorf("an expired version: %+v", e)
	}
	if len(f.fake.revoked) != 0 {
		t.Errorf("revoked %v", f.fake.revoked)
	}
}

func TestRetire(t *testing.T) {
	f := newFixture(t)
	c, old, current := f.issueTwice()
	path := "/certificates/" + strconv.FormatInt(c.ID, 10) + "/retire"
	if e := f.apiError(f.api.Post(path, map[string]any{"password": "wrong"}), http.StatusUnprocessableEntity); e.Code != string(i18n.KernelErrorPasswordWrong) {
		t.Errorf("wrong password: %+v", e)
	}

	// The CA refuses one: nothing is deleted.
	f.fake.revoke = func(leaf *x509.Certificate, _ contracts.RevocationReason) error {
		if fmt.Sprintf("%x", leaf.SerialNumber) == old.Serial {
			return &contracts.Problem{Key: i18n.AcmeErrorRevoke, Params: map[string]any{"detail": "no"}}
		}
		return nil
	}
	if e := f.apiError(f.api.Post(path, map[string]any{"password": testPassword}), http.StatusBadGateway); e.Code != string(i18n.AcmeErrorRevoke) {
		t.Errorf("the CA refused: %+v", e)
	}
	if got := f.certificate(c.ID); got.ID != c.ID || len(f.fake.revoked) != 0 {
		t.Fatalf("after a refusal: revoked %v", f.fake.revoked)
	}
	f.fake.revoke = nil

	// The one in use too, last, for no site serves it any more.
	var out struct {
		Revoked int `json:"revoked"`
	}
	f.call(f.api.Post(path, map[string]any{"password": testPassword}), http.StatusOK, &out)
	want := []string{old.Serial + " 5", current.Serial + " 5"}
	if out.Revoked != 2 || !slices.Equal(f.fake.revoked, want) {
		t.Errorf("revoked %d: %v, want %v", out.Revoked, f.fake.revoked, want)
	}
	f.apiError(f.api.Get("/certificates/"+strconv.FormatInt(c.ID, 10)), http.StatusNotFound)
}

func TestRetireOtherReason(t *testing.T) {
	f := newFixture(t)
	c, old, current := f.issueTwice()
	// A CA that refuses cessation_of_operation, as ZeroSSL did.
	f.fake.revoke = func(_ *x509.Certificate, reason contracts.RevocationReason) error {
		if reason != contracts.ReasonUnspecified {
			return &contracts.Problem{Key: i18n.AcmeErrorRevokeReason, Err: contracts.ErrReasonRefused}
		}
		return nil
	}
	var out struct {
		Revoked int `json:"revoked"`
	}
	f.call(f.api.Post("/certificates/"+strconv.FormatInt(c.ID, 10)+"/retire", map[string]any{"password": testPassword}), http.StatusOK, &out)
	want := []string{old.Serial + " 0", current.Serial + " 0"}
	if out.Revoked != 2 || !slices.Equal(f.fake.revoked, want) {
		t.Errorf("revoked %d: %v, want %v", out.Revoked, f.fake.revoked, want)
	}

	// Revoking one version, the reason is the user's to change.
	c, old, _ = f.issueTwice()
	if e := f.apiError(f.api.Post(revokePath(c, old), map[string]any{"password": testPassword, "reason": "superseded"}), http.StatusBadGateway); e.Code != string(i18n.AcmeErrorRevokeReason) {
		t.Errorf("revoking one: %+v", e)
	}
}

func TestRevokeAll(t *testing.T) {
	f := newFixture(t)
	c, old, current := f.issueTwice()
	path := "/certificates/" + strconv.FormatInt(c.ID, 10) + "/revoke"
	body := func(reason, then string) map[string]any {
		return map[string]any{"password": testPassword, "reason": reason, "then": then}
	}

	// The CA refuses the one in use: the renewal is not left paused.
	f.fake.revoke = func(leaf *x509.Certificate, _ contracts.RevocationReason) error {
		if fmt.Sprintf("%x", leaf.SerialNumber) == current.Serial {
			return &contracts.Problem{Key: i18n.AcmeErrorRevoke, Params: map[string]any{"detail": "no"}}
		}
		return nil
	}
	if e := f.apiError(f.api.Post(path, body("cessation_of_operation", "pause")), http.StatusBadGateway); e.Code != string(i18n.AcmeErrorRevoke) {
		t.Errorf("the CA refused: %+v", e)
	}
	if got := f.certificate(c.ID); got.Paused || got.State == stateRevoked {
		t.Errorf("after a refusal: %+v", got)
	}
	f.fake.revoke = nil

	// Paused, the certificate stays, its version in use revoked last.
	var out struct {
		Revoked int `json:"revoked"`
	}
	f.call(f.api.Post(path, body("cessation_of_operation", "pause")), http.StatusOK, &out)
	want := []string{old.Serial + " 5", current.Serial + " 5"}
	if out.Revoked != 1 || !slices.Equal(f.fake.revoked, want) {
		t.Errorf("revoked %d: %v, want %v", out.Revoked, f.fake.revoked, want)
	}
	got := f.certificate(c.ID)
	if !got.Paused || got.State != stateRevoked || got.RenewAt != nil || got.Current.RevokedAt == nil || got.Current.RevokeReason != "cessation_of_operation" {
		t.Errorf("paused: %+v", got)
	}
	// Nor does the renewal start for it.
	if err := f.svc.renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.certificate(c.ID); got.Issuance != nil && got.Issuance.State != issuanceDone {
		t.Errorf("renewing a paused one: %+v", got.Issuance)
	}
	if e := f.apiError(f.api.Post(path, body("cessation_of_operation", "pause")), http.StatusConflict); e.Code != string(i18n.CertsErrorRevokeNone) {
		t.Errorf("nothing left: %+v", e)
	}

	// Renewed: a new version comes at once.
	c, _, current = f.issueTwice()
	f.fake.revoked = nil
	path = "/certificates/" + strconv.FormatInt(c.ID, 10) + "/revoke"
	f.call(f.api.Post(path, body("key_compromise", "renew")), http.StatusOK, &out)
	if out.Revoked != 2 || !slices.Contains(f.fake.revoked, current.Serial+" 1") {
		t.Errorf("revoked %d: %v", out.Revoked, f.fake.revoked)
	}
	f.run()
	if got := f.certificate(c.ID); got.Paused || got.Current.ID == current.ID || got.State == stateRevoked {
		t.Errorf("renewed: %+v", got)
	}

	// Uploaded: nothing JEC can revoke, and nothing to issue it from.
	up := f.add(f.leaf(90, "up.example.com"))
	if e := f.apiError(f.api.Post("/certificates/"+strconv.FormatInt(up.ID, 10)+"/revoke", body("superseded", "pause")), http.StatusConflict); e.Code != string(i18n.CertsErrorRevokeNone) {
		t.Errorf("uploaded: %+v", e)
	}
}

func TestRevokeOld(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	c, old, current := f.issueTwice()
	check := func() {
		t.Helper()
		if err := f.svc.checkRevokeOld(ctx); err != nil {
			t.Fatal(err)
		}
	}
	revokedOld := func() *VersionBody {
		t.Helper()
		for _, v := range f.versionsOf(c.ID) {
			if v.ID == old.ID {
				return &v
			}
		}
		t.Fatal("no old version")
		return nil
	}

	// Off: the old one stays.
	f.now = f.now.Add(2 * day)
	check()
	if len(f.fake.revoked) != 0 {
		t.Fatalf("off: %v", f.fake.revoked)
	}

	// On, without targets: a day after the version in use came.
	path := "/certificates/" + strconv.FormatInt(c.ID, 10)
	var got CertificateBody
	f.call(f.api.Patch(path, map[string]any{"revokeOld": true}), http.StatusOK, &got)
	if !got.RevokeOld {
		t.Fatalf("on: %+v", got)
	}
	// A CA that refuses the reason revokes for none; one that fails is
	// asked again a day later.
	f.fake.revoke = func(*x509.Certificate, contracts.RevocationReason) error {
		return &contracts.Problem{Key: i18n.AcmeErrorRevoke, Params: map[string]any{"detail": "down"}}
	}
	check()
	f.now = f.now.Add(time.Hour)
	check()
	if v := revokedOld(); v.RevokedAt != nil {
		t.Fatalf("failed: %+v", v)
	}
	asked := 0
	f.fake.revoke = func(_ *x509.Certificate, reason contracts.RevocationReason) error {
		asked++
		if reason != contracts.ReasonUnspecified {
			return &contracts.Problem{Key: i18n.AcmeErrorRevokeReason, Err: contracts.ErrReasonRefused}
		}
		return nil
	}
	check()
	if asked != 0 {
		t.Fatalf("asked again within a day: %d", asked)
	}
	f.now = f.now.Add(day)
	check()
	if v := revokedOld(); asked != 2 || v.RevokedAt == nil || v.RevokeReason != "unspecified" {
		t.Errorf("revoked: %+v, asked %d", v, asked)
	}
	if got := f.certificate(c.ID); got.Current.ID != current.ID || got.Current.RevokedAt != nil {
		t.Errorf("the version in use: %+v", got.Current)
	}

	// With a target: only once the new version is deployed there.
	f.fake.revoke = nil
	f.fake.revoked = nil
	target := f.provider("fake.hook")
	f.call(f.api.Patch(path, map[string]any{"targets": []int64{target}}), http.StatusOK, nil)
	f.runDeploys()
	f.call(f.api.Post(path+"/issue"), http.StatusAccepted, nil)
	f.run()
	check()
	if len(f.fake.revoked) != 0 {
		t.Fatalf("before the deployment: %v", f.fake.revoked)
	}
	f.runDeploys()
	check()
	if want := []string{current.Serial + " 4"}; !slices.Equal(f.fake.revoked, want) {
		t.Errorf("after the deployment: %v, want %v", f.fake.revoked, want)
	}
}
