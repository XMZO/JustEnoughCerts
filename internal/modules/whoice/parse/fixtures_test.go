package parse

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

type parserExpected struct {
	Status             model.ResultStatus  `json:"status"`
	DomainName         string              `json:"domainName"`
	RegistrarName      string              `json:"registrarName"`
	RegistrarURL       string              `json:"registrarUrl"`
	RegistrarIanaID    string              `json:"registrarIanaId"`
	CreatedAt          string              `json:"createdAt"`
	ExpiresAt          string              `json:"expiresAt"`
	UpdatedAt          string              `json:"updatedAt"`
	Statuses           []string            `json:"statuses"`
	Nameservers        []string            `json:"nameservers"`
	NameserverIPs      map[string][]string `json:"nameserverIps"`
	DNSSEC             string              `json:"dnssec"`
	RegistrantName     string              `json:"registrantName"`
	RegistrantOrg      string              `json:"registrantOrganization"`
	RegistrantCountry  string              `json:"registrantCountry"`
	RegistrantProvince string              `json:"registrantProvince"`
	RegistrantCity     string              `json:"registrantCity"`
	RegistrantAddress  string              `json:"registrantAddress"`
	RegistrantPostal   string              `json:"registrantPostalCode"`
	RegistrantEmail    string              `json:"registrantEmail"`
	RegistrantPhone    string              `json:"registrantPhone"`
	RegistrantExtra    []string            `json:"registrantExtra"`
	NetworkCIDR        string              `json:"networkCidr"`
	NetworkRange       string              `json:"networkRange"`
	NetworkName        string              `json:"networkName"`
	NetworkOriginAS    string              `json:"networkOriginAS"`
}

func TestWHOISParserFixtures(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		expect string
		query  model.NormalizedQuery
	}{
		{
			name:   "registered",
			raw:    fixturePath("whois", "generic", "registered.raw"),
			expect: fixturePath("whois", "generic", "registered.expected.json"),
			query:  domainQuery("example.com"),
		},
		{
			name:   "unregistered",
			raw:    fixturePath("whois", "generic", "unregistered.raw"),
			expect: fixturePath("whois", "generic", "unregistered.expected.json"),
			query:  domainQuery("example-missing.com"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawBody := readString(t, tt.raw)
			expected := readExpected(t, tt.expect)
			part, err := WHOISParser{}.Parse(context.Background(), model.RawResponse{
				Source: model.SourceWHOIS,
				Query:  tt.query.Query,
				Body:   rawBody,
			}, tt.query)
			if err != nil {
				t.Fatal(err)
			}
			assertPartial(t, part, expected)
		})
	}
}

func TestWHOISCompatibilityStatusPatterns(t *testing.T) {
	tests := []struct {
		name string
		body string
		want model.ResultStatus
	}{
		{
			name: "no entries found",
			body: "No entries found for the selected source.",
			want: model.StatusUnregistered,
		},
		{
			name: "no information",
			body: "No information available about domain name.",
			want: model.StatusUnregistered,
		},
		{
			name: "domain available",
			body: "Domain name is available.",
			want: model.StatusUnregistered,
		},
		{
			name: "reserved status",
			body: "Status: not allowed",
			want: model.StatusReserved,
		},
		{
			name: "restricted name",
			body: "Name is restricted by registry policy.",
			want: model.StatusReserved,
		},
		{
			name: "cannot register",
			body: "This domain cannot be registered.",
			want: model.StatusReserved,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part, err := WHOISParser{}.Parse(context.Background(), model.RawResponse{
				Source: model.SourceWHOIS,
				Query:  "example.test",
				Body:   tt.body,
			}, domainQueryFor("example.test", "test"))
			if err != nil {
				t.Fatal(err)
			}
			if part.Status != tt.want {
				t.Fatalf("status: got %q want %q", part.Status, tt.want)
			}
		})
	}
}

func TestWHOISRegistrarURLExtraction(t *testing.T) {
	tests := []struct {
		name              string
		body              string
		wantRegistrarName string
		wantRegistrarURL  string
	}{
		{
			name:              "sponsoring registrar url",
			body:              "Domain Name: EXAMPLE.COM\nRegistrar: Example Registrar\nSponsoring Registrar URL: registrar.example",
			wantRegistrarName: "Example Registrar",
			wantRegistrarURL:  "http://registrar.example",
		},
		{
			name:              "registration service url",
			body:              "Domain Name: EXAMPLE.COM\nRegistrar: Example Registrar\nRegistration Service URL: www.registrar.example/help",
			wantRegistrarName: "Example Registrar",
			wantRegistrarURL:  "https://www.registrar.example/help",
		},
		{
			name:              "registrar parenthesized url",
			body:              "Domain Name: EXAMPLE.COM\nRegistrar: Example Registrar (https://registrar.example)",
			wantRegistrarName: "Example Registrar",
			wantRegistrarURL:  "https://registrar.example",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part, err := WHOISParser{}.Parse(context.Background(), model.RawResponse{
				Source: model.SourceWHOIS,
				Query:  "example.com",
				Body:   tt.body,
			}, domainQuery("example.com"))
			if err != nil {
				t.Fatal(err)
			}
			if part.Registrar.Name != tt.wantRegistrarName {
				t.Fatalf("registrar name: got %q want %q", part.Registrar.Name, tt.wantRegistrarName)
			}
			if part.Registrar.URL != tt.wantRegistrarURL {
				t.Fatalf("registrar url: got %q want %q", part.Registrar.URL, tt.wantRegistrarURL)
			}
		})
	}
}

func TestAdditionalTLDReservedPatterns(t *testing.T) {
	tests := []struct {
		name   string
		parser Parser
		query  model.NormalizedQuery
		body   string
		want   model.ResultStatus
	}{
		{
			name:   "qa reserved",
			parser: QAWHOISParser{},
			query:  domainQueryFor("reserved.qa", "qa"),
			body:   "Domain Name: reserved.qa\nThis domain is reserved by QDR.",
			want:   model.StatusReserved,
		},
		{
			name:   "qa not available reserved",
			parser: QAWHOISParser{},
			query:  domainQueryFor("qa.qa", "qa"),
			body:   "Domain Name: qa.qa\nThis domain is not available.",
			want:   model.StatusReserved,
		},
		{
			name:   "lu reserved",
			parser: LUWHOISParser{},
			query:  domainQueryFor("reserved.lu", "lu"),
			body:   "domainname: reserved.lu\ndomaintype: reserved\n",
			want:   model.StatusReserved,
		},
		{
			name:   "am reserved",
			parser: AMWHOISParser{},
			query:  domainQueryFor("reserved.am", "am"),
			body:   "Domain name: reserved.am\nreserved name\n",
			want:   model.StatusReserved,
		},
		{
			name:   "lt blocked reserved",
			parser: LTWHOISParser{},
			query:  domainQueryFor("reserved.lt", "lt"),
			body:   "domain: reserved.lt\nstatus:\t\t\tblocked\n",
			want:   model.StatusReserved,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part, err := tt.parser.Parse(context.Background(), model.RawResponse{
				Source: model.SourceWHOIS,
				Query:  tt.query.Query,
				Body:   tt.body,
			}, tt.query)
			if err != nil {
				t.Fatal(err)
			}
			if part.Status != tt.want {
				t.Fatalf("status: got %q want %q", part.Status, tt.want)
			}
			if !part.Domain.Reserved {
				t.Fatalf("expected reserved domain: %#v", part.Domain)
			}
		})
	}
}

func TestTLDWHOISParserFixtures(t *testing.T) {
	registry := NewRegistry(WHOISParser{}, UKWHOISParser{}, JPWHOISParser{}, FRWHOISParser{}, CNWHOISParser{}, BRWHOISParser{}, ITWHOISParser{}, EUWHOISParser{}, BEWHOISParser{}, PLWHOISParser{}, CZWHOISParser{}, HUWHOISParser{}, SKWHOISParser{}, ROWHOISParser{}, MXWHOISParser{}, PTWHOISParser{}, QAWHOISParser{}, MDWHOISParser{}, LUWHOISParser{}, LVWHOISParser{}, AMWHOISParser{}, AXWHOISParser{}, BDWHOISParser{}, BNWHOISParser{}, GGWHOISParser{}, ILWHOISParser{}, LTWHOISParser{}, MOWHOISParser{}, RSWHOISParser{}, STWHOISParser{}, TNWHOISParser{}, UZWHOISParser{}, DEWHOISParser{}, NLWHOISParser{}, CAWHOISParser{}, AUWHOISParser{}, SEWHOISParser{}, FIWHOISParser{}, KRWHOISParser{}, ATWHOISParser{}, RUWHOISParser{}, EEWHOISParser{}, BGWHOISParser{}, KGWHOISParser{}, TRWHOISParser{}, HKWHOISParser{}, TWWHOISParser{}, SIWHOISParser{}, UAWHOISParser{}, IDWHOISParser{}, KZWHOISParser{})
	tests := []struct {
		name   string
		raw    string
		expect string
		query  model.NormalizedQuery
	}{
		{
			name:   "uk",
			raw:    fixturePath("whois", "uk", "registered.raw"),
			expect: fixturePath("whois", "uk", "registered.expected.json"),
			query:  domainQueryFor("example.uk", "uk"),
		},
		{
			name:   "jp",
			raw:    fixturePath("whois", "jp", "registered.raw"),
			expect: fixturePath("whois", "jp", "registered.expected.json"),
			query:  domainQueryFor("example.jp", "jp"),
		},
		{
			name:   "fr",
			raw:    fixturePath("whois", "fr", "registered.raw"),
			expect: fixturePath("whois", "fr", "registered.expected.json"),
			query:  domainQueryFor("example.fr", "fr"),
		},
		{
			name:   "cn",
			raw:    fixturePath("whois", "cn", "registered.raw"),
			expect: fixturePath("whois", "cn", "registered.expected.json"),
			query:  domainQueryFor("example.cn", "cn"),
		},
		{
			name:   "br",
			raw:    fixturePath("whois", "br", "registered.raw"),
			expect: fixturePath("whois", "br", "registered.expected.json"),
			query:  domainQueryFor("example.br", "br"),
		},
		{
			name:   "it",
			raw:    fixturePath("whois", "it", "registered.raw"),
			expect: fixturePath("whois", "it", "registered.expected.json"),
			query:  domainQueryFor("example.it", "it"),
		},
		{
			name:   "eu",
			raw:    fixturePath("whois", "eu", "registered.raw"),
			expect: fixturePath("whois", "eu", "registered.expected.json"),
			query:  domainQueryFor("example.eu", "eu"),
		},
		{
			name:   "be",
			raw:    fixturePath("whois", "be", "registered.raw"),
			expect: fixturePath("whois", "be", "registered.expected.json"),
			query:  domainQueryFor("example.be", "be"),
		},
		{
			name:   "pl",
			raw:    fixturePath("whois", "pl", "registered.raw"),
			expect: fixturePath("whois", "pl", "registered.expected.json"),
			query:  domainQueryFor("example.pl", "pl"),
		},
		{
			name:   "cz",
			raw:    fixturePath("whois", "cz", "registered.raw"),
			expect: fixturePath("whois", "cz", "registered.expected.json"),
			query:  domainQueryFor("example.cz", "cz"),
		},
		{
			name:   "hu",
			raw:    fixturePath("whois", "hu", "registered.raw"),
			expect: fixturePath("whois", "hu", "registered.expected.json"),
			query:  domainQueryFor("example.hu", "hu"),
		},
		{
			name:   "sk",
			raw:    fixturePath("whois", "sk", "registered.raw"),
			expect: fixturePath("whois", "sk", "registered.expected.json"),
			query:  domainQueryFor("example.sk", "sk"),
		},
		{
			name:   "ro",
			raw:    fixturePath("whois", "ro", "registered.raw"),
			expect: fixturePath("whois", "ro", "registered.expected.json"),
			query:  domainQueryFor("example.ro", "ro"),
		},
		{
			name:   "mx",
			raw:    fixturePath("whois", "mx", "registered.raw"),
			expect: fixturePath("whois", "mx", "registered.expected.json"),
			query:  domainQueryFor("example.mx", "mx"),
		},
		{
			name:   "pt",
			raw:    fixturePath("whois", "pt", "registered.raw"),
			expect: fixturePath("whois", "pt", "registered.expected.json"),
			query:  domainQueryFor("example.pt", "pt"),
		},
		{
			name:   "qa",
			raw:    fixturePath("whois", "qa", "registered.raw"),
			expect: fixturePath("whois", "qa", "registered.expected.json"),
			query:  domainQueryFor("example.qa", "qa"),
		},
		{
			name:   "md",
			raw:    fixturePath("whois", "md", "registered.raw"),
			expect: fixturePath("whois", "md", "registered.expected.json"),
			query:  domainQueryFor("example.md", "md"),
		},
		{
			name:   "lu",
			raw:    fixturePath("whois", "lu", "registered.raw"),
			expect: fixturePath("whois", "lu", "registered.expected.json"),
			query:  domainQueryFor("example.lu", "lu"),
		},
		{
			name:   "lv",
			raw:    fixturePath("whois", "lv", "registered.raw"),
			expect: fixturePath("whois", "lv", "registered.expected.json"),
			query:  domainQueryFor("example.lv", "lv"),
		},
		{
			name:   "am",
			raw:    fixturePath("whois", "am", "registered.raw"),
			expect: fixturePath("whois", "am", "registered.expected.json"),
			query:  domainQueryFor("example.am", "am"),
		},
		{
			name:   "ax",
			raw:    fixturePath("whois", "ax", "registered.raw"),
			expect: fixturePath("whois", "ax", "registered.expected.json"),
			query:  domainQueryFor("example.ax", "ax"),
		},
		{
			name:   "bd",
			raw:    fixturePath("whois", "bd", "registered.raw"),
			expect: fixturePath("whois", "bd", "registered.expected.json"),
			query:  domainQueryFor("example.bd", "bd"),
		},
		{
			name:   "bn",
			raw:    fixturePath("whois", "bn", "registered.raw"),
			expect: fixturePath("whois", "bn", "registered.expected.json"),
			query:  domainQueryFor("example.bn", "bn"),
		},
		{
			name:   "gg",
			raw:    fixturePath("whois", "gg", "registered.raw"),
			expect: fixturePath("whois", "gg", "registered.expected.json"),
			query:  domainQueryFor("example.gg", "gg"),
		},
		{
			name:   "je",
			raw:    fixturePath("whois", "je", "registered.raw"),
			expect: fixturePath("whois", "je", "registered.expected.json"),
			query:  domainQueryFor("example.je", "je"),
		},
		{
			name:   "il",
			raw:    fixturePath("whois", "il", "registered.raw"),
			expect: fixturePath("whois", "il", "registered.expected.json"),
			query:  domainQueryFor("example.il", "il"),
		},
		{
			name:   "lt",
			raw:    fixturePath("whois", "lt", "registered.raw"),
			expect: fixturePath("whois", "lt", "registered.expected.json"),
			query:  domainQueryFor("example.lt", "lt"),
		},
		{
			name:   "mo",
			raw:    fixturePath("whois", "mo", "registered.raw"),
			expect: fixturePath("whois", "mo", "registered.expected.json"),
			query:  domainQueryFor("example.mo", "mo"),
		},
		{
			name:   "rs",
			raw:    fixturePath("whois", "rs", "registered.raw"),
			expect: fixturePath("whois", "rs", "registered.expected.json"),
			query:  domainQueryFor("example.rs", "rs"),
		},
		{
			name:   "st",
			raw:    fixturePath("whois", "st", "registered.raw"),
			expect: fixturePath("whois", "st", "registered.expected.json"),
			query:  domainQueryFor("example.st", "st"),
		},
		{
			name:   "tn",
			raw:    fixturePath("whois", "tn", "registered.raw"),
			expect: fixturePath("whois", "tn", "registered.expected.json"),
			query:  domainQueryFor("example.tn", "tn"),
		},
		{
			name:   "uz",
			raw:    fixturePath("whois", "uz", "registered.raw"),
			expect: fixturePath("whois", "uz", "registered.expected.json"),
			query:  domainQueryFor("example.uz", "uz"),
		},
		{
			name:   "de",
			raw:    fixturePath("whois", "de", "registered.raw"),
			expect: fixturePath("whois", "de", "registered.expected.json"),
			query:  domainQueryFor("example.de", "de"),
		},
		{
			name:   "nl",
			raw:    fixturePath("whois", "nl", "registered.raw"),
			expect: fixturePath("whois", "nl", "registered.expected.json"),
			query:  domainQueryFor("example.nl", "nl"),
		},
		{
			name:   "ca",
			raw:    fixturePath("whois", "ca", "registered.raw"),
			expect: fixturePath("whois", "ca", "registered.expected.json"),
			query:  domainQueryFor("example.ca", "ca"),
		},
		{
			name:   "au",
			raw:    fixturePath("whois", "au", "registered.raw"),
			expect: fixturePath("whois", "au", "registered.expected.json"),
			query:  domainQueryFor("example.au", "au"),
		},
		{
			name:   "se",
			raw:    fixturePath("whois", "se", "registered.raw"),
			expect: fixturePath("whois", "se", "registered.expected.json"),
			query:  domainQueryFor("example.se", "se"),
		},
		{
			name:   "fi",
			raw:    fixturePath("whois", "fi", "registered.raw"),
			expect: fixturePath("whois", "fi", "registered.expected.json"),
			query:  domainQueryFor("example.fi", "fi"),
		},
		{
			name:   "kr",
			raw:    fixturePath("whois", "kr", "registered.raw"),
			expect: fixturePath("whois", "kr", "registered.expected.json"),
			query:  domainQueryFor("example.kr", "kr"),
		},
		{
			name:   "at",
			raw:    fixturePath("whois", "at", "registered.raw"),
			expect: fixturePath("whois", "at", "registered.expected.json"),
			query:  domainQueryFor("example.at", "at"),
		},
		{
			name:   "ru",
			raw:    fixturePath("whois", "ru", "registered.raw"),
			expect: fixturePath("whois", "ru", "registered.expected.json"),
			query:  domainQueryFor("example.ru", "ru"),
		},
		{
			name:   "ee",
			raw:    fixturePath("whois", "ee", "registered.raw"),
			expect: fixturePath("whois", "ee", "registered.expected.json"),
			query:  domainQueryFor("example.ee", "ee"),
		},
		{
			name:   "bg",
			raw:    fixturePath("whois", "bg", "registered.raw"),
			expect: fixturePath("whois", "bg", "registered.expected.json"),
			query:  domainQueryFor("example.bg", "bg"),
		},
		{
			name:   "kg",
			raw:    fixturePath("whois", "kg", "registered.raw"),
			expect: fixturePath("whois", "kg", "registered.expected.json"),
			query:  domainQueryFor("example.kg", "kg"),
		},
		{
			name:   "tr",
			raw:    fixturePath("whois", "tr", "registered.raw"),
			expect: fixturePath("whois", "tr", "registered.expected.json"),
			query:  domainQueryFor("example.tr", "tr"),
		},
		{
			name:   "hk",
			raw:    fixturePath("whois", "hk", "registered.raw"),
			expect: fixturePath("whois", "hk", "registered.expected.json"),
			query:  domainQueryFor("example.hk", "hk"),
		},
		{
			name:   "tw",
			raw:    fixturePath("whois", "tw", "registered.raw"),
			expect: fixturePath("whois", "tw", "registered.expected.json"),
			query:  domainQueryFor("example.tw", "tw"),
		},
		{
			name:   "si",
			raw:    fixturePath("whois", "si", "registered.raw"),
			expect: fixturePath("whois", "si", "registered.expected.json"),
			query:  domainQueryFor("example.si", "si"),
		},
		{
			name:   "ua",
			raw:    fixturePath("whois", "ua", "registered.raw"),
			expect: fixturePath("whois", "ua", "registered.expected.json"),
			query:  domainQueryFor("example.ua", "ua"),
		},
		{
			name:   "id",
			raw:    fixturePath("whois", "id", "registered.raw"),
			expect: fixturePath("whois", "id", "registered.expected.json"),
			query:  domainQueryFor("example.id", "id"),
		},
		{
			name:   "kz",
			raw:    fixturePath("whois", "kz", "registered.raw"),
			expect: fixturePath("whois", "kz", "registered.expected.json"),
			query:  domainQueryFor("example.kz", "kz"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawBody := readString(t, tt.raw)
			expected := readExpected(t, tt.expect)
			part, err := registry.Parse(context.Background(), model.RawResponse{
				Source: model.SourceWHOIS,
				Query:  tt.query.Query,
				Body:   rawBody,
			}, tt.query)
			if err != nil {
				t.Fatal(err)
			}
			assertPartial(t, part, expected)
		})
	}
}

func TestWHOISWebNoticeIsUnknown(t *testing.T) {
	q := domainQueryFor("example.ao", "ao")
	part, err := WHOISParser{}.Parse(context.Background(), model.RawResponse{
		Source: model.SourceWHOISWeb,
		Query:  q.Query,
		Body:   "WHOIS Web fallback notice: Please visit https://www.dns.ao/ao/whois/ for example.ao.",
	}, q)
	if err != nil {
		t.Fatal(err)
	}
	if part.Status != model.StatusUnknown {
		t.Fatalf("status: got %q want %q", part.Status, model.StatusUnknown)
	}
	if part.Raw.WHOISWeb == "" {
		t.Fatal("expected raw WHOIS Web payload")
	}
}

func TestRDAPParserFixtures(t *testing.T) {
	rawBody := readString(t, fixturePath("rdap", "domain", "registered.json"))
	expected := readExpected(t, fixturePath("rdap", "domain", "registered.expected.json"))
	q := domainQuery("example.com")
	part, err := RDAPParser{}.Parse(context.Background(), model.RawResponse{
		Source:     model.SourceRDAP,
		Query:      q.Query,
		Body:       rawBody,
		StatusCode: 200,
		Server:     "https://rdap.example/domain/example.com",
	}, q)
	if err != nil {
		t.Fatal(err)
	}
	assertPartial(t, part, expected)
}

func TestRDAPParserNetworkTypes(t *testing.T) {
	tests := []struct {
		name string
		q    model.NormalizedQuery
		body string
		want parserExpected
	}{
		{
			name: "ipv6",
			q: model.NormalizedQuery{
				Input: "2001:db8::1",
				Query: "2001:db8::1",
				Type:  model.QueryIPv6,
			},
			body: `{"objectClassName":"ip network","startAddress":"2001:db8::","endAddress":"2001:db8::ffff","name":"EXAMPLE-V6","type":"ALLOCATED-BY-RIR","country":"ZZ","status":["active"]}`,
			want: parserExpected{
				Status:       model.StatusRegistered,
				NetworkCIDR:  "2001:db8::-2001:db8::ffff",
				NetworkRange: "2001:db8:: - 2001:db8::ffff",
				NetworkName:  "EXAMPLE-V6",
				Statuses:     []string{"active"},
			},
		},
		{
			name: "cidr",
			q: model.NormalizedQuery{
				Input: "2001:db8::/32",
				Query: "2001:db8::/32",
				Type:  model.QueryCIDR,
			},
			body: `{"objectClassName":"ip network","startAddress":"2001:db8::","endAddress":"2001:db8:ffff:ffff:ffff:ffff:ffff:ffff","name":"EXAMPLE-CIDR","type":"ALLOCATED-BY-RIR","country":"ZZ","status":["active"]}`,
			want: parserExpected{
				Status:       model.StatusRegistered,
				NetworkCIDR:  "2001:db8::/32",
				NetworkRange: "2001:db8:: - 2001:db8:ffff:ffff:ffff:ffff:ffff:ffff",
				NetworkName:  "EXAMPLE-CIDR",
				Statuses:     []string{"active"},
			},
		},
		{
			name: "asn",
			q: model.NormalizedQuery{
				Input: "AS64496",
				Query: "AS64496",
				Type:  model.QueryASN,
				ASN:   64496,
			},
			body: `{"objectClassName":"autnum","startAutnum":64496,"endAutnum":64496,"name":"EXAMPLE-AS","country":"ZZ","status":["active"]}`,
			want: parserExpected{
				Status:          model.StatusRegistered,
				NetworkName:     "EXAMPLE-AS",
				NetworkOriginAS: "AS64496",
				Statuses:        []string{"active"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part, err := RDAPParser{}.Parse(context.Background(), model.RawResponse{
				Source:     model.SourceRDAP,
				Query:      tt.q.Query,
				Body:       tt.body,
				StatusCode: 200,
				Server:     "https://rdap.example.test",
			}, tt.q)
			if err != nil {
				t.Fatal(err)
			}
			assertPartial(t, part, tt.want)
		})
	}
}

func TestRDAPRegistrarURLExtraction(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "registrar website link",
			body: `{"objectClassName":"domain","ldhName":"EXAMPLE.COM","entities":[{"roles":["registrar"],"vcardArray":["vcard",[["fn",{},"text","Example Registrar"]]],"links":[{"title":"Registrar's Website","href":"registrar.example"}]}]}`,
			want: "http://registrar.example",
		},
		{
			name: "entity url",
			body: `{"objectClassName":"domain","ldhName":"EXAMPLE.COM","entities":[{"roles":["registrar"],"handle":"EXAMPLE","url":"https://registrar.example"}]}`,
			want: "https://registrar.example",
		},
		{
			name: "skip rdap api self link",
			body: `{"objectClassName":"domain","ldhName":"EXAMPLE.COM","entities":[{"roles":["registrar"],"handle":"EXAMPLE","links":[{"rel":"self","type":"application/rdap+json","href":"https://rdap.example/entity/EXAMPLE"},{"href":"https://registrar.example"}]}]}`,
			want: "https://registrar.example",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part, err := RDAPParser{}.Parse(context.Background(), model.RawResponse{
				Source:     model.SourceRDAP,
				Query:      "example.com",
				Body:       tt.body,
				StatusCode: 200,
				Server:     "https://rdap.example.test/domain/example.com",
			}, domainQuery("example.com"))
			if err != nil {
				t.Fatal(err)
			}
			if part.Registrar.URL != tt.want {
				t.Fatalf("registrar url: got %q want %q", part.Registrar.URL, tt.want)
			}
		})
	}
}

func TestRDAPCompatibilityEdges(t *testing.T) {
	tests := []struct {
		name string
		body string
		want parserExpected
	}{
		{
			name: "reserved variant",
			body: `{"objectClassName":"domain","ldhName":"RESERVED.EXAMPLE","variants":[{"relations":["RESTRICTED_REGISTRATION"]}],"description":["This label has usage restrictions"]}`,
			want: parserExpected{Status: model.StatusReserved},
		},
		{
			name: "string dnssec and extra date actions",
			body: `{"objectClassName":"domain","ldhName":"EXAMPLE.KG","secureDNS":{"delegationSigned":"true"},"events":[{"eventAction":"registration","eventDate":"2021-01-02T03:04:05Z"},{"eventAction":"record expires","eventDate":"2027-01-02T03:04:05Z"},{"eventAction":"last modified","eventDate":"2026-01-02T03:04:05Z"}]}`,
			want: parserExpected{
				Status:    model.StatusRegistered,
				CreatedAt: "2021-01-02T03:04:05Z",
				ExpiresAt: "2027-01-02T03:04:05Z",
				UpdatedAt: "2026-01-02T03:04:05Z",
				DNSSEC:    "signed",
			},
		},
		{
			name: "nested registrar abuse entity and address array",
			body: `{"objectClassName":"domain","ldhName":"EXAMPLE.TLD","entities":[{"roles":["registrar"],"entities":[{"roles":["abuse"],"vcardArray":["vcard",[["fn",{},"text","Nested Registrar"]]]}],"publicIds":[{"type":"IANA Registrar ID","identifier":"9999"}]},{"roles":["registrant"],"vcardArray":["vcard",[["fn",{},"text","Registrant Person"],["org",{},"text","Registrant Org"],["adr",{},"text",["","","123 Test St","Testville","TS","12345","ZZ"]],["email",{},"text","user@example.tld"]]]}],"links":[{"rel":"related","href":"https://rdap.registrar.example/domain/example.tld"}]}`,
			want: parserExpected{
				Status:             model.StatusRegistered,
				RegistrarName:      "Nested Registrar",
				RegistrarIanaID:    "9999",
				RegistrantName:     "Registrant Person",
				RegistrantOrg:      "Registrant Org",
				RegistrantCountry:  "ZZ",
				RegistrantProvince: "TS",
				RegistrantCity:     "Testville",
				RegistrantAddress:  "123 Test St",
				RegistrantPostal:   "12345",
				RegistrantEmail:    "user@example.tld",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part, err := RDAPParser{}.Parse(context.Background(), model.RawResponse{
				Source:     model.SourceRDAP,
				Query:      "example.tld",
				Body:       tt.body,
				StatusCode: 200,
				Server:     "https://rdap.example.test/domain/example.tld",
			}, domainQueryFor("example.tld", "tld"))
			if err != nil {
				t.Fatal(err)
			}
			assertPartial(t, part, tt.want)
		})
	}
}

func TestRDAPParserHTTPErrorDoesNotBecomeRegisteredEvidence(t *testing.T) {
	q := domainQuery("example.com")
	part, err := RDAPParser{}.Parse(context.Background(), model.RawResponse{
		Source:     model.SourceRDAP,
		Query:      q.Query,
		Body:       "temporarily unavailable",
		StatusCode: 500,
		Server:     "https://rdap.example.test/domain/example.com",
	}, q)
	// A failed RDAP request is a source error, so WHOIS or the DNS delegation
	// check decides the status.
	if err == nil || part != nil {
		t.Fatalf("expected an error for RDAP HTTP 500, got %+v", part)
	}
}

func assertPartial(t *testing.T, part *model.PartialResult, expected parserExpected) {
	t.Helper()
	if part.Status != expected.Status {
		t.Fatalf("status: got %q want %q", part.Status, expected.Status)
	}
	if expected.DomainName != "" && part.Domain.Name != expected.DomainName {
		t.Fatalf("domain: got %q want %q", part.Domain.Name, expected.DomainName)
	}
	if expected.RegistrarName != "" && part.Registrar.Name != expected.RegistrarName {
		t.Fatalf("registrar: got %q want %q", part.Registrar.Name, expected.RegistrarName)
	}
	if expected.RegistrarURL != "" && part.Registrar.URL != expected.RegistrarURL {
		t.Fatalf("registrar url: got %q want %q", part.Registrar.URL, expected.RegistrarURL)
	}
	if expected.RegistrarIanaID != "" && part.Registrar.IANAID != expected.RegistrarIanaID {
		t.Fatalf("iana id: got %q want %q", part.Registrar.IANAID, expected.RegistrarIanaID)
	}
	if expected.CreatedAt != "" && part.Dates.CreatedAt != expected.CreatedAt {
		t.Fatalf("created: got %q want %q", part.Dates.CreatedAt, expected.CreatedAt)
	}
	if expected.ExpiresAt != "" && part.Dates.ExpiresAt != expected.ExpiresAt {
		t.Fatalf("expires: got %q want %q", part.Dates.ExpiresAt, expected.ExpiresAt)
	}
	if expected.UpdatedAt != "" && part.Dates.UpdatedAt != expected.UpdatedAt {
		t.Fatalf("updated: got %q want %q", part.Dates.UpdatedAt, expected.UpdatedAt)
	}
	if expected.DNSSEC != "" && part.DNSSEC.Text != expected.DNSSEC {
		t.Fatalf("dnssec: got %q want %q", part.DNSSEC.Text, expected.DNSSEC)
	}
	if expected.RegistrantName != "" && part.Registrant.Name != expected.RegistrantName {
		t.Fatalf("registrant name: got %q want %q", part.Registrant.Name, expected.RegistrantName)
	}
	if expected.RegistrantOrg != "" && part.Registrant.Organization != expected.RegistrantOrg {
		t.Fatalf("registrant organization: got %q want %q", part.Registrant.Organization, expected.RegistrantOrg)
	}
	if expected.RegistrantCountry != "" && part.Registrant.Country != expected.RegistrantCountry {
		t.Fatalf("registrant country: got %q want %q", part.Registrant.Country, expected.RegistrantCountry)
	}
	if expected.RegistrantProvince != "" && part.Registrant.Province != expected.RegistrantProvince {
		t.Fatalf("registrant province: got %q want %q", part.Registrant.Province, expected.RegistrantProvince)
	}
	if expected.RegistrantCity != "" && part.Registrant.City != expected.RegistrantCity {
		t.Fatalf("registrant city: got %q want %q", part.Registrant.City, expected.RegistrantCity)
	}
	if expected.RegistrantAddress != "" && part.Registrant.Address != expected.RegistrantAddress {
		t.Fatalf("registrant address: got %q want %q", part.Registrant.Address, expected.RegistrantAddress)
	}
	if expected.RegistrantPostal != "" && part.Registrant.PostalCode != expected.RegistrantPostal {
		t.Fatalf("registrant postal code: got %q want %q", part.Registrant.PostalCode, expected.RegistrantPostal)
	}
	if expected.RegistrantEmail != "" && part.Registrant.Email != expected.RegistrantEmail {
		t.Fatalf("registrant email: got %q want %q", part.Registrant.Email, expected.RegistrantEmail)
	}
	if expected.RegistrantPhone != "" && part.Registrant.Phone != expected.RegistrantPhone {
		t.Fatalf("registrant phone: got %q want %q", part.Registrant.Phone, expected.RegistrantPhone)
	}
	if len(expected.RegistrantExtra) > 0 {
		if len(part.Registrant.Extra) != len(expected.RegistrantExtra) {
			t.Fatalf("registrant extra: got %d want %d", len(part.Registrant.Extra), len(expected.RegistrantExtra))
		}
		for i, expectedField := range expected.RegistrantExtra {
			got := part.Registrant.Extra[i].Label + "=" + part.Registrant.Extra[i].Value
			if got != expectedField {
				t.Fatalf("registrant extra[%d]: got %q want %q", i, got, expectedField)
			}
		}
	}
	if expected.NetworkCIDR != "" && part.Network.CIDR != expected.NetworkCIDR {
		t.Fatalf("network cidr: got %q want %q", part.Network.CIDR, expected.NetworkCIDR)
	}
	if expected.NetworkRange != "" && part.Network.Range != expected.NetworkRange {
		t.Fatalf("network range: got %q want %q", part.Network.Range, expected.NetworkRange)
	}
	if expected.NetworkName != "" && part.Network.Name != expected.NetworkName {
		t.Fatalf("network name: got %q want %q", part.Network.Name, expected.NetworkName)
	}
	if expected.NetworkOriginAS != "" && part.Network.OriginAS != expected.NetworkOriginAS {
		t.Fatalf("network origin AS: got %q want %q", part.Network.OriginAS, expected.NetworkOriginAS)
	}
	assertStatuses(t, part.Statuses, expected.Statuses)
	assertNameservers(t, part.Nameservers, expected.Nameservers)
	assertNameserverIPs(t, part.Nameservers, expected.NameserverIPs)
}

func assertStatuses(t *testing.T, got []model.DomainStatus, expected []string) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf("statuses: got %d want %d", len(got), len(expected))
	}
	for i, value := range expected {
		if got[i].Code != value {
			t.Fatalf("status[%d]: got %q want %q", i, got[i].Code, value)
		}
	}
}

func assertNameservers(t *testing.T, got []model.Nameserver, expected []string) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf("nameservers: got %d want %d", len(got), len(expected))
	}
	for i, value := range expected {
		if got[i].Host != value {
			t.Fatalf("nameserver[%d]: got %q want %q", i, got[i].Host, value)
		}
	}
}

func assertNameserverIPs(t *testing.T, got []model.Nameserver, expected map[string][]string) {
	t.Helper()
	if len(expected) == 0 {
		return
	}
	byHost := map[string][]string{}
	for _, ns := range got {
		byHost[ns.Host] = ns.Addresses
	}
	for host, want := range expected {
		values := byHost[host]
		if len(values) != len(want) {
			t.Fatalf("nameserver %s addresses: got %#v want %#v", host, values, want)
		}
		for i, value := range want {
			if values[i] != value {
				t.Fatalf("nameserver %s address[%d]: got %q want %q", host, i, values[i], value)
			}
		}
	}
}

func readExpected(t *testing.T, path string) parserExpected {
	t.Helper()
	var expected parserExpected
	if err := json.Unmarshal([]byte(readString(t, path)), &expected); err != nil {
		t.Fatal(err)
	}
	return expected
}

func readString(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func fixturePath(parts ...string) string {
	all := append([]string{"testdata"}, parts...)
	return filepath.Join(all...)
}

func domainQuery(value string) model.NormalizedQuery {
	return domainQueryFor(value, "com")
}

func domainQueryFor(value, suffix string) model.NormalizedQuery {
	return model.NormalizedQuery{
		Input:            value,
		Query:            value,
		UnicodeQuery:     value,
		Type:             model.QueryDomain,
		Host:             value,
		Suffix:           suffix,
		RegisteredDomain: value,
	}
}

func TestWHOISParserReservedWinsAndRejectionsAreErrors(t *testing.T) {
	q := domainQuery("example.com")
	parse := func(body string) (*model.PartialResult, error) {
		return WHOISParser{}.Parse(context.Background(), model.RawResponse{Source: model.SourceWHOIS, Query: q.Query, Body: body, Server: "whois.example.test"}, q)
	}
	for _, body := range []string{
		"The queried object does not exist: \n\u6839\u636e\u6709\u5173\u89c4\u5b9a\uff0c\u8be5\u57df\u540d\u9650\u5236\u6ce8\u518c\u3002\nThis domain name is restricted from registration.",
		"Domain Name: example.com\n\n>>> This name is not available for registration:\n\n>>> Domain name matches a restricted word\n",
		"Error code: 01044\nError message: The domain name requested has usage restrictions applied to it.",
		"This domain is currently not available for registration. Please select other domain.",
		"object pending auction",
	} {
		part, err := parse(body)
		if err != nil || part.Status != model.StatusReserved {
			t.Fatalf("expected reserved for %q, got %+v %v", body, part, err)
		}
	}
	if part, err := parse("Not find MatchingRecord\n\n\u4f7f\u7528\u6761\u6b3e: ..."); err != nil || part.Status != model.StatusUnregistered {
		t.Fatalf("expected unregistered for CONAC, got %+v %v", part, err)
	}
	for _, body := range []string{
		"TLD is not supported.\n>>> Last update of WHOIS database: 2026-10-21T10:36:17Z <<<",
		"Domain Name: x\n>>> This name is not available for registration:\n>>> Tld not supported by this registry interface\n",
		"DOMAIN NAME: x.бг (x.xn--90ae)\nregistration status: invalid",
		"% EURid\n-7: %Invalid pattern",
		"NIC Whois server for cTLDs : .tn\nErreur\tSyntax [ Domain ]",
	} {
		if part, err := parse(body); err == nil {
			t.Fatalf("expected a rejection error for %q, got %+v", body, part)
		}
	}
}
