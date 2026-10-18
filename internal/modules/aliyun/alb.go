package aliyun

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/aliyunapi"
)

// ALB, Application Load Balancer (2020-06-16): each new certificate is
// uploaded to the Certificate Management Service, as for aliyun.cas, and
// set on listeners of a load balancer, HTTPS and QUIC ones, as their
// default certificate or as an additional one, which listeners choose by
// the names a client asks for; the additional ones JEC set before go. A
// listener takes one change at a time: before each, JEC waits for it to be
// done with the last. Those JEC uploaded before are deleted from the
// service, but for those still in use. The calls follow the SDK, as
// certimate makes them.

const albVersion = "2020-06-16"

// How often, and how many times, JEC looks at a listener being
// configured; tests shorten them.
var (
	albPoll  = 3 * time.Second
	albTries = 40
)

// albEndpoint is the address of the API for region.
func albEndpoint(region string) string {
	if region == "" || region == "cn-hangzhou-finance" {
		return "alb.cn-hangzhou.aliyuncs.com"
	}
	return "alb." + region + ".aliyuncs.com"
}

// casSiteOf is the site of the Certificate Management Service whose
// certificates the products of region take.
func casSiteOf(region string) string {
	if region == "" || strings.HasPrefix(region, "cn-") {
		return casChina
	}
	return casIntl
}

func albType() providers.Type {
	return providers.Type{
		ID: "aliyun.alb", Kind: providers.Target, Name: i18n.AliyunAlbName, Description: i18n.AliyunAlbDescription,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.AliyunFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "access_key_secret", Label: i18n.AliyunFieldAccessKeySecret, Help: i18n.AliyunFieldAlbSecretHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "region", Label: i18n.AliyunFieldRegion, Help: i18n.AliyunFieldRegionHelp, Kind: providers.Text, Required: true, Default: "cn-hangzhou"},
			{Key: "load_balancer", Label: i18n.AliyunFieldLoadBalancer, Kind: providers.Text, Required: true},
			{Key: "listeners", Label: i18n.AliyunFieldAlbListeners, Help: i18n.AliyunFieldAlbListenersHelp, Kind: providers.Text},
			{Key: "additional", Label: i18n.AliyunFieldAlbAdditional, Help: i18n.AliyunFieldAlbAdditionalHelp, Kind: providers.Switch, Advanced: true},
			deleteOldField,
		},
	}
}

// albBalancer is a load balancer of ALB, and how it gets certificates.
type albBalancer struct {
	api, cas              rpc
	endpoint, casEndpoint string
	region, casRegion     string
	id                    string
	listeners             []string // those set, or none for all
	additional            bool
	deleteOld             bool
}

var (
	_ contracts.Target = (*albBalancer)(nil)
	_ contracts.Tester = (*albBalancer)(nil)
)

func (m *Module) albFactory(_ context.Context, in providers.Instance) (any, error) {
	region := strings.TrimSpace(in.Config["region"])
	id, secret := strings.TrimSpace(in.Value("access_key_id")), strings.TrimSpace(in.Value("access_key_secret"))
	endpoint := albEndpoint(region)
	api, err := m.rpc(endpoint, albVersion, id, secret)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	site := casSiteOf(region)
	cas, err := m.rpc(casEndpoint(site), casVersion, id, secret)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	var listeners []string
	for l := range strings.SplitSeq(in.Config["listeners"], ",") {
		if l = strings.TrimSpace(l); l != "" && !slices.Contains(listeners, l) {
			listeners = append(listeners, l)
		}
	}
	return &albBalancer{
		api: api, cas: cas, endpoint: endpoint, casEndpoint: casEndpoint(site), region: region, casRegion: site,
		id: strings.TrimSpace(in.Config["load_balancer"]), listeners: listeners,
		additional: in.Config["additional"] == "true", deleteOld: in.Config["delete_old"] != "false",
	}, nil
}

// Close has nothing to release.
func (*albBalancer) Close() error { return nil }

// explain turns an error of the API into a Problem, knowing the load
// balancer it was about.
func (b *albBalancer) explain(err error) error {
	return explainRPC(err, b.endpoint, func(lower string) *contracts.Problem {
		if strings.Contains(lower, "loadbalancer") && missing(lower) {
			return &contracts.Problem{Key: i18n.AliyunErrorLoadBalancer, Params: map[string]any{"id": b.id, "region": b.region}}
		}
		return nil
	})
}

// name returns the name of the load balancer.
func (b *albBalancer) name(ctx context.Context) (string, error) {
	var out struct {
		LoadBalancerName string `json:"LoadBalancerName"`
	}
	if err := b.api.do(ctx, "GetLoadBalancerAttribute", map[string]string{"LoadBalancerId": b.id}, nil, &out); err != nil {
		return "", b.explain(err)
	}
	return out.LoadBalancerName, nil
}

// all returns the IDs of the HTTPS and QUIC listeners of the load
// balancer.
func (b *albBalancer) all(ctx context.Context) ([]string, error) {
	var ids []string
	for _, protocol := range []string{"HTTPS", "QUIC"} {
		token := ""
		for range maxPages {
			var out struct {
				NextToken string `json:"NextToken"`
				Listeners []struct {
					ListenerID string `json:"ListenerId"`
				} `json:"Listeners"`
			}
			query := map[string]string{"LoadBalancerIds.1": b.id, "ListenerProtocol": protocol, "MaxResults": "100"}
			if token != "" {
				query["NextToken"] = token
			}
			if err := b.api.do(ctx, "ListListeners", query, nil, &out); err != nil {
				return nil, b.explain(err)
			}
			for _, l := range out.Listeners {
				ids = append(ids, l.ListenerID)
			}
			if token = out.NextToken; token == "" || len(out.Listeners) == 0 {
				break
			}
		}
	}
	return ids, nil
}

// Test finds the load balancer and its HTTPS and QUIC listeners, changing
// nothing.
func (b *albBalancer) Test(ctx context.Context) (contracts.Message, error) {
	name, err := b.name(ctx)
	if err != nil {
		return contracts.Message{}, err
	}
	ids, err := b.all(ctx)
	if err != nil {
		return contracts.Message{}, err
	}
	if name == "" {
		name = b.id
	}
	return contracts.Message{Key: i18n.AliyunTestAlbOk, Params: map[string]any{"name": name, "count": len(ids)}}, nil
}

// listener is a listener as GetListenerAttribute tells it.
type listener struct {
	LoadBalancerID string `json:"LoadBalancerId"`
	ListenerStatus string `json:"ListenerStatus"`
	Certificates   []struct {
		CertificateID string `json:"CertificateId"`
	} `json:"Certificates"`
}

// attribute returns listener id, waiting while it is being configured.
func (b *albBalancer) attribute(ctx context.Context, id string, log func(contracts.LogLine)) (listener, error) {
	for try := range albTries {
		var l listener
		if err := b.api.do(ctx, "GetListenerAttribute", map[string]string{"ListenerId": id}, nil, &l); err != nil {
			code, _ := aliyunapi.ErrorCode(err)
			if lower := strings.ToLower(code); strings.Contains(lower, "listener") && missing(lower) {
				return l, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorAlbListener, Params: map[string]any{"id": b.id, "listener": id}, Err: err})
			}
			return l, b.explain(err)
		}
		if l.ListenerStatus != "Configuring" {
			return l, nil
		}
		if try == 0 {
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogAlbWait, Params: map[string]any{"listener": id}})
		}
		select {
		case <-ctx.Done():
			return l, ctx.Err()
		case <-time.After(albPoll):
		}
	}
	return listener{}, &contracts.Problem{Key: i18n.AliyunErrorAlbBusy, Params: map[string]any{"listener": id}}
}

// Deploy uploads the certificate, unless an earlier attempt did, sets it
// on the listeners, and deletes those JEC uploaded before, if set to.
func (b *albBalancer) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := logger(req)
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("aliyun: no certificate to deploy"))
	}
	if _, err := b.name(ctx); err != nil {
		return none, err
	}
	ids := b.listeners
	if len(ids) == 0 {
		var err error
		if ids, err = b.all(ctx); err != nil {
			return none, err
		}
		if len(ids) == 0 {
			return none, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorAlbNoListeners, Params: map[string]any{"id": b.id}})
		}
	}
	id, err := casUpload(ctx, b.cas, b.casEndpoint, req, log)
	if err != nil {
		return none, err
	}
	cert, err := casIdentifier(ctx, b.cas, b.casEndpoint, b.casRegion, id)
	if err != nil {
		return none, err
	}
	for _, l := range ids {
		if b.additional {
			err = b.setAdditional(ctx, l, cert, req.Previous, log)
		} else {
			err = b.setDefault(ctx, l, cert, log)
		}
		if err != nil {
			return none, err
		}
	}
	res := contracts.DeployResult{Ref: id}
	if b.deleteOld {
		// The listeners let go of the old certificates once configured.
		for _, l := range ids {
			if _, err := b.attribute(ctx, l, log); err != nil {
				return none, err
			}
		}
		res.Removed = removeOld(req.Previous, id, log, func(old string) (bool, error) {
			err := b.cas.do(ctx, "DeleteUserCertificate", map[string]string{"CertId": old}, nil, nil)
			code, _ := aliyunapi.ErrorCode(err)
			return missing(strings.ToLower(code)), err
		})
	}
	return res, nil
}

// setDefault makes cert the default certificate of listener id, unless it
// is already.
func (b *albBalancer) setDefault(ctx context.Context, id, cert string, log func(contracts.LogLine)) error {
	l, err := b.attribute(ctx, id, log)
	if err != nil {
		return err
	}
	if l.LoadBalancerID != "" && l.LoadBalancerID != b.id {
		return retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorAlbListener, Params: map[string]any{"id": b.id, "listener": id}})
	}
	if len(l.Certificates) > 0 && l.Certificates[0].CertificateID == cert {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogAlbAlready, Params: map[string]any{"listener": id}})
		return nil
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogAlbDefault, Params: map[string]any{"listener": id}})
	err = b.api.do(ctx, "UpdateListenerAttribute", map[string]string{"ListenerId": id, "Certificates.1.CertificateId": cert}, nil, nil)
	if err != nil {
		return b.explain(err)
	}
	return nil
}

// setAdditional adds cert to the additional certificates of listener id,
// unless it is there, and takes away those of previous, the versions JEC
// deployed before.
func (b *albBalancer) setAdditional(ctx context.Context, id, cert string, previous []string, log func(contracts.LogLine)) error {
	l, err := b.attribute(ctx, id, log)
	if err != nil {
		return err
	}
	if l.LoadBalancerID != "" && l.LoadBalancerID != b.id {
		return retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorAlbListener, Params: map[string]any{"id": b.id, "listener": id}})
	}
	have, err := b.additionalOf(ctx, id)
	if err != nil {
		return err
	}
	if slices.Contains(have, cert) {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogAlbAlready, Params: map[string]any{"listener": id}})
	} else {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogAlbAdditional, Params: map[string]any{"listener": id}})
		err := b.api.do(ctx, "AssociateAdditionalCertificatesWithListener", map[string]string{"ListenerId": id, "Certificates.1.CertificateId": cert}, nil, nil)
		if err != nil {
			return b.explain(err)
		}
	}
	var old []string
	for _, c := range have {
		bare, _, _ := strings.Cut(c, "-")
		if c != cert && slices.Contains(previous, bare) {
			old = append(old, c)
		}
	}
	for chunk := range slices.Chunk(old, 10) {
		if _, err := b.attribute(ctx, id, log); err != nil {
			return err
		}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogAlbDissociate, Params: map[string]any{"listener": id, "ids": strings.Join(chunk, ", ")}})
		query := map[string]string{"ListenerId": id}
		for i, c := range chunk {
			query["Certificates."+strconv.Itoa(i+1)+".CertificateId"] = c
		}
		if err := b.api.do(ctx, "DissociateAdditionalCertificatesFromListener", query, nil, nil); err != nil {
			return b.explain(err)
		}
	}
	return nil
}

// additionalOf returns the additional server certificates of listener id.
func (b *albBalancer) additionalOf(ctx context.Context, id string) ([]string, error) {
	var have []string
	token := ""
	for range maxPages {
		var out struct {
			NextToken    string `json:"NextToken"`
			Certificates []struct {
				CertificateID string `json:"CertificateId"`
				IsDefault     bool   `json:"IsDefault"`
				Status        string `json:"Status"`
			} `json:"Certificates"`
		}
		query := map[string]string{"ListenerId": id, "CertificateType": "Server", "MaxResults": "100"}
		if token != "" {
			query["NextToken"] = token
		}
		if err := b.api.do(ctx, "ListListenerCertificates", query, nil, &out); err != nil {
			return nil, b.explain(err)
		}
		for _, c := range out.Certificates {
			if !c.IsDefault && !strings.EqualFold(c.Status, "Dissociating") {
				have = append(have, c.CertificateID)
			}
		}
		if token = out.NextToken; token == "" || len(out.Certificates) == 0 {
			break
		}
	}
	return have, nil
}
