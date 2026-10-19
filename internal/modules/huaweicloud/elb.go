package huaweicloud

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	hwbasic "github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	hwconfig "github.com/huaweicloud/huaweicloud-sdk-go-v3/core/config"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
	hwelb "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3"
	elbmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3/model"
	elbregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3/region"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

func elbType() providers.Type {
	return providers.Type{
		ID: "huaweicloud.elb", Kind: providers.Target, Name: i18n.HuaweicloudElbName, Description: i18n.HuaweicloudElbDescription, Untested: true,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.HuaweicloudFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "secret_access_key", Label: i18n.HuaweicloudFieldSecretAccessKey, Help: i18n.HuaweicloudFieldElbKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "region", Label: i18n.HuaweicloudFieldRegion, Help: i18n.HuaweicloudFieldRegionHelp, Kind: providers.Text, Required: true, Default: "cn-north-4"},
			{Key: "certificate_id", Label: i18n.HuaweicloudFieldCertificateId, Help: i18n.HuaweicloudFieldCertificateIdHelp, Kind: providers.Text},
			{Key: "listeners", Label: i18n.HuaweicloudFieldListeners, Help: i18n.HuaweicloudFieldListenersHelp, Kind: providers.Text},
			{Key: "project_id", Label: i18n.HuaweicloudFieldProjectId, Help: i18n.HuaweicloudFieldProjectIdHelp, Kind: providers.Text, Advanced: true},
		},
		Credential: credentials.Use(credentials.HuaweiCloud),
	}
}

// elb is the certificates and listeners of Huawei Cloud's ELB in a region.
// JEC keeps one certificate there and updates it in place, which the
// listeners that use it serve at once. As for the CDN, it keeps the calls
// it makes, not the SDK's client.
type elb struct {
	create         func(*elbmodel.CreateCertificateRequest) (*elbmodel.CreateCertificateResponse, error)
	update         func(*elbmodel.UpdateCertificateRequest) (*elbmodel.UpdateCertificateResponse, error)
	show           func(*elbmodel.ShowCertificateRequest) (*elbmodel.ShowCertificateResponse, error)
	list           func(*elbmodel.ListCertificatesRequest) (*elbmodel.ListCertificatesResponse, error)
	showListener   func(*elbmodel.ShowListenerRequest) (*elbmodel.ShowListenerResponse, error)
	updateListener func(*elbmodel.UpdateListenerRequest) (*elbmodel.UpdateListenerResponse, error)
	certificate    string // the setting: the certificate to update
	listeners      []string
}

var (
	_ contracts.Target = (*elb)(nil)
	_ contracts.Tester = (*elb)(nil)
)

func (m *Module) elbFactory(_ context.Context, in providers.Instance) (any, error) {
	auth := hwbasic.NewCredentialsBuilder().
		WithAk(strings.TrimSpace(in.Value("access_key_id"))).WithSk(strings.TrimSpace(in.Value("secret_access_key")))
	if id := strings.TrimSpace(in.Config["project_id"]); id != "" {
		auth = auth.WithProjectId(id)
	}
	cred, err := auth.SafeBuild()
	if err != nil {
		return nil, retry.Permanent(err)
	}
	builder := hwelb.ElbClientBuilder().WithCredential(cred).
		WithHttpConfig(hwconfig.DefaultHttpConfig().WithTimeout(timeout).WithRetries(0))
	name := strings.TrimSpace(in.Config["region"])
	if m.endpoint != "" {
		builder = builder.WithEndpoints([]string{m.endpoint})
	} else {
		region, err := elbregion.SafeValueOf(name)
		if err != nil {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.HuaweicloudErrorRegion, Params: map[string]any{"region": name}, Err: err})
		}
		builder = builder.WithRegion(region)
	}
	hc, err := builder.SafeBuild()
	if err != nil {
		// Without a project set, the SDK looks the region's up as it
		// builds the client.
		var e *sdkerr.ServiceResponseError
		if errors.As(err, &e) {
			return nil, explain(err, "")
		}
		return nil, &contracts.Problem{Key: i18n.HuaweicloudErrorProject, Params: map[string]any{"region": name}, Err: err}
	}
	client := hwelb.NewElbClient(hc)
	return &elb{
		create: client.CreateCertificate, update: client.UpdateCertificate, show: client.ShowCertificate, list: client.ListCertificates,
		showListener: client.ShowListener, updateListener: client.UpdateListener,
		certificate: strings.TrimSpace(in.Config["certificate_id"]), listeners: split(in.Config["listeners"]),
	}, nil
}

// Close has nothing to release.
func (*elb) Close() error { return nil }

// Test reads the certificate and the listeners set, or else the list of
// certificates, changing nothing.
func (e *elb) Test(context.Context) (contracts.Message, error) {
	if e.certificate != "" {
		res, err := e.show(&elbmodel.ShowCertificateRequest{CertificateId: e.certificate})
		if err != nil {
			return contracts.Message{}, explainELB(err, e.certificate)
		}
		if res.Certificate != nil && res.Certificate.Type != "server" {
			return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.HuaweicloudErrorElbType, Params: map[string]any{"id": e.certificate}})
		}
	}
	var names []string
	for _, id := range e.listeners {
		res, err := e.showListener(&elbmodel.ShowListenerRequest{ListenerId: id})
		if err != nil {
			return contracts.Message{}, explainELB(err, id)
		}
		if res.Listener != nil {
			names = append(names, res.Listener.Name+" ("+res.Listener.Protocol+")")
		}
	}
	if len(names) > 0 {
		return contracts.Message{Key: i18n.HuaweicloudTestListeners, Params: map[string]any{"listeners": strings.Join(names, ", ")}}, nil
	}
	if _, err := e.list(&elbmodel.ListCertificatesRequest{Limit: ptr(int32(1))}); err != nil {
		return contracts.Message{}, explain(err, "")
	}
	return contracts.Message{Key: i18n.HuaweicloudTestElb}, nil
}

// Deploy updates the certificate in place: the one set, else the one the
// last deployment left, else one an earlier attempt created; when there
// is none, it creates one. Then each listener set serves it, as its
// default certificate, unless it does already.
func (e *elb) Deploy(_ context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("huaweicloud: no certificate to deploy"))
	}
	key, err := cryptox.EncodeKeyTraditional(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	chain := string(cryptox.EncodeCertificates(req.Chain...))
	name := "jec-" + strconv.FormatInt(req.CertificateID, 10)

	id := e.certificate
	if id == "" && len(req.Previous) > 0 {
		id = req.Previous[0]
		// One deleted since is made anew.
		if _, err := e.show(&elbmodel.ShowCertificateRequest{CertificateId: id}); err != nil {
			var se *sdkerr.ServiceResponseError
			if !errors.As(err, &se) || se.StatusCode != http.StatusNotFound {
				return none, explainELB(err, id)
			}
			id = ""
		}
	}
	if id == "" {
		found, err := e.list(&elbmodel.ListCertificatesRequest{Name: &[]string{name}})
		if err != nil {
			return none, explain(err, "")
		}
		if found.Certificates != nil && len(*found.Certificates) > 0 {
			id = (*found.Certificates)[0].Id
		}
	}
	if id != "" {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.HuaweicloudLogElbUpdate, Params: map[string]any{"id": id}})
		_, err := e.update(&elbmodel.UpdateCertificateRequest{CertificateId: id, Body: &elbmodel.UpdateCertificateRequestBody{
			Certificate: &elbmodel.UpdateCertificateOption{Certificate: ptr(chain), PrivateKey: ptr(string(key))},
		}})
		if err != nil {
			return none, explainELB(err, id)
		}
	} else {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.HuaweicloudLogElbCreate, Params: map[string]any{"name": name}})
		kind := elbmodel.GetCreateCertificateOptionTypeEnum().SERVER
		res, err := e.create(&elbmodel.CreateCertificateRequest{Body: &elbmodel.CreateCertificateRequestBody{
			Certificate: &elbmodel.CreateCertificateOption{Name: ptr(name), Type: &kind, Certificate: ptr(chain), PrivateKey: ptr(string(key))},
		}})
		if err != nil {
			return none, explain(err, "")
		}
		if res.Certificate == nil || res.Certificate.Id == "" {
			return none, &contracts.Problem{Key: i18n.HuaweicloudErrorApi, Params: map[string]any{"code": "-", "message": "no certificate ID"}}
		}
		id = res.Certificate.Id
		log(contracts.LogLine{Level: contracts.OK, Key: i18n.HuaweicloudLogElbCreated, Params: map[string]any{"id": id}})
	}

	for _, l := range e.listeners {
		res, err := e.showListener(&elbmodel.ShowListenerRequest{ListenerId: l})
		if err != nil {
			return none, explainELB(err, l)
		}
		if res.Listener != nil && res.Listener.DefaultTlsContainerRef == id {
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.HuaweicloudLogListenerCurrent, Params: map[string]any{"id": l}})
			continue
		}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.HuaweicloudLogListenerSet, Params: map[string]any{"id": l}})
		_, err = e.updateListener(&elbmodel.UpdateListenerRequest{ListenerId: l, Body: &elbmodel.UpdateListenerRequestBody{
			Listener: &elbmodel.UpdateListenerOption{DefaultTlsContainerRef: ptr(id)},
		}})
		if err != nil {
			return none, explainELB(err, l)
		}
	}
	return contracts.DeployResult{Ref: id}, nil
}

// explainELB is explain for a call about the certificate or listener id,
// which may not be there.
func explainELB(err error, id string) error {
	var e *sdkerr.ServiceResponseError
	if errors.As(err, &e) && e.StatusCode == http.StatusNotFound {
		return retry.Permanent(&contracts.Problem{Key: i18n.HuaweicloudErrorElbNotFound, Params: map[string]any{"id": id}, Err: err})
	}
	return explain(err, "")
}
