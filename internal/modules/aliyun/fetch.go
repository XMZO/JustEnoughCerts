package aliyun

import (
	"cmp"
	"context"
	"crypto/x509"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// The certificates the Certificate Management Service issued, bought or
// personal test ones, are taken in with the keys Alibaba Cloud made for
// them, as the console downloads them: a source of certificates with keys
// of their own (contracts.KeySource). Renewing or reissuing a certificate
// there gives JEC the next one to take in; those uploaded there are left
// alone, as JEC's own.

// recheck is how long a certificate due for renewal waits before the
// service is looked at again for the next one.
const recheck = 6 * time.Hour

func fetchType() providers.Type {
	return providers.Type{
		ID: "aliyun.fetch", Kind: providers.Source, OwnKey: true, Name: i18n.AliyunFetchName, Description: i18n.AliyunFetchDescription,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.AliyunFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "access_key_secret", Label: i18n.AliyunFieldAccessKeySecret, Help: i18n.AliyunFieldFetchSecretHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "site", Label: i18n.AliyunFieldSite, Help: i18n.AliyunFieldFetchSiteHelp, Kind: providers.Select, Required: true, Default: casChina, Options: []providers.Option{
				{Value: casChina, Label: i18n.AliyunSiteChina},
				{Value: casIntl, Label: i18n.AliyunSiteIntl},
			}},
		},
	}
}

// fetcher takes in the certificates the service of an account issued.
type fetcher struct {
	api      rpc
	endpoint string
}

var (
	_ contracts.KeySource = (*fetcher)(nil)
	_ contracts.Tester    = (*fetcher)(nil)
)

func (m *Module) fetchFactory(_ context.Context, in providers.Instance) (any, error) {
	endpoint := casEndpoint(in.Config["site"])
	api, err := m.rpc(endpoint, casVersion, strings.TrimSpace(in.Value("access_key_id")), strings.TrimSpace(in.Value("access_key_secret")))
	if err != nil {
		return nil, retry.Permanent(err)
	}
	return &fetcher{api: api, endpoint: endpoint}, nil
}

// Close has nothing to release.
func (*fetcher) Close() error { return nil }

// Test counts the certificates of the service, which needs the AccessKey
// to work.
func (f *fetcher) Test(ctx context.Context) (contracts.Message, error) {
	var out struct {
		TotalCount int `json:"TotalCount"`
	}
	err := f.api.do(ctx, "ListUserCertificateOrder", map[string]string{"OrderType": "CERT", "CurrentPage": "1", "ShowSize": "1"}, nil, &out)
	if err != nil {
		return contracts.Message{}, explainRPC(err, f.endpoint, noneAbsent)
	}
	return contracts.Message{Key: i18n.AliyunTestFetchOk, Params: map[string]any{"count": out.TotalCount}}, nil
}

// listed is a certificate of the service, as its list shows it.
type listed struct {
	CertificateID anyID  `json:"CertificateId"`
	CommonName    string `json:"CommonName"`
	Sans          string `json:"Sans"`
	EndDate       string `json:"EndDate"` // YYYY-MM-DD
	Upload        bool   `json:"Upload"`
	Expired       bool   `json:"Expired"`
}

// names are the domains of the certificate.
func (c listed) names() []string {
	names := []string{c.CommonName}
	for n := range strings.SplitSeq(c.Sans, ",") {
		names = append(names, strings.TrimSpace(n))
	}
	return names
}

// later tells whether c ends after o, or on the same day with a later ID,
// as a reissued certificate does.
func (c listed) later(o listed) bool {
	if c.EndDate != o.EndDate {
		return c.EndDate > o.EndDate
	}
	a, errA := strconv.ParseInt(string(c.CertificateID), 10, 64)
	b, errB := strconv.ParseInt(string(o.CertificateID), 10, 64)
	if errA != nil || errB != nil {
		return c.CertificateID > o.CertificateID
	}
	return a > b
}

// Collect takes in the newest certificate the service issued for the
// domains, unless it is the one in use or older: then the certificate
// waits for the next one, which renewing or reissuing it there makes.
func (f *fetcher) Collect(ctx context.Context, req contracts.CollectRequest) (*contracts.Collected, error) {
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	domains := strings.Join(req.Domains, ", ")
	log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.AliyunLogFetchLook, Params: map[string]any{"domains": domains}})
	best, err := f.newest(ctx, req.Domains)
	if err != nil {
		return nil, explainRPC(err, f.endpoint, noneAbsent)
	}
	if best == nil {
		return nil, &contracts.Problem{Key: i18n.AliyunErrorFetchNone, Params: map[string]any{"domains": domains}}
	}
	id := string(best.CertificateID)
	var detail struct {
		Cert string `json:"Cert"`
		Key  string `json:"Key"`
	}
	if err := f.api.do(ctx, "GetUserCertificateDetail", map[string]string{"CertId": id}, nil, &detail); err != nil {
		return nil, explainRPC(err, f.endpoint, noneAbsent)
	}
	if strings.TrimSpace(detail.Key) == "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorFetchNoKey, Params: map[string]any{"id": id}})
	}
	b, err := cryptox.ParseBundle([]byte(detail.Cert), []byte(detail.Key))
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorFetchBad, Params: map[string]any{"id": id}, Err: err})
	}
	if !cryptox.Covers(cryptox.Names(b.Leaf), req.Domains) {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorFetchNone, Params: map[string]any{"domains": domains}})
	}
	if req.Current != "" && (cryptox.Fingerprint(b.Leaf) == req.Current || b.Leaf.NotAfter.Before(req.CurrentEnd)) {
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.AliyunLogFetchSame,
			Params: map[string]any{"domains": domains, "id": id, "hours": int(recheck.Hours())}})
		return nil, &contracts.Wait{After: recheck}
	}
	if len(b.Chain) == 0 {
		log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepDownload, Key: i18n.AliyunLogFetchNoChain, Params: map[string]any{"id": id}})
	}
	log(contracts.LogLine{Level: contracts.OK, Step: contracts.StepDownload, Key: i18n.AliyunLogFetchFound,
		Params: map[string]any{"id": id, "date": b.Leaf.NotAfter.Format(time.DateOnly)}})
	return &contracts.Collected{Chain: append([]*x509.Certificate{b.Leaf}, b.Chain...), Key: b.Key}, nil
}

// newest returns the certificate the service issued, not one uploaded
// there, that covers domains and ends last; nil when there is none.
func (f *fetcher) newest(ctx context.Context, domains []string) (*listed, error) {
	var best *listed
	for page := 1; page <= maxPages; page++ {
		var out struct {
			CertificateOrderList []listed `json:"CertificateOrderList"`
		}
		// With no status, the list has the valid certificates only.
		query := map[string]string{"OrderType": "CERT", "Keyword": strings.TrimPrefix(cmp.Or(domains...), "*."), "CurrentPage": strconv.Itoa(page), "ShowSize": "50"}
		if err := f.api.do(ctx, "ListUserCertificateOrder", query, nil, &out); err != nil {
			return nil, err
		}
		for _, c := range out.CertificateOrderList {
			if !c.Upload && !c.Expired && cryptox.Covers(c.names(), domains) && (best == nil || c.later(*best)) {
				best = &c
			}
		}
		if len(out.CertificateOrderList) < 50 {
			break
		}
	}
	return best, nil
}
