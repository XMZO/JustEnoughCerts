package aliyun

import (
	"errors"
	"maps"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// fakeALB is the ALB of a region: its load balancers and their listeners,
// which are being configured for a while after each change.
type fakeALB struct {
	*fakeRPC
	mu        sync.Mutex
	lbs       map[string]string // names by ID
	listeners map[string]*fakeListener
	busy      int // how many looks a change keeps a listener configuring
}

type fakeListener struct {
	lb, protocol string
	configuring  int
	def          string
	extra        []string
}

func newFakeALB() *fakeALB {
	f := &fakeALB{lbs: map[string]string{"alb-1": "web"}, busy: 1, listeners: map[string]*fakeListener{
		"lsn-https": {lb: "alb-1", protocol: "HTTPS", def: "900-cn-hangzhou", extra: []string{"900-cn-hangzhou", "555-cn-hangzhou"}},
		"lsn-quic":  {lb: "alb-1", protocol: "QUIC", def: "900-cn-hangzhou"},
		"lsn-http":  {lb: "alb-1", protocol: "HTTP"},
		"lsn-other": {lb: "alb-2", protocol: "HTTPS"},
	}}
	f.fakeRPC = &fakeRPC{answer: f.respond}
	return f
}

func (f *fakeALB) respond(c rpcCall) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := f.listeners[c.query["ListenerId"]]
	switch c.action {
	case "GetLoadBalancerAttribute":
		name, ok := f.lbs[c.query["LoadBalancerId"]]
		if !ok {
			return nil, apiError("ResourceNotFound.LoadBalancer", 404)
		}
		return map[string]any{"LoadBalancerName": name}, nil
	case "ListListeners":
		list := []map[string]any{}
		for _, id := range slices.Sorted(maps.Keys(f.listeners)) {
			if l := f.listeners[id]; l.lb == c.query["LoadBalancerIds.1"] && l.protocol == c.query["ListenerProtocol"] {
				list = append(list, map[string]any{"ListenerId": id})
			}
		}
		return map[string]any{"Listeners": list}, nil
	case "GetListenerAttribute":
		if l == nil {
			return nil, apiError("ResourceNotFound.Listener", 404)
		}
		status := "Running"
		if l.configuring > 0 {
			l.configuring--
			status = "Configuring"
		}
		return map[string]any{"LoadBalancerId": l.lb, "ListenerStatus": status, "Certificates": []map[string]any{{"CertificateId": l.def}}}, nil
	case "UpdateListenerAttribute":
		l.def, l.configuring = c.query["Certificates.1.CertificateId"], f.busy
		return map[string]any{}, nil
	case "ListListenerCertificates":
		list := []map[string]any{{"CertificateId": l.def, "IsDefault": true, "Status": "Associated"}}
		for _, c := range l.extra {
			list = append(list, map[string]any{"CertificateId": c, "IsDefault": false, "Status": "Associated"})
		}
		return map[string]any{"Certificates": list}, nil
	case "AssociateAdditionalCertificatesWithListener":
		l.extra, l.configuring = append(l.extra, c.query["Certificates.1.CertificateId"]), f.busy
		return map[string]any{}, nil
	case "DissociateAdditionalCertificatesFromListener":
		for i := 1; c.query["Certificates."+strconv.Itoa(i)+".CertificateId"] != ""; i++ {
			gone := c.query["Certificates."+strconv.Itoa(i)+".CertificateId"]
			l.extra = slices.DeleteFunc(l.extra, func(e string) bool { return e == gone })
		}
		l.configuring = f.busy
		return map[string]any{}, nil
	}
	return nil, apiError("InvalidAction", 400)
}

func (f *fakeALB) listener(id string) fakeListener {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.listeners[id]
}

func openALB(t *testing.T, alb *fakeALB, cas *fakeCAS, config map[string]string) *albBalancer {
	t.Helper()
	old, tries := albPoll, albTries
	albPoll, albTries = time.Millisecond, 5
	t.Cleanup(func() { albPoll, albTries = old, tries })
	m := &Module{rpc: func(endpoint, _, id, secret string) (rpc, error) {
		if id != "LTAI-id" || secret != "s3cret" {
			t.Errorf("opened %s with %s %s", endpoint, id, secret)
		}
		switch endpoint {
		case "alb.cn-hangzhou.aliyuncs.com":
			return alb.fakeRPC, nil
		case "cas.aliyuncs.com":
			return cas.fakeRPC, nil
		}
		t.Errorf("opened %s", endpoint)
		return nil, errors.New("no such endpoint")
	}}
	cfg := map[string]string{"region": "cn-hangzhou", "load_balancer": "alb-1"}
	maps.Copy(cfg, config)
	p, err := m.albFactory(t.Context(), providers.Instance{Config: cfg, Secrets: map[string]string{"access_key_id": "LTAI-id", "access_key_secret": "s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*albBalancer)
}

func TestALBDefault(t *testing.T) {
	alb, cas := newFakeALB(), newFakeCAS()
	b := openALB(t, alb, cas, nil)
	req := request(t, "example.com")
	req.Previous = []string{"900", "901"}
	var keys []i18n.Key
	req.Log = func(l contracts.LogLine) { keys = append(keys, l.Key) }

	// The HTTPS and QUIC listeners of the load balancer take the
	// certificate as their default one; the old certificate goes once
	// they let go of it, but one still in use.
	res, err := b.Deploy(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"lsn-https", "lsn-quic"} {
		if got := alb.listener(id).def; got != "1001-cn-hangzhou" {
			t.Errorf("%s has %s", id, got)
		}
	}
	if alb.listener("lsn-http").def != "" || alb.listener("lsn-other").def != "" {
		t.Error("changed listeners of another protocol or load balancer")
	}
	if res.Ref != "1001" || !slices.Equal(res.Removed, []string{"900"}) {
		t.Errorf("result %+v", res)
	}
	if !slices.Contains(keys, i18n.AliyunLogAlbWait) || slices.Index(keys, i18n.AliyunLogAlbDefault) < 0 {
		t.Errorf("log %v", keys)
	}

	// Again: nothing to change.
	alb.actions()
	if _, err := b.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(alb.actions(), "UpdateListenerAttribute") {
		t.Error("set the certificate again")
	}
}

func TestALBAdditional(t *testing.T) {
	alb, cas := newFakeALB(), newFakeCAS()
	b := openALB(t, alb, cas, map[string]string{"additional": "true", "listeners": "lsn-https", "delete_old": "false"})
	req := request(t, "example.com")
	req.Previous = []string{"900"}
	if _, err := b.Deploy(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	// JEC's earlier certificate goes, but not one of somebody else.
	l := alb.listener("lsn-https")
	if l.def != "900-cn-hangzhou" || !slices.Equal(l.extra, []string{"555-cn-hangzhou", "1001-cn-hangzhou"}) {
		t.Errorf("listener %+v", l)
	}
	if alb.listener("lsn-quic").def != "900-cn-hangzhou" || len(cas.called("DeleteUserCertificate")) != 0 {
		t.Error("changed what was not set")
	}
}

func TestALBErrors(t *testing.T) {
	for name, c := range map[string]struct {
		config    map[string]string
		key       i18n.Key
		permanent bool
	}{
		"no load balancer":          {map[string]string{"load_balancer": "alb-9"}, i18n.AliyunErrorLoadBalancer, true},
		"another's listener":        {map[string]string{"listeners": "lsn-other"}, i18n.AliyunErrorAlbListener, true},
		"no listener":               {map[string]string{"listeners": "lsn-9"}, i18n.AliyunErrorAlbListener, true},
		"listener configured still": {map[string]string{"listeners": "lsn-busy"}, i18n.AliyunErrorAlbBusy, false},
	} {
		alb := newFakeALB()
		alb.listeners["lsn-busy"] = &fakeListener{lb: "alb-1", protocol: "HTTPS", configuring: 100}
		_, err := openALB(t, alb, newFakeCAS(), c.config).Deploy(t.Context(), request(t, "example.com"))
		if problemKey(err) != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%s: %v", name, err)
		}
	}
	alb := newFakeALB()
	alb.lbs["alb-3"] = "empty"
	_, err := openALB(t, alb, newFakeCAS(), map[string]string{"load_balancer": "alb-3"}).Deploy(t.Context(), request(t, "example.com"))
	if problemKey(err) != i18n.AliyunErrorAlbNoListeners || !retry.IsPermanent(err) {
		t.Errorf("no listeners: %v", err)
	}
}

func TestALBTest(t *testing.T) {
	msg, err := openALB(t, newFakeALB(), newFakeCAS(), nil).Test(t.Context())
	if err != nil || msg.Key != i18n.AliyunTestAlbOk || msg.Params["name"] != "web" || msg.Params["count"] != 2 {
		t.Errorf("test: %+v %v", msg, err)
	}
}

func TestALBEndpoints(t *testing.T) {
	for region, want := range map[string][2]string{
		"":               {"alb.cn-hangzhou.aliyuncs.com", casChina},
		"cn-shanghai":    {"alb.cn-shanghai.aliyuncs.com", casChina},
		"ap-southeast-1": {"alb.ap-southeast-1.aliyuncs.com", casIntl},
	} {
		if got := [2]string{albEndpoint(region), casSiteOf(region)}; got != want {
			t.Errorf("%q: %v", region, got)
		}
	}
}
