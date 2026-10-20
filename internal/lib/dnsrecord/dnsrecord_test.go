package dnsrecord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// inZone makes every name belong to example.com, as its SOA would say.
func inZone(t *testing.T) {
	t.Helper()
	old, oldDelegated := findZone, delegated
	findZone = func(_ context.Context, name string) (string, error) {
		switch {
		case strings.HasSuffix(name, "example.com"):
			return "example.com", nil
		case strings.HasSuffix(name, "example.net"):
			return "example.net", nil
		}
		return "", errors.New("no zone")
	}
	// _acme-challenge.delegated.example.com is a CNAME into example.net.
	delegated = func(_ context.Context, name string) string {
		if name == "delegated.example.com" {
			return "delegated.acme.example.net"
		}
		return ""
	}
	t.Cleanup(func() { findZone, delegated = old, oldDelegated })
}

var txt = Record{Name: "_dnsauth.www.example.com", Type: "TXT", Value: "2026101900000000abcdef"}

// recorder is a fake API that keeps what each request asked.
type recorder struct {
	mu   sync.Mutex
	got  []map[string]any
	reqs []*http.Request
}

func (rec *recorder) keep(r *http.Request, v map[string]any) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.got = append(rec.got, v)
	rec.reqs = append(rec.reqs, r)
}

func TestSplit(t *testing.T) {
	inZone(t)
	for name, want := range map[string]string{"example.com": "@", "_dnsauth.example.com.": "_dnsauth", "a.b.Example.com": "a.b"} {
		zone, sub, err := split(t.Context(), name)
		if err != nil || zone != "example.com" || sub != want {
			t.Errorf("split(%q) = %q %q %v", name, zone, sub, err)
		}
	}
	if _, _, err := split(t.Context(), "example.org"); err == nil {
		t.Error("split a name of no zone")
	}
}

func TestOpen(t *testing.T) {
	for code, settings := range map[string]map[string]string{
		"route53":      {"AWS_ACCESS_KEY_ID": "a"},
		"tencentcloud": {"TENCENTCLOUD_SECRET_ID": "a"},
		"alidns":       {"ALICLOUD_RAM_ROLE": "role"},
		"cloudflare":   {"CF_API_EMAIL": "me@example.com"},
		"huaweicloud":  {"HUAWEICLOUD_ACCESS_KEY_ID": "a", "HUAWEICLOUD_SECRET_ACCESS_KEY": "b"},
	} {
		if _, err := Open(code, settings); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v", code, err)
		}
	}
}

func TestTencent(t *testing.T) {
	inZone(t)
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var params map[string]any
		_ = json.NewDecoder(r.Body).Decode(&params)
		params["action"] = r.Header.Get("X-TC-Action")
		rec.keep(r, params)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "TC3-HMAC-SHA256 Credential=AKID1/") || r.Header.Get("X-TC-Version") != "2021-03-23" {
			_, _ = io.WriteString(w, `{"Response":{"Error":{"Code":"AuthFailure","Message":"no"},"RequestId":"r"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"Response":{"RecordId":123456789012,"RequestId":"r"}}`)
	}))
	defer srv.Close()
	old, oldScheme := tencentEndpoint, tencentScheme
	tencentEndpoint, tencentScheme = strings.TrimPrefix(srv.URL, "http://"), "HTTP"
	t.Cleanup(func() { tencentEndpoint, tencentScheme = old, oldScheme })

	api, err := Open("tencentcloud", map[string]string{"TENCENTCLOUD_SECRET_ID": "AKID1", "TENCENTCLOUD_SECRET_KEY": "k"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := api.Add(t.Context(), txt)
	if err != nil || id != "example.com/123456789012" {
		t.Fatalf("add: %q, %v", id, err)
	}
	if err := api.Remove(t.Context(), txt, id); err != nil {
		t.Fatal(err)
	}
	add, del := rec.got[0], rec.got[1]
	if add["action"] != "CreateRecord" || add["Domain"] != "example.com" || add["SubDomain"] != "_dnsauth.www" || add["RecordType"] != "TXT" ||
		add["RecordLine"] != lineDefault || add["Value"] != txt.Value || add["TTL"] != float64(600) {
		t.Errorf("created with %v", add)
	}
	if del["action"] != "DeleteRecord" || del["Domain"] != "example.com" || del["RecordId"] != float64(123456789012) {
		t.Errorf("deleted with %v", del)
	}
}

func TestAliyun(t *testing.T) {
	inZone(t)
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		params := map[string]any{"action": r.Header.Get("x-acs-action")}
		for k := range r.Form {
			params[k] = r.Form.Get(k)
		}
		rec.keep(r, params)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"RequestId":"r","RecordId":"9876"}`)
	}))
	defer srv.Close()
	old, oldProtocol := aliyunEndpoint, aliyunProtocol
	aliyunEndpoint, aliyunProtocol = strings.TrimPrefix(srv.URL, "http://"), "HTTP"
	t.Cleanup(func() { aliyunEndpoint, aliyunProtocol = old, oldProtocol })

	api, err := Open("alidns", map[string]string{"ALICLOUD_ACCESS_KEY": "LTAI1", "ALICLOUD_SECRET_KEY": "s", "ALICLOUD_TTL": "60"})
	if err != nil {
		t.Fatal(err)
	}
	cname := Record{Name: "_abc.example.com", Type: "CNAME", Value: "_def.sectigo.com"}
	id, err := api.Add(t.Context(), cname)
	if err != nil || id != "9876" {
		t.Fatalf("add: %q, %v", id, err)
	}
	if err := api.Remove(t.Context(), cname, id); err != nil {
		t.Fatal(err)
	}
	add, del := rec.got[0], rec.got[1]
	if add["action"] != "AddDomainRecord" || add["DomainName"] != "example.com" || add["RR"] != "_abc" || add["Type"] != "CNAME" ||
		add["Value"] != cname.Value || add["TTL"] != "60" || del["action"] != "DeleteDomainRecord" || del["RecordId"] != "9876" {
		t.Errorf("calls %v", rec.got)
	}
}

func TestCloudflare(t *testing.T) {
	inZone(t)
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body == nil {
			body = map[string]any{}
		}
		body["call"] = r.Method + " " + r.URL.Path + "?" + r.URL.RawQuery
		body["auth"] = r.Header.Get("Authorization")
		rec.keep(r, body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			_, _ = io.WriteString(w, `{"success":true,"result":[{"id":"zone1"}]}`)
		case r.Method == http.MethodPost:
			_, _ = io.WriteString(w, `{"success":true,"result":{"id":"rec1"}}`)
		case r.Method == http.MethodDelete:
			_, _ = io.WriteString(w, `{"success":true,"result":{"id":"rec1"}}`)
		}
	}))
	defer srv.Close()
	api, err := Open("cloudflare", map[string]string{"CF_DNS_API_TOKEN": "dns", "CF_ZONE_API_TOKEN": "zone", "CLOUDFLARE_BASE_URL": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	id, err := api.Add(t.Context(), txt)
	if err != nil || id != "zone1/rec1" {
		t.Fatalf("add: %q, %v", id, err)
	}
	if err := api.Remove(t.Context(), txt, id); err != nil {
		t.Fatal(err)
	}
	zones, post, del := rec.got[0], rec.got[1], rec.got[2]
	if zones["call"] != "GET /zones?name=example.com" || zones["auth"] != "Bearer zone" ||
		post["call"] != "POST /zones/zone1/dns_records?" || post["auth"] != "Bearer dns" || post["type"] != "TXT" ||
		post["name"] != txt.Name || post["content"] != txt.Value || post["ttl"] != float64(120) ||
		del["call"] != "DELETE /zones/zone1/dns_records/rec1?" {
		t.Errorf("calls %v", rec.got)
	}
}

func TestHuawei(t *testing.T) {
	inZone(t)
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["call"] = r.Method + " " + r.URL.Path
		body["query"] = r.URL.RawQuery
		rec.keep(r, body)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = io.WriteString(w, `{"zones":[{"id":"other","name":"example.org."},{"id":"z1","name":"example.com."}]}`)
		case http.MethodPost:
			_, _ = io.WriteString(w, `{"id":"rs1"}`)
		default:
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"rs1"}`)
		}
	}))
	defer srv.Close()
	huaweiEndpoint, huaweiProject = srv.URL, "p1"
	t.Cleanup(func() { huaweiEndpoint, huaweiProject = "", "" })

	api, err := Open("huaweicloud", map[string]string{"HUAWEICLOUD_ACCESS_KEY_ID": "AK", "HUAWEICLOUD_SECRET_ACCESS_KEY": "SK", "HUAWEICLOUD_REGION": "cn-south-1"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := api.Add(t.Context(), txt)
	if err != nil || id != "z1/rs1" {
		t.Fatalf("add: %q, %v", id, err)
	}
	if err := api.Remove(t.Context(), txt, id); err != nil {
		t.Fatal(err)
	}
	post, del := rec.got[1], rec.got[2]
	records, _ := post["records"].([]any)
	if post["call"] != "POST /v2/zones/z1/recordsets" || post["name"] != txt.Name+"." || post["type"] != "TXT" ||
		len(records) != 1 || records[0] != `"`+txt.Value+`"` || del["call"] != "DELETE /v2/zones/z1/recordsets/rs1" {
		t.Errorf("calls %v", rec.got)
	}
	if !strings.Contains(rec.reqs[0].Header.Get("Authorization"), "Access=AK") {
		t.Errorf("headers %v", rec.reqs[0].Header)
	}
}

// Each provider lists the domains of the account, the first few by name
// and all by number, as a test of the keys.
func TestDomains(t *testing.T) {
	serve := func(answer func(r *http.Request) string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, answer(r))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	check := func(code string, settings map[string]string, names []string, total int) {
		t.Helper()
		api, err := Open(code, settings)
		if err != nil {
			t.Fatal(err)
		}
		got, n, err := api.Domains(t.Context())
		if err != nil || !slices.Equal(got, names) || n != total {
			t.Errorf("%s: %v %d %v", code, got, n, err)
		}
	}

	tc := serve(func(r *http.Request) string {
		var params map[string]any
		_ = json.NewDecoder(r.Body).Decode(&params)
		if r.Header.Get("X-TC-Action") != "DescribeDomainList" || params["Limit"] != float64(listed) {
			t.Errorf("DNSPod: %s %v", r.Header.Get("X-TC-Action"), params)
		}
		return `{"Response":{"DomainCountInfo":{"AllTotal":3},"DomainList":[{"Name":"example.com"},{"Name":"example.org"}],"RequestId":"r"}}`
	})
	oldTC, oldScheme := tencentEndpoint, tencentScheme
	tencentEndpoint, tencentScheme = strings.TrimPrefix(tc.URL, "http://"), "HTTP"
	t.Cleanup(func() { tencentEndpoint, tencentScheme = oldTC, oldScheme })
	check("tencentcloud", map[string]string{"TENCENTCLOUD_SECRET_ID": "AKID1", "TENCENTCLOUD_SECRET_KEY": "k"}, []string{"example.com", "example.org"}, 3)

	ali := serve(func(r *http.Request) string {
		_ = r.ParseForm()
		if r.Header.Get("x-acs-action") != "DescribeDomains" || r.Form.Get("PageSize") != strconv.Itoa(listed) {
			t.Errorf("alidns: %s %v", r.Header.Get("x-acs-action"), r.Form)
		}
		return `{"RequestId":"r","TotalCount":1,"Domains":{"Domain":[{"DomainName":"example.com"}]}}`
	})
	oldAli, oldProtocol := aliyunEndpoint, aliyunProtocol
	aliyunEndpoint, aliyunProtocol = strings.TrimPrefix(ali.URL, "http://"), "HTTP"
	t.Cleanup(func() { aliyunEndpoint, aliyunProtocol = oldAli, oldProtocol })
	check("alidns", map[string]string{"ALICLOUD_ACCESS_KEY": "LTAI1", "ALICLOUD_SECRET_KEY": "s"}, []string{"example.com"}, 1)

	cf := serve(func(r *http.Request) string {
		if r.URL.Path != "/zones" || r.URL.Query().Get("per_page") != strconv.Itoa(listed) || r.Header.Get("Authorization") != "Bearer zone" {
			t.Errorf("Cloudflare: %s %s", r.URL, r.Header.Get("Authorization"))
		}
		return `{"success":true,"result":[{"name":"example.com"}],"result_info":{"total_count":7}}`
	})
	check("cloudflare", map[string]string{"CF_DNS_API_TOKEN": "dns", "CF_ZONE_API_TOKEN": "zone", "CLOUDFLARE_BASE_URL": cf.URL}, []string{"example.com"}, 7)

	hw := serve(func(r *http.Request) string {
		if r.URL.Path != "/v2/zones" || r.URL.Query().Get("limit") != strconv.Itoa(listed) {
			t.Errorf("Huawei Cloud: %s", r.URL)
		}
		return `{"zones":[{"id":"z1","name":"example.com."}],"metadata":{"total_count":1}}`
	})
	huaweiEndpoint, huaweiProject = hw.URL, "p1"
	t.Cleanup(func() { huaweiEndpoint, huaweiProject = "", "" })
	check("huaweicloud", map[string]string{"HUAWEICLOUD_ACCESS_KEY_ID": "AK", "HUAWEICLOUD_SECRET_ACCESS_KEY": "SK", "HUAWEICLOUD_REGION": "cn-south-1"}, []string{"example.com"}, 1)
}

func TestHolds(t *testing.T) {
	serve := func(answer func(r *http.Request) string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, answer(r))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	check := func(code string, settings map[string]string) {
		t.Helper()
		api, err := Open(code, settings)
		if err != nil {
			t.Fatal(err)
		}
		for zone, want := range map[string]bool{"example.com": true, "other.com": false} {
			if got, err := api.Holds(t.Context(), zone); err != nil || got != want {
				t.Errorf("%s holds %s = %v %v", code, zone, got, err)
			}
		}
	}

	tc := serve(func(r *http.Request) string {
		var params map[string]any
		_ = json.NewDecoder(r.Body).Decode(&params)
		if params["Keyword"] == "example.com" {
			return `{"Response":{"DomainList":[{"Name":"example.com"},{"Name":"sub.example.com"}],"RequestId":"r"}}`
		}
		return `{"Response":{"Error":{"Code":"ResourceNotFound.NoDataOfDomain","Message":"none"},"RequestId":"r"}}`
	})
	oldTC, oldScheme := tencentEndpoint, tencentScheme
	tencentEndpoint, tencentScheme = strings.TrimPrefix(tc.URL, "http://"), "HTTP"
	t.Cleanup(func() { tencentEndpoint, tencentScheme = oldTC, oldScheme })
	check("tencentcloud", map[string]string{"TENCENTCLOUD_SECRET_ID": "AKID1", "TENCENTCLOUD_SECRET_KEY": "k"})

	ali := serve(func(r *http.Request) string {
		_ = r.ParseForm()
		if r.Form.Get("SearchMode") != "EXACT" || r.Form.Get("KeyWord") != "example.com" {
			return `{"RequestId":"r","TotalCount":0,"Domains":{"Domain":[]}}`
		}
		return `{"RequestId":"r","TotalCount":1,"Domains":{"Domain":[{"DomainName":"example.com"}]}}`
	})
	oldAli, oldProtocol := aliyunEndpoint, aliyunProtocol
	aliyunEndpoint, aliyunProtocol = strings.TrimPrefix(ali.URL, "http://"), "HTTP"
	t.Cleanup(func() { aliyunEndpoint, aliyunProtocol = oldAli, oldProtocol })
	check("alidns", map[string]string{"ALICLOUD_ACCESS_KEY": "LTAI1", "ALICLOUD_SECRET_KEY": "s"})

	cf := serve(func(r *http.Request) string {
		if r.URL.Query().Get("name") == "example.com" {
			return `{"success":true,"result":[{"name":"example.com"}],"result_info":{"total_count":1}}`
		}
		return `{"success":true,"result":[],"result_info":{"total_count":0}}`
	})
	check("cloudflare", map[string]string{"CF_DNS_API_TOKEN": "dns", "CF_ZONE_API_TOKEN": "zone", "CLOUDFLARE_BASE_URL": cf.URL})

	hw := serve(func(r *http.Request) string {
		if r.URL.Query().Get("name") == "example.com" {
			return `{"zones":[{"id":"z1","name":"example.com."},{"id":"z2","name":"a.example.com."}],"metadata":{"total_count":2}}`
		}
		return `{"zones":[],"metadata":{"total_count":0}}`
	})
	huaweiEndpoint, huaweiProject = hw.URL, "p1"
	t.Cleanup(func() { huaweiEndpoint, huaweiProject = "", "" })
	check("huaweicloud", map[string]string{"HUAWEICLOUD_ACCESS_KEY_ID": "AK", "HUAWEICLOUD_SECRET_ACCESS_KEY": "SK", "HUAWEICLOUD_REGION": "cn-south-1"})

	// The zone of a name is found in the DNS first.
	inZone(t)
	api, _ := Open("cloudflare", map[string]string{"CF_DNS_API_TOKEN": "dns", "CF_ZONE_API_TOKEN": "zone", "CLOUDFLARE_BASE_URL": cf.URL})
	if zone, held, err := ZoneOf(t.Context(), api, "*.www.example.com"); err != nil || zone != "example.com" || !held {
		t.Errorf("ZoneOf = %s %v %v", zone, held, err)
	}
	if zone, _, err := ZoneOf(t.Context(), api, "delegated.example.com"); err != nil || zone != "example.net" {
		t.Errorf("ZoneOf a delegated name = %s %v", zone, err)
	}
}
