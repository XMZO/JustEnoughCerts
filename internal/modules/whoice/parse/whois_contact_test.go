package parse

import (
	"context"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

func TestWHOISRegistrantEmailDropsRedactionSentence(t *testing.T) {
	raw := model.RawResponse{Source: model.SourceWHOIS, Server: "whois.nic.cat", Body: "Domain Name: kotori.cat\n" +
		"Registrant Country: CN\n" +
		"Registrant Email: Please query the Whois service of the Registrar of Record identified in this output for information on how to contact the Registrant, Admin, or Tech contact of the queried domain name.\n"}
	part, err := (WHOISParser{}).Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "kotori.cat"})
	if err != nil {
		t.Fatal(err)
	}
	if part.Registrant.Email != "" || part.Registrant.Country != "CN" {
		t.Fatalf("registrant: %#v", part.Registrant)
	}
	for _, keep := range []string{"owner@example.cat", "https://whois.example/contact/kotori.cat"} {
		if got := contactEmail(keep); got != keep {
			t.Fatalf("contactEmail(%q) = %q", keep, got)
		}
	}
}

func TestContactPhoneDropsPlaceholders(t *testing.T) {
	for value, want := range map[string]string{"+000000000": "", "+0.0000000000": "", "+216.71840721": "+216.71840721", "": ""} {
		if got := contactPhone(value); got != want {
			t.Fatalf("contactPhone(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestWHOISQueriedObjectDoesNotExistIsUnregistered(t *testing.T) {
	raw := model.RawResponse{Source: model.SourceWHOIS, Server: "whois.nic.gal", Query: "lolicon.gal", Body: "The queried object does not exist: no matching objects found\n>>> Last update of Whois database: 2026-10-20T11:21:36.485Z <<<\n"}
	part, err := (WHOISParser{}).Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "lolicon.gal"})
	if err != nil {
		t.Fatal(err)
	}
	if part.Status != model.StatusUnregistered {
		t.Fatalf("status: %q", part.Status)
	}
}

func TestWHOISNoObjectFoundIsUnregistered(t *testing.T) {
	raw := model.RawResponse{Source: model.SourceWHOIS, Server: "registry.ps", Query: "adadaw.ps", Body: "Domain Name: adadaw.ps\nDomain Status: No Object Found\nURL of the ICANN Whois Inaccuracy Complaint Form: https://www.icann.org/wicf/\n"}
	part, err := (WHOISParser{}).Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "adadaw.ps"})
	if err != nil {
		t.Fatal(err)
	}
	if part.Status != model.StatusUnregistered {
		t.Fatalf("status: %q", part.Status)
	}
	for _, status := range part.Statuses {
		t.Fatalf("a miss should not report domain statuses, got %#v", status)
	}
}

func TestWHOISPlaceholderRegistrarMeansReserved(t *testing.T) {
	for name, want := range map[string]model.ResultStatus{
		"NIC Reserved ":        model.StatusReserved,
		"Registry Reserved":    model.StatusReserved,
		"Markmonitor":          model.StatusRegistered,
		"Reserved Hosting Ltd": model.StatusRegistered,
	} {
		body := "Domain Name: ac.cd\nCreation Date: 2021-01-15T00:00:00.0Z\nRegistrar: " + name + "\nDomain Status: inactive https://icann.org/epp#inactive\n"
		raw := model.RawResponse{Source: model.SourceWHOIS, Server: "whois.nic.cd", Query: "ac.cd", Body: body}
		part, err := (WHOISParser{}).Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "ac.cd"})
		if err != nil {
			t.Fatal(err)
		}
		got := part.Status
		if got == model.StatusUnknown {
			got = model.StatusRegistered
		}
		if got != want {
			t.Fatalf("registrar %q: status %q, want %q", name, part.Status, want)
		}
	}
}

func TestWHOISRegistryMissPhrasings(t *testing.T) {
	for server, body := range map[string]string{
		"whois.cnnic.cn":           "No matching record.\n",
		"whois.domain-registry.nl": "zwsemxnvqex7q.nl is free\n",
		"whois.twnic.net.tw":       "No Found\n",
		"whois.nic.ar":             "El dominio no se encuentra registrado en NIC Argentina\n",
		"whois.cctld.by":           "object does not exist\n",
		"whois.nic.tm":             "Domain zwsemxnvqex7q.tm is available for purchase\nPlease visit http://www.nic.tm/ for more information\n",
	} {
		raw := model.RawResponse{Source: model.SourceWHOIS, Server: server, Query: "x.test", Body: body}
		part, err := (WHOISParser{}).Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "x.test"})
		if err != nil {
			t.Fatal(err)
		}
		if part.Status != model.StatusUnregistered {
			t.Fatalf("%s: status %q for %q", server, part.Status, body)
		}
	}
	// A registered record that mentions "free" in prose stays registered.
	raw := model.RawResponse{Source: model.SourceWHOIS, Query: "x.nl", Body: "Domain name: x.nl\nStatus: active\nRegistrar: Free Software Hosting B.V.\n"}
	part, _ := (WHOISParser{}).Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "x.nl"})
	if part.Status == model.StatusUnregistered {
		t.Fatal("registered record misread as free")
	}
}

func TestWHOISILNoDataIsUnregistered(t *testing.T) {
	raw := model.RawResponse{Source: model.SourceWHOIS, Server: "whois.isoc.org.il", Query: "x.co.il", Body: "% By submitting this query, you agree to abide by this policy.\n \n% No data was found to match the request criteria.\n"}
	part, err := (WHOISParser{}).Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "x.co.il"})
	if err != nil || part.Status != model.StatusUnregistered {
		t.Fatalf("status %q err %v", part.Status, err)
	}
	if got := dayMonthYear("05-06-1999"); got != "1999-06-05" {
		t.Fatalf("dayMonthYear: %q", got)
	}
}

func TestShortUnrecognizedWHOISReplyIsAnError(t *testing.T) {
	raw := model.RawResponse{Source: model.SourceWHOIS, Server: "whois.hkirc.hk", Query: "x.com.hk", Body: "CMM174\n"}
	if _, err := (WHOISParser{}).Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "x.com.hk"}); err == nil {
		t.Fatal("an unrecognized short reply must not count as a record")
	}
	for _, body := range []string{"No matching record.\n", "No Found\n", "The domain has not been registered.\n"} {
		raw.Body = body
		part, err := (WHOISParser{}).Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "x.com.hk"})
		if err != nil || part.Status != model.StatusUnregistered {
			t.Fatalf("%q: %v %v", body, err, part)
		}
	}
}

func TestPlaceholderNameserversAreDropped(t *testing.T) {
	body := "Domain Name: kun.uz\nName Server: daniella.ns.cloudflare.com.\nName Server: jeremy.ns.cloudflare.com.\nName Server: not.defined.\nName Server: not.defined.\n"
	raw := model.RawResponse{Source: model.SourceWHOIS, Server: "whois.cctld.uz", Query: "kun.uz", Body: body}
	for _, parser := range []Parser{WHOISParser{}, UZWHOISParser{}} {
		part, err := parser.Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "kun.uz"})
		if err != nil {
			t.Fatal(err)
		}
		var hosts []string
		for _, ns := range part.Nameservers {
			hosts = append(hosts, ns.Host)
		}
		if len(hosts) != 2 || hosts[0] != "daniella.ns.cloudflare.com" || hosts[1] != "jeremy.ns.cloudflare.com" {
			t.Fatalf("%T nameservers: %v", parser, hosts)
		}
	}
}

func TestRDAPPlaceholderNameserversAreDropped(t *testing.T) {
	body := `{"objectClassName":"domain","ldhName":"kun.uz","nameservers":[{"objectClassName":"nameserver","ldhName":"daniella.ns.cloudflare.com"},{"objectClassName":"nameserver","ldhName":"not.defined"}]}`
	raw := model.RawResponse{Source: model.SourceRDAP, Server: "https://rdap.cctld.uz", Query: "kun.uz", Body: body, StatusCode: 200}
	part, err := (RDAPParser{}).Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "kun.uz"})
	if err != nil {
		t.Fatal(err)
	}
	if len(part.Nameservers) != 1 || part.Nameservers[0].Host != "daniella.ns.cloudflare.com" {
		t.Fatalf("nameservers: %#v", part.Nameservers)
	}
}

func TestWHOISRegistrarContact(t *testing.T) {
	body := "Domain Name: linux.sb\nRegistrar: DNS11 GROUP\nRegistrar Street Address: 6th Floor, No. 2 Jinfeng Road\nRegistrar City: Yunfu\nRegistrar State: GD\nRegistrar Postal Code: 527300\nRegistrar Country: CN\nRegistrar Phone: +86.400 788 4688\nRegistrar Email: domains@dns11.net\nRegistrar Abuse Contact Email: \n"
	raw := model.RawResponse{Source: model.SourceWHOIS, Server: "whois.nic.net.sb", Query: "linux.sb", Body: body}
	part, err := (WHOISParser{}).Parse(context.Background(), raw, model.NormalizedQuery{Type: model.QueryDomain, Query: "linux.sb"})
	if err != nil {
		t.Fatal(err)
	}
	r := part.Registrar
	if r.Email != "domains@dns11.net" || r.Phone != "+86.400 788 4688" || r.Address != "6th Floor, No. 2 Jinfeng Road, Yunfu, GD, 527300" || r.Country != "CN" {
		t.Fatalf("registrar contact: %#v", r)
	}
	if part.Registrant.Email != "" || part.Registrant.Name != "" {
		t.Fatalf("registrar details must not become registrant data: %#v", part.Registrant)
	}
}
