package certs

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

func TestUseVersion(t *testing.T) {
	f := newFixture(t)
	target := f.provider("fake.hook")
	c := f.uploadTo(f.leaf(60, "example.com"), target)
	f.uploadVersion(c.ID, f.leaf(90, "example.com"))
	f.runDeploys()
	versions := f.versionsOf(c.ID)
	if len(versions) != 2 {
		t.Fatalf("versions %+v", versions)
	}
	current, earlier := versions[0], versions[1]
	if current.Usable || !earlier.Usable {
		t.Fatalf("usable: current %v, earlier %v", current.Usable, earlier.Usable)
	}
	use := func(v VersionBody) string {
		return "/certificates/" + strconv.FormatInt(c.ID, 10) + "/versions/" + strconv.FormatInt(v.ID, 10) + "/use"
	}
	if e := f.apiError(f.api.Post(use(current)), http.StatusConflict); e.Code != string(i18n.CertsErrorUseCurrent) {
		t.Errorf("use the current one: %+v", e)
	}

	// Back to the earlier one: current again, deployed, and told of.
	f.call(f.api.Post(use(earlier)), http.StatusNoContent, nil)
	got := f.certificate(c.ID)
	if got.Current == nil || got.Current.ID != earlier.ID || got.State != "deploying" {
		t.Fatalf("after going back: %s %+v", got.State, got.Current)
	}
	f.runDeploys()
	last := f.fake.deployed[len(f.fake.deployed)-1]
	if cryptox.Fingerprint(last.Chain[0]) != earlier.Fingerprint || len(last.Previous) == 0 {
		t.Errorf("deployed %+v", last)
	}
	told := false
	for _, e := range f.events() {
		if e.typ == contracts.CertificateCurrent && e.data.VersionID == earlier.ID && e.data.Fingerprint == earlier.Fingerprint {
			told = true
		}
	}
	if !told {
		t.Errorf("not told: %+v", f.events())
	}
	if f.certificate(c.ID).State != "ok" {
		t.Errorf("state %s", f.certificate(c.ID).State)
	}

	// One that ended can no longer be used.
	f.now = f.now.Add(91 * 24 * time.Hour)
	for _, v := range f.versionsOf(c.ID) {
		if v.Usable {
			t.Errorf("an ended version usable: %+v", v)
		}
	}
	if e := f.apiError(f.api.Post(use(current)), http.StatusConflict); e.Code != string(i18n.CertsErrorUseEnded) {
		t.Errorf("use an ended one: %+v", e)
	}
}
