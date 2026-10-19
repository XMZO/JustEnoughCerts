package certs

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestImport(t *testing.T) {
	f := newFixture(t)
	a, b := f.leaf(60, "a.example.com"), f.leaf(80, "b.example.com", "www.b.example.com")
	again := f.leaf(50, "again.example.com")
	f.add(again)
	alone := f.leaf(40, "alone.example.com")
	file := func(name, content string) map[string]string {
		return map[string]string{"name": name, "content": content}
	}
	var out struct {
		Certificates []ImportedBody
		Files        []FileProblemBody
	}
	f.call(f.api.Post("/certificates/import", map[string]any{"files": []map[string]string{
		file("a.crt", base64.StdEncoding.EncodeToString([]byte(a.cert))),
		file("a.key", base64.StdEncoding.EncodeToString([]byte(a.key))),
		file("b.pem", b.cert+b.key), // PEM text as it is
		file("again.pem", again.cert+again.key),
		file("alone.crt", alone.cert),
		file("notes.txt", "nothing here"),
	}, "renewBeforeDays": 20}), http.StatusOK, &out)

	results := map[string]ImportedBody{}
	for _, c := range out.Certificates {
		results[c.Domains[0]] = c
	}
	if r := results["a.example.com"]; r.Result != "added" || r.ID == 0 {
		t.Errorf("a: %+v", r)
	}
	if r := results["b.example.com"]; r.Result != "added" || !slices.Equal(r.Domains, []string{"b.example.com", "www.b.example.com"}) {
		t.Errorf("b: %+v", r)
	}
	if r := results["again.example.com"]; r.Result != "duplicate" || r.ID == 0 {
		t.Errorf("again: %+v", r)
	}
	if r := results["alone.example.com"]; r.Result != "no_key" || r.ID != 0 {
		t.Errorf("alone: %+v", r)
	}
	if len(out.Certificates) != 4 || len(out.Files) != 1 || out.Files[0].Name != "notes.txt" || out.Files[0].Code != "certs.error.no_certificate" {
		t.Errorf("certificates %d, files %+v", len(out.Certificates), out.Files)
	}
	// Each added one is uploaded, with the window asked for and its chain.
	c := f.certificate(results["a.example.com"].ID)
	if c.Source != "upload" || c.RenewBeforeDays != 20 || c.Current == nil {
		t.Errorf("added %+v", c)
	}
}

func TestExportAll(t *testing.T) {
	f := newFixture(t)
	f.add(f.leaf(60, "*.example.com", "example.com"))
	f.add(f.leaf(80, "shop.example.org"))
	f.issueNew("pending.example.net") // no version yet: left out
	f.apiError(f.api.Post("/certificates/export", map[string]any{"password": "wrong"}), http.StatusUnprocessableEntity)
	before := len(f.events())

	resp := f.api.Post("/certificates/export", map[string]any{"password": testPassword})
	if resp.Code != http.StatusOK || resp.Header().Get("Content-Type") != "application/zip" ||
		!strings.HasPrefix(resp.Header().Get("Content-Disposition"), `attachment; filename="jec-certificates_`) {
		t.Fatalf("export: %d %v", resp.Code, resp.Header())
	}
	zr, err := zip.NewReader(bytes.NewReader(resp.Body.Bytes()), int64(resp.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, zf := range zr.File {
		names = append(names, zf.Name)
	}
	slices.Sort(names)
	end1, end2 := f.now.Add(60*day).Format(time.DateOnly), f.now.Add(80*day).Format(time.DateOnly)
	want := []string{
		"shop.example.org_" + end2 + "/cert.pem", "shop.example.org_" + end2 + "/chain.pem",
		"shop.example.org_" + end2 + "/fullchain.pem", "shop.example.org_" + end2 + "/privkey.pem",
		"star.example.com_" + end1 + "/cert.pem", "star.example.com_" + end1 + "/chain.pem",
		"star.example.com_" + end1 + "/fullchain.pem", "star.example.com_" + end1 + "/privkey.pem",
	}
	if !slices.Equal(names, want) {
		t.Errorf("files %v", names)
	}
	// Each key that left is in the audit trail.
	if n := len(f.events()) - before; n != 2 {
		t.Errorf("%d events", n)
	}
}
