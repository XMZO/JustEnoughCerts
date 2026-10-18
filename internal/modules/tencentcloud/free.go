package tencentcloud

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// Free certificates are applied for at SSL Certificates (TrustAsia DV,
// three months, one domain): Tencent Cloud makes the key, adds the
// validation record to the domain's zone in DNSPod of the same account
// (DNS_AUTO) and takes it away once issued; JEC follows the order and
// takes the certificate in with its key, out of the ZIP archive. So it is
// a source of certificates with keys of their own (contracts.KeySource)
// that keeps the order between its attempts.

// freePoll is how often an order is looked at.
const freePoll = 2 * time.Minute

func freeType() providers.Type {
	return providers.Type{
		ID: "tencentcloud.free", Kind: providers.Source, OwnKey: true, Name: i18n.TencentcloudFreeName, Description: i18n.TencentcloudFreeDescription,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "secret_id", Label: i18n.TencentcloudFieldSecretId, Kind: providers.Text, Required: true},
			{Key: "secret_key", Label: i18n.TencentcloudFieldSecretKey, Help: i18n.TencentcloudFieldFreeSecretKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "key_algorithm", Label: i18n.TencentcloudFieldKeyAlgorithm, Kind: providers.Select, Default: "RSA", Options: []providers.Option{
				{Value: "RSA", Label: i18n.TencentcloudKeyRsa},
				{Value: "ECC", Label: i18n.TencentcloudKeyEcc},
			}},
			{Key: "site", Label: i18n.TencentcloudFieldSite, Kind: providers.Select, Default: siteChina, Advanced: true, Options: []providers.Option{
				{Value: siteChina, Label: i18n.TencentcloudSiteChina},
				{Value: siteIntl, Label: i18n.TencentcloudSiteInternational},
			}},
		},
	}
}

// freeCerts applies for free certificates in an account.
type freeCerts struct {
	api      sslAPI
	endpoint string
	algo     string // RSA or ECC
}

var (
	_ contracts.KeySource = (*freeCerts)(nil)
	_ contracts.Tester    = (*freeCerts)(nil)
)

func (m *Module) freeFactory(_ context.Context, in providers.Instance) (any, error) {
	endpoint := endpointChina
	if in.Config["site"] == siteIntl {
		endpoint = endpointIntl
	}
	api, err := m.open(strings.TrimSpace(in.Value("secret_id")), strings.TrimSpace(in.Value("secret_key")), endpoint)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	algo := "RSA"
	if in.Config["key_algorithm"] == "ECC" {
		algo = "ECC"
	}
	return &freeCerts{api: api, endpoint: endpoint, algo: algo}, nil
}

// Close has nothing to release.
func (*freeCerts) Close() error { return nil }

// Test counts the certificates of the account, which needs the keys to
// work.
func (f *freeCerts) Test(ctx context.Context) (contracts.Message, error) {
	n, err := f.api.count(ctx)
	if err != nil {
		return contracts.Message{}, explain(err, f.endpoint)
	}
	return contracts.Message{Key: i18n.TencentcloudTestFetchOk, Params: map[string]any{"count": n}}, nil
}

// freeOrder is what an issuance keeps between its attempts.
type freeOrder struct {
	// Cert is the ID of the certificate applied for.
	Cert string `json:"cert,omitempty"`
	// Completed tells that the CA was asked to check the validation.
	Completed bool `json:"completed,omitempty"`
	// Seen is what the last look found, logged once.
	Seen string `json:"seen,omitempty"`
}

// wait ends an attempt that waits for the order.
func (o *freeOrder) wait() error {
	state, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return &contracts.Wait{State: state, After: freePoll}
}

// Collect applies for the certificate unless an earlier attempt did,
// waits while the CA validates and reviews it, and takes it in once
// issued. An order the CA refused, or one gone, makes the next issuance
// apply anew.
func (f *freeCerts) Collect(ctx context.Context, req contracts.CollectRequest) (*contracts.Collected, error) {
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Domains) != 1 || strings.HasPrefix(req.Domains[0], "*.") {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorFreeDomains, Params: map[string]any{"domains": strings.Join(req.Domains, ", ")}})
	}
	domain := req.Domains[0]
	var o freeOrder
	if len(req.State) > 0 && json.Unmarshal(req.State, &o) != nil {
		o = freeOrder{}
	}
	if o.Cert == "" {
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.TencentcloudLogFreeApply, Params: map[string]any{"domain": domain}})
		id, err := f.api.apply(ctx, domain, f.algo)
		if err != nil {
			return nil, explainApply(err, f.endpoint)
		}
		o = freeOrder{Cert: id}
	}
	st, err := f.api.status(ctx, o.Cert)
	switch {
	case errorCode(err) == "FailedOperation.CertificateNotFound":
		return nil, contracts.Restart(&contracts.Problem{Key: i18n.TencentcloudErrorFreeGone, Params: map[string]any{"id": o.Cert}, Err: err})
	case err != nil:
		return nil, explain(err, f.endpoint)
	}
	switch st.Status {
	case statusIssued:
		return f.download(ctx, o.Cert, req.Domains, log)
	case statusRefused:
		return nil, contracts.Restart(retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorFreeRefused,
			Params: map[string]any{"id": o.Cert, "reason": st.Message}}))
	case statusExpired, statusCancelled, statusRevoked, statusRefunded:
		return nil, contracts.Restart(retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorFreeGone, Params: map[string]any{"id": o.Cert}}))
	case statusReviewing:
		if !o.Completed {
			// The CA checks the validation now rather than in a while; it
			// would anyway, so a failure changes nothing.
			_ = f.api.complete(ctx, o.Cert)
			o.Completed = true
		}
	}
	if seen := "status:" + strconv.Itoa(st.Status); o.Seen != seen {
		o.Seen = seen
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepValidate, Key: i18n.TencentcloudLogFreeWait,
			Params: map[string]any{"id": o.Cert, "status": st.Name}})
	}
	return nil, o.wait()
}

// download takes in issued certificate id with its key.
func (f *freeCerts) download(ctx context.Context, id string, domains []string, log func(contracts.LogLine)) (*contracts.Collected, error) {
	data, err := f.api.download(ctx, id)
	if err != nil {
		return nil, explain(err, f.endpoint)
	}
	b, err := bundleOf(data, domains)
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorFetchBad, Params: map[string]any{"id": id}, Err: err})
	}
	log(contracts.LogLine{Level: contracts.OK, Step: contracts.StepDownload, Key: i18n.TencentcloudLogFetchFound,
		Params: map[string]any{"id": id, "date": b.Leaf.NotAfter.Format(time.DateOnly)}})
	return &contracts.Collected{Chain: append([]*x509.Certificate{b.Leaf}, b.Chain...), Key: b.Key}, nil
}

// explainApply explains a refused application: the quota of free
// certificates spent, or what explain says.
func explainApply(err error, endpoint string) error {
	switch code := errorCode(err); {
	case strings.Contains(code, "FreeLimit"), strings.Contains(code, "CountLimit"):
		return retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorFreeQuota, Err: err})
	case errors.Is(err, context.Canceled):
		return err
	}
	return explain(err, endpoint)
}
