package certs

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/configfile"
)

// configFile is a configuration file with the module's sections.
func (f *fixture) configFile() *configfile.Registry {
	f.t.Helper()
	r := configfile.New(f.db, f.reg, func(string) bool { return true })
	for _, s := range f.svc.configSections() {
		if err := r.Add("certs", s); err != nil {
			f.t.Fatal(err)
		}
	}
	return r
}

// named creates an instance of a fake type called name.
func (f *fixture) named(typ, name string) int64 {
	f.t.Helper()
	id, err := f.reg.Create(f.t.Context(), typ, name, map[string]string{}, map[string]string{})
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) importConfig(file string, apply bool) *configfile.Report {
	f.t.Helper()
	rep, err := f.configFile().Import(f.t.Context(), []byte(file), apply)
	if err != nil {
		f.t.Fatal(err)
	}
	return rep
}

// itemsOf lists the items of a section, as "name: action", and their
// problems, as "name/field: key".
func itemsOf(rep *configfile.Report, section string) []string {
	var out []string
	for _, s := range rep.Sections {
		if s.Key != section {
			continue
		}
		for _, it := range s.Items {
			out = append(out, it.Name+": "+string(it.Action))
			for _, p := range it.Problems {
				out = append(out, it.Name+"/"+p.Field+": "+string(p.Key))
			}
		}
	}
	return out
}

func TestConfigExport(t *testing.T) {
	f := newFixture(t)
	ca, dns, cdn := f.named("fake.ca", "Let's Encrypt"), f.named("fake.dns", "DNSPod"), f.named("fake.hook", "CDN")
	f.call(f.api.Post("/certificates/issue", map[string]any{
		"domains": []string{"example.com", "*.example.com"}, "sourceProvider": ca, "solverProvider": dns,
		"keyType": "ecdsa-p384", "renewBeforeDays": 20, "targets": []int64{cdn},
	}), 201, nil)
	f.call(f.api.Put("/settings", map[string]any{"renewBeforeDays": 25, "keyType": "rsa-3072", "checkTime": "03:30"}), 200, nil)
	f.uploadTo(f.leaf(90, "upload.example.com"), cdn)

	data, err := f.configFile().Export(t.Context(), configfile.Export{})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Renewal      renewalEntry       `yaml:"renewal"`
		Certificates []certificateEntry `yaml:"certificates"`
	}
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	want := []certificateEntry{
		{Domains: []string{"example.com", "*.example.com"}, Source: "Let's Encrypt", Solver: "DNSPod", KeyType: "ecdsa-p384", RenewBeforeDays: 20, Targets: []string{"CDN"}},
		{Domains: []string{"upload.example.com"}, RenewBeforeDays: 25, Targets: []string{"CDN"}},
	}
	if r := got.Renewal; r.RenewBeforeDays != 25 || r.CheckTime != "03:30" || r.KeyType != "rsa-3072" || r.Retries == nil || *r.Retries != defaultRetries ||
		len(got.Certificates) != 2 ||
		!slices.EqualFunc(got.Certificates, want, func(a, b certificateEntry) bool {
			return slices.Equal(a.Domains, b.Domains) && a.Source == b.Source && a.Solver == b.Solver && a.KeyType == b.KeyType &&
				a.RenewBeforeDays == b.RenewBeforeDays && slices.Equal(a.Targets, b.Targets)
		}) {
		t.Errorf("exported\n%s", data)
	}

	// As it is, the file changes nothing.
	rep := f.importConfig(string(data), true)
	if rep.Errors != 0 || !rep.Applied || strings.Join(itemsOf(rep, "certificates"), "\n") != "example.com *.example.com: keep\nupload.example.com: keep" ||
		strings.Join(itemsOf(rep, "renewal"), "\n") != ": keep" {
		t.Errorf("report %+v", rep)
	}
}

func TestConfigImportAdds(t *testing.T) {
	f := newFixture(t)
	file := `version: 1
providers:
  - {name: LE, type: fake.ca}
  - {name: DNS, type: fake.dns}
  - {name: CDN, type: fake.hook}
renewal:
  key_type: ecdsa-p256
certificates:
  - domains: [Example.COM, www.example.com]
    source: LE
    solver: DNS
    targets: [CDN]
`
	rep := f.importConfig(file, false)
	if rep.Errors != 0 || rep.Applied || strings.Join(itemsOf(rep, "certificates"), "\n") != "example.com www.example.com: add" ||
		strings.Join(itemsOf(rep, "renewal"), "\n") != ": change" {
		t.Fatalf("report %+v", rep)
	}
	if list, _ := f.svc.list(t.Context()); len(list) != 0 || f.triggered.Load() != 0 {
		t.Fatal("a dry run added a certificate")
	}

	rep = f.importConfig(file, true)
	if !rep.Applied {
		t.Fatalf("report %+v", rep)
	}
	list, err := f.svc.list(t.Context())
	if err != nil || len(list) != 1 {
		t.Fatalf("%v, %v", list, err)
	}
	c := list[0]
	// The defaults are those the file leaves, its renewal section coming
	// first.
	if c.Source != sourceProvider || c.KeyType != "ecdsa-p256" || c.RenewBeforeDays != 30 || len(c.Targets) != 1 || c.Issuance == nil || c.Issuance.State != issuancePending {
		t.Errorf("added %+v", c)
	}
	if f.triggered.Load() == 0 {
		t.Error("the runner was not woken")
	}
	st, _ := f.svc.settings(t.Context())
	if st.KeyType != "ecdsa-p256" {
		t.Errorf("settings %+v", st)
	}
}

func TestConfigImportChanges(t *testing.T) {
	f := newFixture(t)
	ca, dns, cdn := f.named("fake.ca", "LE"), f.named("fake.dns", "DNS"), f.named("fake.hook", "CDN")
	var body CertificateBody
	f.call(f.api.Post("/certificates/issue", map[string]any{
		"domains": []string{"example.com"}, "sourceProvider": ca, "solverProvider": dns, "keyType": "ecdsa-p256", "targets": []int64{cdn},
	}), 201, &body)
	f.run()

	// Another solver, from the file; another key type and window; no
	// targets. The source stays.
	rep := f.importConfig(`version: 1
providers:
  - {name: DNS 2, type: fake.dns}
  - {name: Other CA, type: fake.free}
certificates:
  - domains: [example.com]
    source: Other CA
    solver: DNS 2
    key_type: rsa-4096
    renew_before_days: 45
    targets: []
`, true)
	if !rep.Applied || strings.Join(itemsOf(rep, "certificates"), "\n") != "example.com: change\nexample.com/source: "+string(i18n.CertsConfigErrorSourceKept) {
		t.Fatalf("report %+v", rep)
	}
	c, err := f.svc.load(t.Context(), f.db, body.ID)
	if err != nil {
		t.Fatal(err)
	}
	solver, _ := f.reg.Get(t.Context(), c.SolverProvider)
	if c.SourceProvider != ca || solver.Name != "DNS 2" || c.KeyType != "rsa-4096" || c.RenewBeforeDays != 45 || len(c.Targets) != 0 {
		t.Errorf("changed to %+v", c)
	}

	// Left out, the targets, the key type and the window stay.
	rep = f.importConfig("version: 1\ncertificates:\n  - {domains: [example.com], source: LE, solver: DNS 2}\n", true)
	if strings.Join(itemsOf(rep, "certificates"), "\n") != "example.com: keep" {
		t.Errorf("report %+v", rep)
	}

	// A note comes and goes with the file, which an export writes again.
	rep = f.importConfig("version: 1\ncertificates:\n  - {domains: [example.com], source: LE, solver: DNS 2, note: the shop}\n", true)
	if c, err := f.svc.load(t.Context(), f.db, body.ID); err != nil || c.Note != "the shop" || !strings.Contains(strings.Join(itemsOf(rep, "certificates"), "\n"), "change") {
		t.Errorf("noted %+v, %v: %+v", c, err, rep)
	}
	if data, err := f.configFile().Export(t.Context(), configfile.Export{}); err != nil || !strings.Contains(string(data), "note: the shop") {
		t.Errorf("export %s: %v", data, err)
	}
	// A reminder too, which an export writes again; a day that is none is
	// a problem.
	rep = f.importConfig("version: 1\ncertificates:\n  - {domains: [example.com], source: LE, solver: DNS 2, reminder: {day: 2027-05-01, note: renew it}}\n", true)
	if c, err := f.svc.load(t.Context(), f.db, 1); err != nil || c.ReminderOn != "2027-05-01" || c.ReminderNote != "renew it" {
		t.Errorf("reminded %+v, %v: %+v", c, err, rep)
	}
	if data, err := f.configFile().Export(t.Context(), configfile.Export{}); err != nil || !strings.Contains(string(data), "day: \"2027-05-01\"") {
		t.Errorf("exported %s, %v", data, err)
	}
	if rep := f.importConfig("version: 1\ncertificates:\n  - {domains: [example.com], source: LE, solver: DNS 2, reminder: {day: soon}}\n", false); !strings.Contains(fmt.Sprint(rep), "reminder") {
		t.Errorf("a day that is none: %+v", rep)
	}

	// And the channels its notes go to, by name.
	f.named("fake.chat", "Ops")
	f.importConfig("version: 1\ncertificates:\n  - {domains: [example.com], source: LE, solver: DNS 2, notify_to: [Ops]}\n", true)
	if c, err := f.svc.load(t.Context(), f.db, body.ID); err != nil || len(c.NotifyTo) != 1 {
		t.Errorf("notify to %+v, %v", c, err)
	}
	if data, err := f.configFile().Export(t.Context(), configfile.Export{}); err != nil || !strings.Contains(string(data), "notify_to:\n") {
		t.Errorf("export %s: %v", data, err)
	}
	// So does keeping the key.
	f.importConfig("version: 1\ncertificates:\n  - {domains: [example.com], source: LE, solver: DNS 2, reuse_key: true}\n", true)
	if c, err := f.svc.load(t.Context(), f.db, body.ID); err != nil || !c.ReuseKey {
		t.Errorf("kept %+v, %v", c, err)
	}
	if data, err := f.configFile().Export(t.Context(), configfile.Export{}); err != nil || !strings.Contains(string(data), "reuse_key: true") {
		t.Errorf("export %s: %v", data, err)
	}
	// And pausing its renewal.
	f.importConfig("version: 1\ncertificates:\n  - {domains: [example.com], source: LE, solver: DNS 2, paused: true}\n", true)
	if c, err := f.svc.load(t.Context(), f.db, body.ID); err != nil || !c.Paused {
		t.Errorf("paused %+v, %v", c, err)
	}
	if data, err := f.configFile().Export(t.Context(), configfile.Export{}); err != nil || !strings.Contains(string(data), "paused: true") {
		t.Errorf("export %s: %v", data, err)
	}
	// And its deploy window, one of those offered.
	f.importConfig("version: 1\ncertificates:\n  - {domains: [example.com], source: LE, solver: DNS 2, deploy_window: 1-5}\n", true)
	if c, err := f.svc.load(t.Context(), f.db, body.ID); err != nil || c.DeployWindow != "1-5" {
		t.Errorf("window %+v, %v", c, err)
	}
	if r := f.importConfig("version: 1\ncertificates:\n  - {domains: [example.com], source: LE, solver: DNS 2, deploy_window: 3-4}\n", false); r.Errors == 0 {
		t.Errorf("a window not offered taken: %+v", r)
	}
}

func TestConfigImportProblems(t *testing.T) {
	f := newFixture(t)
	ca, dns := f.named("fake.ca", "LE"), f.named("fake.dns", "DNS")
	f.named("fake.vendor", "Vendor")
	f.named("fake.web", "Web root")
	f.call(f.api.Post("/certificates/issue", map[string]any{
		"domains": []string{"taken.example.com"}, "sourceProvider": ca, "solverProvider": dns,
	}), 201, nil)
	f.add(f.leaf(90, "uploaded.example.com"))
	rep := f.importConfig(`version: 1
renewal:
  check_time: "25:00"
certificates:
  - {domains: ["bad domain"], source: LE, solver: DNS}
  - {domains: [new.example.com], source: LE, solver: DNS, targets: [Nowhere]}
  - {domains: [new.example.com], source: LE, solver: DNS}
  - {domains: [nosource.example.com]}
  - {domains: [nosolver.example.com], source: LE}
  - {domains: [vendor.example.com], source: Vendor, solver: DNS}
  - {domains: ["*.wild.example.com"], source: Vendor, solver: Web root}
  - {domains: [key.example.com], source: LE, solver: DNS, key_type: dsa}
  - {domains: [uploaded.example.com], source: LE}
  - {domains: [taken.example.com], source: LE, solver: DNS, renew_before_days: 400}
`, true)
	want := []string{
		"bad domain: skip", "bad domain/domains[0]: " + string(i18n.CertsErrorBadDomain),
		"new.example.com: skip", "new.example.com/targets[0]: " + string(i18n.KernelConfigErrorNoProvider),
		"new.example.com: skip", "new.example.com/domains: " + string(i18n.KernelConfigErrorDuplicate),
		"nosource.example.com: skip", "nosource.example.com/: " + string(i18n.CertsConfigErrorNoSource),
		"nosolver.example.com: skip", "nosolver.example.com/solver: " + string(i18n.KernelErrorFieldRequired),
		"vendor.example.com: skip", "vendor.example.com/solver: " + string(i18n.CertsErrorSolverChallenge),
		"*.wild.example.com: skip", "*.wild.example.com/solver: " + string(i18n.CertsErrorWildcardHttp),
		"key.example.com: skip", "key.example.com/key_type: " + string(i18n.KernelErrorInvalidField),
		"uploaded.example.com: keep", "uploaded.example.com/source: " + string(i18n.CertsConfigErrorUploadedKept),
		"taken.example.com: skip", "taken.example.com/renew_before_days: " + string(i18n.KernelErrorInvalidField),
	}
	if got := itemsOf(rep, "certificates"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("certificates:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := itemsOf(rep, "renewal"); !slices.Equal(got, []string{": skip", "/check_time: " + string(i18n.KernelErrorInvalidField)}) {
		t.Errorf("renewal: %v", got)
	}
	// The warnings do not count.
	if rep.Errors != 9 || rep.Applied {
		t.Errorf("%d errors, applied %v", rep.Errors, rep.Applied)
	}
	if list, _ := f.svc.list(t.Context()); len(list) != 2 {
		t.Errorf("%d certificates after an import with errors", len(list))
	}
}

func TestConfigNamesProviders(t *testing.T) {
	f := newFixture(t)
	f.named("fake.ca", "Twin")
	f.named("fake.ca", "Twin")
	f.named("fake.dns", "DNS")
	rep := f.importConfig("version: 1\ncertificates:\n  - {domains: [a.example.com], source: Twin, solver: DNS}\n", false)
	if got := itemsOf(rep, "certificates"); !slices.Equal(got, []string{"a.example.com: skip", "a.example.com/source: " + string(i18n.KernelConfigErrorAmbiguous)}) {
		t.Errorf("an ambiguous source: %v", got)
	}
	if rep.Errors != 1 {
		t.Errorf("report %+v", rep)
	}
}

func TestConfigBackupSource(t *testing.T) {
	f := newFixture(t)
	f.named("fake.vendor", "Vendor")
	f.named("fake.web", "Web root")
	f.named("fake.records", "Records")
	spare := f.named("fake.spare", "Spare")
	f.named("fake.acme", "Other")

	// Added with its backup, which an export writes again.
	rep := f.importConfig(`version: 1
certificates:
  - {domains: [a.example.com], source: Vendor, solver: Web root, backup_source: Spare}
  - {domains: [b.example.com], source: Vendor, solver: Web root, backup_source: Other}
  - {domains: [c.example.com], source: Vendor, solver: Records, backup_source: Spare}
`, false)
	want := []string{
		"a.example.com: add",
		"b.example.com: skip", "b.example.com/backup_source: " + string(i18n.CertsErrorBackupSource),
		"c.example.com: skip", "c.example.com/backup_source: " + string(i18n.CertsErrorBackupSolver),
	}
	if got := itemsOf(rep, "certificates"); !slices.Equal(got, want) {
		t.Fatalf("report:\n%s", strings.Join(got, "\n"))
	}
	f.importConfig("version: 1\ncertificates:\n  - {domains: [a.example.com], source: Vendor, solver: Web root, backup_source: Spare}\n", true)
	list, err := f.svc.list(t.Context())
	if err != nil || len(list) != 1 || list[0].BackupProvider != spare {
		t.Fatalf("added %+v, %v", list, err)
	}
	if data, err := f.configFile().Export(t.Context(), configfile.Export{}); err != nil || !strings.Contains(string(data), "backup_source: Spare") {
		t.Errorf("export %s: %v", data, err)
	}

	// Left out, it stays, unless the new solver does not suit it; empty
	// removes it.
	rep = f.importConfig("version: 1\ncertificates:\n  - {domains: [a.example.com], source: Vendor, solver: Records}\n", false)
	if got := itemsOf(rep, "certificates"); !slices.Equal(got, []string{"a.example.com: skip", "a.example.com/backup_source: " + string(i18n.CertsErrorBackupSolver)}) {
		t.Errorf("another solver: %v", got)
	}
	f.importConfig("version: 1\ncertificates:\n  - {domains: [a.example.com], source: Vendor, solver: Records, backup_source: \"\"}\n", true)
	if c, err := f.svc.load(t.Context(), f.db, list[0].ID); err != nil || c.BackupProvider != 0 {
		t.Errorf("removed %+v, %v", c, err)
	}
}
