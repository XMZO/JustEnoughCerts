package aliyun

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/aliyunapi"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// CLB, Classic Load Balancer, once SLB (2014-05-15): each new certificate
// is uploaded to the load balancers' own store of the region, then set on
// the HTTPS listeners of a load balancer, as their default certificate or
// as the additional certificate of a domain; those JEC uploaded before
// are deleted, but for those still in use. The calls follow the SDK, as
// certimate makes them.

const clbVersion = "2014-05-15"

// clbEndpoint is the address of the API for region: one for the regions
// of Hangzhou, one of each other region.
func clbEndpoint(region string) string {
	switch region {
	case "", "cn-hangzhou", "cn-hangzhou-finance", "cn-shanghai-finance-1", "cn-shenzhen-finance-1":
		return "slb.aliyuncs.com"
	}
	return "slb." + region + ".aliyuncs.com"
}

func clbType() providers.Type {
	return providers.Type{
		ID: "aliyun.clb", Kind: providers.Target, Name: i18n.AliyunClbName, Description: i18n.AliyunClbDescription, Untested: true,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.AliyunFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "access_key_secret", Label: i18n.AliyunFieldAccessKeySecret, Help: i18n.AliyunFieldClbSecretHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "region", Label: i18n.AliyunFieldRegion, Help: i18n.AliyunFieldRegionHelp, Kind: providers.Text, Required: true, Default: "cn-hangzhou"},
			{Key: "load_balancer", Label: i18n.AliyunFieldLoadBalancer, Kind: providers.Text, Required: true},
			{Key: "listener_port", Label: i18n.AliyunFieldListenerPort, Help: i18n.AliyunFieldListenerPortHelp, Kind: providers.Number},
			{Key: "domain", Label: i18n.AliyunFieldClbDomain, Help: i18n.AliyunFieldClbDomainHelp, Kind: providers.Text, Advanced: true},
			deleteOldField,
		},
		Credential: credentials.Use(credentials.Aliyun),
	}
}

// loadBalancer is a load balancer of CLB, and how it gets certificates.
type loadBalancer struct {
	api              rpc
	endpoint, region string
	id               string
	port             int // 0 for every HTTPS listener
	domain           string
	deleteOld        bool
}

var (
	_ contracts.Target = (*loadBalancer)(nil)
	_ contracts.Tester = (*loadBalancer)(nil)
)

func (m *Module) clbFactory(_ context.Context, in providers.Instance) (any, error) {
	region := strings.TrimSpace(in.Config["region"])
	port := 0
	if v := strings.TrimSpace(in.Config["listener_port"]); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorPort, Params: map[string]any{"port": v}})
		}
		port = p
	}
	endpoint := clbEndpoint(region)
	api, err := m.rpc(endpoint, clbVersion, strings.TrimSpace(in.Value("access_key_id")), strings.TrimSpace(in.Value("access_key_secret")))
	if err != nil {
		return nil, retry.Permanent(err)
	}
	return &loadBalancer{
		api: api, endpoint: endpoint, region: region, id: strings.TrimSpace(in.Config["load_balancer"]), port: port,
		domain: strings.ToLower(strings.TrimSpace(in.Config["domain"])), deleteOld: in.Config["delete_old"] != "false",
	}, nil
}

// Close has nothing to release.
func (*loadBalancer) Close() error { return nil }

// explain turns an error of the API into a Problem, knowing the load
// balancer it was about.
func (l *loadBalancer) explain(err error) error {
	return explainRPC(err, l.endpoint, func(lower string) *contracts.Problem {
		if strings.Contains(lower, "loadbalancer") && missing(lower) {
			return &contracts.Problem{Key: i18n.AliyunErrorLoadBalancer, Params: map[string]any{"id": l.id, "region": l.region}}
		}
		return nil
	})
}

// listeners returns the name of the load balancer and the ports of its
// HTTPS listeners.
func (l *loadBalancer) listeners(ctx context.Context) (string, []int, error) {
	var out struct {
		LoadBalancerName         string `json:"LoadBalancerName"`
		ListenerPortsAndProtocol struct {
			ListenerPortAndProtocol []struct {
				ListenerPort     int    `json:"ListenerPort"`
				ListenerProtocol string `json:"ListenerProtocol"`
			} `json:"ListenerPortAndProtocol"`
		} `json:"ListenerPortsAndProtocol"`
	}
	err := l.api.do(ctx, "DescribeLoadBalancerAttribute", map[string]string{"RegionId": l.region, "LoadBalancerId": l.id}, nil, &out)
	if err != nil {
		return "", nil, l.explain(err)
	}
	var ports []int
	for _, p := range out.ListenerPortsAndProtocol.ListenerPortAndProtocol {
		if strings.EqualFold(p.ListenerProtocol, "https") && !slices.Contains(ports, p.ListenerPort) {
			ports = append(ports, p.ListenerPort)
		}
	}
	slices.Sort(ports)
	return out.LoadBalancerName, ports, nil
}

// Test finds the load balancer and its HTTPS listeners, changing nothing.
func (l *loadBalancer) Test(ctx context.Context) (contracts.Message, error) {
	name, ports, err := l.listeners(ctx)
	if err != nil {
		return contracts.Message{}, err
	}
	if name == "" {
		name = l.id
	}
	return contracts.Message{Key: i18n.AliyunTestClbOk, Params: map[string]any{"name": name, "count": len(ports)}}, nil
}

// Deploy uploads the certificate, unless an earlier attempt did, sets it
// on the listeners, and deletes those JEC uploaded before, if set to.
func (l *loadBalancer) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := logger(req)
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("aliyun: no certificate to deploy"))
	}
	_, ports, err := l.listeners(ctx)
	if err != nil {
		return none, err
	}
	if l.port != 0 {
		if !slices.Contains(ports, l.port) {
			return none, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorListener, Params: map[string]any{"id": l.id, "port": l.port}})
		}
		ports = []int{l.port}
	}
	if len(ports) == 0 {
		return none, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorNoListeners, Params: map[string]any{"id": l.id}})
	}
	id, err := l.upload(ctx, req, log)
	if err != nil {
		return none, err
	}
	for _, port := range ports {
		if l.domain == "" {
			err = l.setDefault(ctx, port, id, log)
		} else {
			err = l.setExtension(ctx, port, id, log)
		}
		if err != nil {
			return none, err
		}
	}
	res := contracts.DeployResult{Ref: id}
	if l.deleteOld {
		res.Removed = removeOld(req.Previous, id, log, func(old string) (bool, error) {
			err := l.api.do(ctx, "DeleteServerCertificate", map[string]string{"RegionId": l.region, "ServerCertificateId": old}, nil, nil)
			code, _ := aliyunapi.ErrorCode(err)
			return missing(strings.ToLower(code)), err
		})
	}
	return res, nil
}

// upload puts the certificate of req into the store of the region, unless
// it is there from an earlier attempt, and returns its ID.
func (l *loadBalancer) upload(ctx context.Context, req contracts.DeployRequest, log func(contracts.LogLine)) (string, error) {
	name := certName(req)
	var list struct {
		ServerCertificates struct {
			ServerCertificate []struct {
				ServerCertificateID   string `json:"ServerCertificateId"`
				ServerCertificateName string `json:"ServerCertificateName"`
			} `json:"ServerCertificate"`
		} `json:"ServerCertificates"`
	}
	if err := l.api.do(ctx, "DescribeServerCertificates", map[string]string{"RegionId": l.region}, nil, &list); err != nil {
		return "", l.explain(err)
	}
	for _, c := range list.ServerCertificates.ServerCertificate {
		if c.ServerCertificateName == name {
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogClbFound, Params: map[string]any{"name": name}})
			return c.ServerCertificateID, nil
		}
	}
	key, err := cryptox.EncodeKeyTraditional(req.Key)
	if err != nil {
		return "", retry.Permanent(err)
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogClbUpload, Params: map[string]any{"name": name}})
	var out struct {
		ServerCertificateID string `json:"ServerCertificateId"`
	}
	form := map[string]string{"ServerCertificate": string(cryptox.EncodeCertificates(req.Chain...)), "PrivateKey": string(key)}
	if err := l.api.do(ctx, "UploadServerCertificate", map[string]string{"RegionId": l.region, "ServerCertificateName": name}, form, &out); err != nil {
		return "", l.explain(err)
	}
	if out.ServerCertificateID == "" {
		return "", errors.New("aliyun: UploadServerCertificate answered no ServerCertificateId")
	}
	return out.ServerCertificateID, nil
}

// setDefault makes certificate id the default of the listener on port.
func (l *loadBalancer) setDefault(ctx context.Context, port int, id string, log func(contracts.LogLine)) error {
	query := map[string]string{"RegionId": l.region, "LoadBalancerId": l.id, "ListenerPort": strconv.Itoa(port)}
	var out struct {
		ServerCertificateID string `json:"ServerCertificateId"`
	}
	if err := l.api.do(ctx, "DescribeLoadBalancerHTTPSListenerAttribute", query, nil, &out); err != nil {
		return l.explain(err)
	}
	if out.ServerCertificateID == id {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogClbCurrent, Params: map[string]any{"port": port}})
		return nil
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogClbListener, Params: map[string]any{"port": port}})
	query["ServerCertificateId"] = id
	if err := l.api.do(ctx, "SetLoadBalancerHTTPSListenerAttribute", query, nil, nil); err != nil {
		return l.explain(err)
	}
	return nil
}

// setExtension makes certificate id that of the domain's additional
// certificates on the listener on port.
func (l *loadBalancer) setExtension(ctx context.Context, port int, id string, log func(contracts.LogLine)) error {
	var out struct {
		DomainExtensions struct {
			DomainExtension []struct {
				DomainExtensionID   string `json:"DomainExtensionId"`
				Domain              string `json:"Domain"`
				ServerCertificateID string `json:"ServerCertificateId"`
			} `json:"DomainExtension"`
		} `json:"DomainExtensions"`
	}
	query := map[string]string{"RegionId": l.region, "LoadBalancerId": l.id, "ListenerPort": strconv.Itoa(port)}
	if err := l.api.do(ctx, "DescribeDomainExtensions", query, nil, &out); err != nil {
		return l.explain(err)
	}
	found := false
	for _, e := range out.DomainExtensions.DomainExtension {
		if !strings.EqualFold(e.Domain, l.domain) {
			continue
		}
		found = true
		if e.ServerCertificateID == id {
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogClbCurrent, Params: map[string]any{"port": port}})
			continue
		}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogClbExtension, Params: map[string]any{"port": port, "domain": l.domain}})
		err := l.api.do(ctx, "SetDomainExtensionAttribute",
			map[string]string{"RegionId": l.region, "DomainExtensionId": e.DomainExtensionID, "ServerCertificateId": id}, nil, nil)
		if err != nil {
			return l.explain(err)
		}
	}
	if !found {
		return retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorNoExtension, Params: map[string]any{"port": port, "domain": l.domain}})
	}
	return nil
}
