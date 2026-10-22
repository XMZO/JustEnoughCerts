package parse

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

func TestTNWHOISParser(t *testing.T) {
	body, err := os.ReadFile("testdata/tn-nn.txt")
	if err != nil {
		t.Fatal(err)
	}
	raw := model.RawResponse{Source: model.SourceWHOIS, Server: "whois.ati.tn", Query: "nn.tn", Body: string(body)}
	part, err := (TNWHOISParser{}).Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "nn.tn"})
	if err != nil {
		t.Fatal(err)
	}
	if part.Status != model.StatusRegistered || part.Domain.Name != "nn.tn" || part.Registrar.Name != "SAFOZI" {
		t.Fatalf("status %q domain %q registrar %q", part.Status, part.Domain.Name, part.Registrar.Name)
	}
	if part.Dates.CreatedAt != "2026-06-17T18:55:01+01:00" {
		t.Fatalf("created: %q", part.Dates.CreatedAt)
	}
	var hosts []string
	for _, ns := range part.Nameservers {
		hosts = append(hosts, ns.Host)
	}
	if strings.Join(hosts, ",") != "jeff.ns.cloudflare.com,sloan.ns.cloudflare.com" {
		t.Fatalf("nameservers: %v", hosts)
	}
	r := part.Registrant
	if r.Name != "Raana Kaname" || r.City != "Tunis" || r.Country != "TN" || r.PostalCode != "1004" || r.Email != "Kaname.Raana@outlook.jp" || r.Phone != "+216.71840721" {
		t.Fatalf("registrant: %#v", r)
	}
	if len(part.Statuses) != 1 || part.Statuses[0].Code != "TRANSFER_PROHIBITED" || part.DNSSEC.Text != "unsigned" {
		t.Fatalf("statuses %#v dnssec %q", part.Statuses, part.DNSSEC.Text)
	}
}
