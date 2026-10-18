package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alibabacloud-go/tea/dara"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// rpcCall is a call of an action, as a fakeRPC got it.
type rpcCall struct {
	action      string
	query, form map[string]string
}

// fakeRPC answers the actions of a service with answer, and keeps the
// calls.
type fakeRPC struct {
	mu     sync.Mutex
	calls  []rpcCall
	answer func(c rpcCall) (any, error)
}

func (f *fakeRPC) do(_ context.Context, action string, query, form map[string]string, out any) error {
	c := rpcCall{action: action, query: maps.Clone(query), form: maps.Clone(form)}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	res, err := f.answer(c)
	if err != nil || out == nil {
		return err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// actions lists the actions called so far, and forgets them.
func (f *fakeRPC) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.action)
	}
	f.calls = nil
	return out
}

// called returns the calls of action so far.
func (f *fakeRPC) called(action string) []rpcCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rpcCall
	for _, c := range f.calls {
		if c.action == action {
			out = append(out, c)
		}
	}
	return out
}

func apiError(code string, status int) error {
	return &dara.SDKError{Code: dara.String(code), StatusCode: dara.Int(status)}
}

// openRPC makes a provider of typ through f, which the module opens at
// endpoint.
func openRPC(t *testing.T, f *fakeRPC, factory func(*Module) providers.Factory, endpoint string, config map[string]string) any {
	t.Helper()
	m := &Module{rpc: func(e, _, id, secret string) (rpc, error) {
		if e != endpoint || id != "LTAI-id" || secret != "s3cret" {
			t.Errorf("opened %s with %s %s", e, id, secret)
		}
		return f, nil
	}}
	p, err := factory(m)(t.Context(), providers.Instance{Config: config, Secrets: map[string]string{"access_key_id": "LTAI-id", "access_key_secret": "s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeCAS is a Certificate Management Service: the uploaded
// certificates by ID, and those in use.
type fakeCAS struct {
	*fakeRPC
	certs map[string]string // name by ID
	inUse map[string]bool
	next  int
}

func newFakeCAS() *fakeCAS {
	c := &fakeCAS{certs: map[string]string{"900": "jec-1-old", "901": "jec-1-older"}, inUse: map[string]bool{"901": true}, next: 1000}
	c.fakeRPC = &fakeRPC{answer: c.answer}
	return c
}

func (c *fakeCAS) answer(call rpcCall) (any, error) {
	switch call.action {
	case "ListUserCertificateOrder":
		type item struct {
			CertificateID json.Number `json:"CertificateId"`
			Name          string
		}
		list := []item{}
		for _, id := range slices.Sorted(maps.Keys(c.certs)) {
			list = append(list, item{json.Number(id), c.certs[id]})
		}
		return map[string]any{"CertificateOrderList": list, "TotalCount": len(list)}, nil
	case "UploadUserCertificate":
		c.next++
		id := strconv.Itoa(c.next)
		c.certs[id] = call.query["Name"]
		return map[string]any{"CertId": json.Number(id)}, nil
	case "DeleteUserCertificate":
		id := call.query["CertId"]
		switch {
		case c.inUse[id]:
			return nil, apiError("CertificateInUse", 400)
		case c.certs[id] == "":
			return nil, apiError("NotFound.Certificate", 404)
		}
		delete(c.certs, id)
		return map[string]any{}, nil
	}
	return nil, apiError("InvalidAction", 400)
}

func casFactory(m *Module) providers.Factory { return m.casFactory }

func TestCAS(t *testing.T) {
	cas := newFakeCAS()
	store := openRPC(t, cas.fakeRPC, casFactory, "cas.aliyuncs.com", map[string]string{"site": casChina}).(*certStore)
	req := request(t, "*.example.com", "example.com")
	req.Previous = []string{"900", "901", "902"}
	var lines []contracts.LogLine
	req.Log = func(l contracts.LogLine) { lines = append(lines, l) }

	res, err := store.Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	// 900 is deleted, 901 is in use, 902 was gone already.
	if res.Ref != "1001" || !slices.Equal(res.Removed, []string{"900", "902"}) {
		t.Errorf("result %+v", res)
	}
	list := cas.called("ListUserCertificateOrder")[0]
	if list.query["OrderType"] != "UPLOAD" || list.query["Keyword"] != "example.com" {
		t.Errorf("listed with %v", list.query)
	}
	up := cas.called("UploadUserCertificate")[0]
	if up.query["Name"] != certName(req) || strings.Count(up.form["Cert"], "BEGIN CERTIFICATE") != 2 ||
		!strings.Contains(up.form["Key"], "EC PRIVATE KEY") || up.query["Cert"] != "" || up.query["Key"] != "" {
		t.Errorf("uploaded with query %v, form %v", up.query, up.form)
	}
	if !slices.ContainsFunc(lines, func(l contracts.LogLine) bool { return l.Key == i18n.AliyunLogKept && l.Params["id"] == "901" }) {
		t.Errorf("log %+v", lines)
	}

	// Again, as after a stop: the certificate is there.
	cas.actions()
	req.Previous = nil
	if res, err := store.Deploy(t.Context(), req); err != nil || res.Ref != "1001" {
		t.Errorf("again: %+v, %v", res, err)
	}
	if got := cas.actions(); !slices.Equal(got, []string{"ListUserCertificateOrder"}) {
		t.Errorf("again called %v", got)
	}

	// Not set to delete, nothing is.
	keep := openRPC(t, cas.fakeRPC, casFactory, "cas.ap-southeast-1.aliyuncs.com", map[string]string{"site": casIntl, "delete_old": "false"}).(*certStore)
	req.Previous = []string{"900"}
	if res, err := keep.Deploy(t.Context(), req); err != nil || len(res.Removed) != 0 || len(cas.called("DeleteUserCertificate")) != 0 {
		t.Errorf("kept: %+v, %v", res, err)
	}

	msg, err := store.Test(t.Context())
	if err != nil || msg.Key != i18n.AliyunTestCasOk || msg.Params["count"] != 2 {
		t.Errorf("test: %+v, %v", msg, err)
	}
}

func TestCASErrors(t *testing.T) {
	f := &fakeRPC{answer: func(rpcCall) (any, error) { return nil, apiError("InvalidAccessKeyId.NotFound", 404) }}
	store := openRPC(t, f, casFactory, "cas.aliyuncs.com", map[string]string{"site": casChina}).(*certStore)
	var p *contracts.Problem
	if _, err := store.Deploy(t.Context(), request(t, "example.com")); !errors.As(err, &p) || p.Key != i18n.AliyunErrorAuth || !retry.IsPermanent(err) {
		t.Errorf("a wrong key: %v", err)
	}
	f.answer = func(rpcCall) (any, error) { return nil, errors.New("dial tcp: i/o timeout") }
	if _, err := store.Test(t.Context()); !errors.As(err, &p) || p.Key != i18n.AliyunErrorNetwork || retry.IsPermanent(err) {
		t.Errorf("no network: %v", err)
	}
}

// fakeCLB is a region of CLB with one load balancer, lb-1.
type fakeCLB struct {
	*fakeRPC
	certs      map[string]string // name by ID
	inUse      map[string]bool
	listeners  map[int]string // the default certificate of each HTTPS listener
	extensions map[string][2]string
	next       int
}

func newFakeCLB() *fakeCLB {
	c := &fakeCLB{
		certs: map[string]string{"sc-old": "jec-1-old"}, inUse: map[string]bool{},
		listeners:  map[int]string{443: "sc-old", 8443: "sc-old"},
		extensions: map[string][2]string{"ext-1": {"www.example.com", "sc-old"}, "ext-2": {"api.example.com", "sc-other"}},
	}
	c.fakeRPC = &fakeRPC{answer: c.answer}
	return c
}

func (c *fakeCLB) answer(call rpcCall) (any, error) {
	q := call.query
	if q["RegionId"] != "cn-shanghai" {
		return nil, apiError("InvalidRegionId", 400)
	}
	if id, ok := q["LoadBalancerId"]; ok && id != "lb-1" {
		return nil, apiError("InvalidLoadBalancerId.NotFound", 400)
	}
	port, _ := strconv.Atoi(q["ListenerPort"])
	switch call.action {
	case "DescribeLoadBalancerAttribute":
		ports := []map[string]any{{"ListenerPort": 80, "ListenerProtocol": "http"}}
		for p := range c.listeners {
			ports = append(ports, map[string]any{"ListenerPort": p, "ListenerProtocol": "https"})
		}
		return map[string]any{"LoadBalancerName": "web", "ListenerPortsAndProtocol": map[string]any{"ListenerPortAndProtocol": ports}}, nil
	case "DescribeServerCertificates":
		list := []map[string]string{}
		for id, name := range c.certs {
			list = append(list, map[string]string{"ServerCertificateId": id, "ServerCertificateName": name})
		}
		return map[string]any{"ServerCertificates": map[string]any{"ServerCertificate": list}}, nil
	case "UploadServerCertificate":
		c.next++
		id := "sc-" + strconv.Itoa(c.next)
		c.certs[id] = q["ServerCertificateName"]
		return map[string]any{"ServerCertificateId": id}, nil
	case "DescribeLoadBalancerHTTPSListenerAttribute":
		return map[string]any{"ServerCertificateId": c.listeners[port]}, nil
	case "SetLoadBalancerHTTPSListenerAttribute":
		c.listeners[port] = q["ServerCertificateId"]
		return map[string]any{}, nil
	case "DescribeDomainExtensions":
		list := []map[string]string{}
		if port == 443 {
			for id, e := range c.extensions {
				list = append(list, map[string]string{"DomainExtensionId": id, "Domain": e[0], "ServerCertificateId": e[1]})
			}
		}
		return map[string]any{"DomainExtensions": map[string]any{"DomainExtension": list}}, nil
	case "SetDomainExtensionAttribute":
		e := c.extensions[q["DomainExtensionId"]]
		c.extensions[q["DomainExtensionId"]] = [2]string{e[0], q["ServerCertificateId"]}
		return map[string]any{}, nil
	case "DeleteServerCertificate":
		if c.inUse[q["ServerCertificateId"]] {
			return nil, apiError("ResourceInUse.ServerCertificate", 400)
		}
		delete(c.certs, q["ServerCertificateId"])
		return map[string]any{}, nil
	}
	return nil, apiError("InvalidAction", 400)
}

func clbFactory(m *Module) providers.Factory { return m.clbFactory }

func openCLB(t *testing.T, c *fakeCLB, config map[string]string) *loadBalancer {
	t.Helper()
	cfg := map[string]string{"region": "cn-shanghai", "load_balancer": "lb-1"}
	maps.Copy(cfg, config)
	return openRPC(t, c.fakeRPC, clbFactory, "slb.cn-shanghai.aliyuncs.com", cfg).(*loadBalancer)
}

func TestCLB(t *testing.T) {
	c := newFakeCLB()
	lb := openCLB(t, c, nil)
	req := request(t, "example.com")
	req.Previous = []string{"sc-old"}
	res, err := lb.Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "sc-1" || !slices.Equal(res.Removed, []string{"sc-old"}) || c.listeners[443] != "sc-1" || c.listeners[8443] != "sc-1" {
		t.Errorf("result %+v, listeners %v", res, c.listeners)
	}
	up := c.called("UploadServerCertificate")[0]
	if up.query["ServerCertificateName"] != certName(req) || !strings.Contains(up.form["ServerCertificate"], "BEGIN CERTIFICATE") ||
		!strings.Contains(up.form["PrivateKey"], "PRIVATE KEY") || up.query["PrivateKey"] != "" {
		t.Errorf("uploaded with %v, %v", up.query, up.form)
	}

	// Again: the certificate is there, and the listeners have it.
	c.actions()
	if res, err := lb.Deploy(t.Context(), req); err != nil || res.Ref != "sc-1" {
		t.Fatalf("again: %+v, %v", res, err)
	}
	if got := c.actions(); slices.Contains(got, "UploadServerCertificate") || slices.Contains(got, "SetLoadBalancerHTTPSListenerAttribute") {
		t.Errorf("again called %v", got)
	}

	msg, err := lb.Test(t.Context())
	if err != nil || msg.Key != i18n.AliyunTestClbOk || msg.Params["name"] != "web" || msg.Params["count"] != 2 {
		t.Errorf("test: %+v, %v", msg, err)
	}
}

func TestCLBListeners(t *testing.T) {
	c := newFakeCLB()
	req := request(t, "example.com", "www.example.com")
	// One listener.
	if _, err := openCLB(t, c, map[string]string{"listener_port": "8443"}).Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if c.listeners[8443] != "sc-1" || c.listeners[443] != "sc-old" {
		t.Errorf("listeners %v", c.listeners)
	}
	// An additional certificate, not the default one.
	if _, err := openCLB(t, c, map[string]string{"listener_port": "443", "domain": "WWW.example.com"}).Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if c.extensions["ext-1"][1] != "sc-1" || c.extensions["ext-2"][1] != "sc-other" || c.listeners[443] != "sc-old" {
		t.Errorf("extensions %v, listeners %v", c.extensions, c.listeners)
	}

	var p *contracts.Problem
	for config, key := range map[string]i18n.Key{
		"80":     i18n.AliyunErrorListener,
		"domain": i18n.AliyunErrorNoExtension,
	} {
		cfg := map[string]string{"listener_port": config}
		if config == "domain" {
			cfg = map[string]string{"domain": "shop.example.com"}
		}
		if _, err := openCLB(t, c, cfg).Deploy(t.Context(), req); !errors.As(err, &p) || p.Key != key || !retry.IsPermanent(err) {
			t.Errorf("%s: %v", config, err)
		}
	}
	if _, err := openCLB(t, c, map[string]string{"load_balancer": "lb-2"}).Deploy(t.Context(), req); !errors.As(err, &p) || p.Key != i18n.AliyunErrorLoadBalancer || !retry.IsPermanent(err) {
		t.Errorf("another load balancer: %v", err)
	}
	_, err := (&Module{rpc: func(string, string, string, string) (rpc, error) { return c, nil }}).clbFactory(t.Context(),
		providers.Instance{Config: map[string]string{"region": "cn-shanghai", "load_balancer": "lb-1", "listener_port": "https"}})
	if !errors.As(err, &p) || p.Key != i18n.AliyunErrorPort {
		t.Errorf("a port that is no number: %v", err)
	}
}

func TestCLBEndpoint(t *testing.T) {
	for region, want := range map[string]string{
		"cn-hangzhou": "slb.aliyuncs.com", "cn-shenzhen-finance-1": "slb.aliyuncs.com", "cn-beijing": "slb.cn-beijing.aliyuncs.com",
	} {
		if got := clbEndpoint(region); got != want {
			t.Errorf("%s: %s", region, got)
		}
	}
}

// Over HTTP, the actions reach the API as RPC calls signed with version 3,
// the form in the body; the answers decode, numbers whole.
func TestRPC(t *testing.T) {
	var got http.Header
	var query, form string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		query = r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		form = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"RequestId":"r1","CertId":12345678901234567,"List":{"Item":[{"Name":"a"}]}}`)
	}))
	defer srv.Close()
	api, err := dialRPC(casVersion, "LTAI-id", "s3cret", strings.TrimPrefix(srv.URL, "http://"), "HTTP")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		CertID anyID `json:"CertId"`
		List   struct{ Item []struct{ Name string } }
	}
	if err := api.do(t.Context(), "UploadUserCertificate", map[string]string{"Name": "jec-1"}, map[string]string{"Key": "KEY PEM"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.CertID != "12345678901234567" || len(out.List.Item) != 1 || out.List.Item[0].Name != "a" {
		t.Errorf("decoded %+v", out)
	}
	if got.Get("x-acs-action") != "UploadUserCertificate" || got.Get("x-acs-version") != casVersion || !strings.Contains(got.Get("Authorization"), "ACS3-HMAC-SHA256") ||
		!strings.Contains(query, "Name=jec-1") || strings.Contains(query, "KEY") || !strings.Contains(form, "Key=KEY") {
		t.Errorf("headers %v, query %q, form %q", got, query, form)
	}
}
