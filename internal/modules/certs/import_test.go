package certs

import (
	"encoding/base64"
	"net/http"
	"slices"
	"testing"
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
