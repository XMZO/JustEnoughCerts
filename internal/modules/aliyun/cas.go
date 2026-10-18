package aliyun

import (
	"context"
	"encoding/json"
	"errors"
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

// The Certificate Management Service (CAS, 2020-04-07): each new
// certificate is uploaded there, from where Alibaba Cloud's products,
// such as ALB, OSS or WAF, take it; the certificates JEC uploaded before
// are deleted, but for those still in use. The calls are those of the
// SDK, which certimate uses too; only the certificate and the key go in
// the form, not the URL.

// The sites of the service, by region.
const (
	casChina   = "cn-hangzhou"
	casIntl    = "ap-southeast-1"
	casVersion = "2020-04-07"
)

func casEndpoint(site string) string {
	if site == casIntl {
		return "cas.ap-southeast-1.aliyuncs.com"
	}
	return "cas.aliyuncs.com"
}

func casType() providers.Type {
	return providers.Type{
		ID: "aliyun.cas", Kind: providers.Target, Name: i18n.AliyunCasName, Description: i18n.AliyunCasDescription,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.AliyunFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "access_key_secret", Label: i18n.AliyunFieldAccessKeySecret, Help: i18n.AliyunFieldCasSecretHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "site", Label: i18n.AliyunFieldSite, Kind: providers.Select, Required: true, Default: casChina, Options: []providers.Option{
				{Value: casChina, Label: i18n.AliyunSiteChina},
				{Value: casIntl, Label: i18n.AliyunSiteIntl},
			}},
			deleteOldField,
		},
		Credential: credentials.Use(credentials.Aliyun),
	}
}

// deleteOldField is the switch of a target that deletes what JEC put
// there before.
var deleteOldField = providers.Field{Key: "delete_old", Label: i18n.AliyunFieldDeleteOld, Help: i18n.AliyunFieldDeleteOldHelp, Kind: providers.Switch, Default: "true"}

// certStore is the Certificate Management Service of an account.
type certStore struct {
	api       rpc
	endpoint  string
	deleteOld bool
}

var (
	_ contracts.Target = (*certStore)(nil)
	_ contracts.Tester = (*certStore)(nil)
)

func (m *Module) casFactory(_ context.Context, in providers.Instance) (any, error) {
	endpoint := casEndpoint(in.Config["site"])
	api, err := m.rpc(endpoint, casVersion, strings.TrimSpace(in.Value("access_key_id")), strings.TrimSpace(in.Value("access_key_secret")))
	if err != nil {
		return nil, retry.Permanent(err)
	}
	return &certStore{api: api, endpoint: endpoint, deleteOld: in.Config["delete_old"] != "false"}, nil
}

// Close has nothing to release.
func (*certStore) Close() error { return nil }

// Test lists the uploaded certificates, which needs the AccessKey to work.
func (c *certStore) Test(ctx context.Context) (contracts.Message, error) {
	var out struct {
		TotalCount int `json:"TotalCount"`
	}
	err := c.api.do(ctx, "ListUserCertificateOrder", map[string]string{"OrderType": "UPLOAD", "CurrentPage": "1", "ShowSize": "1"}, nil, &out)
	if err != nil {
		return contracts.Message{}, explainRPC(err, c.endpoint, noneAbsent)
	}
	return contracts.Message{Key: i18n.AliyunTestCasOk, Params: map[string]any{"count": out.TotalCount}}, nil
}

// Deploy uploads the certificate, unless an earlier attempt did, and
// deletes those JEC uploaded before, if set to.
func (c *certStore) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := logger(req)
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("aliyun: no certificate to deploy"))
	}
	id, err := casUpload(ctx, c.api, c.endpoint, req, log)
	if err != nil {
		return none, err
	}
	res := contracts.DeployResult{Ref: id}
	if c.deleteOld {
		res.Removed = removeOld(req.Previous, id, log, func(old string) (bool, error) {
			err := c.api.do(ctx, "DeleteUserCertificate", map[string]string{"CertId": old}, nil, nil)
			code, _ := aliyunapi.ErrorCode(err)
			return missing(strings.ToLower(code)), err
		})
	}
	return res, nil
}

// casUpload puts the certificate of req into the Certificate Management
// Service, unless it is there from an earlier attempt, and returns its ID.
func casUpload(ctx context.Context, api rpc, endpoint string, req contracts.DeployRequest, log func(contracts.LogLine)) (string, error) {
	name := certName(req)
	id, err := casFind(ctx, api, name, req.Domains)
	if err != nil {
		return "", explainRPC(err, endpoint, noneAbsent)
	}
	if id != "" {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogCasFound, Params: map[string]any{"name": name}})
		return id, nil
	}
	key, err := cryptox.EncodeKeyTraditional(req.Key)
	if err != nil {
		return "", retry.Permanent(err)
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogCasUpload, Params: map[string]any{"name": name}})
	var out struct {
		CertID anyID `json:"CertId"`
	}
	form := map[string]string{"Cert": string(cryptox.EncodeCertificates(req.Chain...)), "Key": string(key)}
	if err := api.do(ctx, "UploadUserCertificate", map[string]string{"Name": name}, form, &out); err != nil {
		return "", explainRPC(err, endpoint, noneAbsent)
	}
	if out.CertID == "" {
		return "", errors.New("aliyun: UploadUserCertificate answered no CertId")
	}
	return string(out.CertID), nil
}

// casFind returns the ID of the uploaded certificate called name, among
// those of domains, or "" when there is none. The search is by domain:
// it does not match names.
func casFind(ctx context.Context, api rpc, name string, domains []string) (string, error) {
	keyword := ""
	if len(domains) > 0 {
		keyword = strings.TrimPrefix(domains[0], "*.")
	}
	for page := 1; page <= maxPages; page++ {
		var out struct {
			CertificateOrderList []struct {
				CertificateID anyID  `json:"CertificateId"`
				Name          string `json:"Name"`
			} `json:"CertificateOrderList"`
		}
		query := map[string]string{"OrderType": "UPLOAD", "Keyword": keyword, "CurrentPage": strconv.Itoa(page), "ShowSize": "50"}
		if err := api.do(ctx, "ListUserCertificateOrder", query, nil, &out); err != nil {
			return "", err
		}
		for _, c := range out.CertificateOrderList {
			if c.Name == name {
				return string(c.CertificateID), nil
			}
		}
		if len(out.CertificateOrderList) < 50 {
			break
		}
	}
	return "", nil
}

// maxPages bounds the pages of a list JEC reads.
const maxPages = 20

// certName is what JEC calls the certificate of req where it uploads it:
// the same for each attempt at a version, and for no other version.
func certName(req contracts.DeployRequest) string {
	return "jec-" + strconv.FormatInt(req.CertificateID, 10) + "-" + cryptox.Fingerprint(req.Chain[0])[:12]
}

// removeOld deletes the refs of previous but keep with del, and returns
// those gone: deleted, or not found. Those del fails to delete, as one
// still in use, stay for a later deployment.
func removeOld(previous []string, keep string, log func(contracts.LogLine), del func(ref string) (gone bool, err error)) []string {
	var removed []string
	for _, ref := range previous {
		if ref == "" || ref == keep {
			continue
		}
		gone, err := del(ref)
		switch {
		case err == nil, gone:
			removed = append(removed, ref)
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogDeleted, Params: map[string]any{"id": ref}})
		default:
			code, _ := aliyunapi.ErrorCode(err)
			log(contracts.LogLine{Level: contracts.Warn, Key: i18n.AliyunLogKept, Params: map[string]any{"id": ref, "code": code}, Detail: err.Error()})
		}
	}
	return removed
}

// logger is where req's log lines go.
func logger(req contracts.DeployRequest) func(contracts.LogLine) {
	if req.Log == nil {
		return func(contracts.LogLine) {}
	}
	return req.Log
}

// noneAbsent knows no code that says something is not there.
func noneAbsent(string) *contracts.Problem { return nil }

// anyID is an ID in an answer, which comes as a number or as a string.
type anyID string

func (a *anyID) UnmarshalJSON(b []byte) error {
	var n json.Number
	if json.Unmarshal(b, &n) == nil {
		*a = anyID(n.String())
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*a = anyID(s)
	return nil
}
