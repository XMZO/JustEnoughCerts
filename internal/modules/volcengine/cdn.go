package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	volc "github.com/volcengine/volc-sdk-golang/base"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// The CDN API: its address, version and region, which signing names, and
// where uploaded certificates go.
const (
	cdnHost    = "cdn.volcengineapi.com"
	cdnVersion = "2021-03-01"
	cdnRegion  = "cn-north-1"
	certCenter = "volc_cert_center"
	// maxPages bounds the pages of a list JEC reads.
	maxPages = 20
)

func cdnType() providers.Type {
	return providers.Type{
		ID: "volcengine.cdn", Kind: providers.Target, Name: i18n.VolcengineCdnName, Description: i18n.VolcengineCdnDescription,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.VolcengineFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "secret_access_key", Label: i18n.VolcengineFieldSecretAccessKey, Help: i18n.VolcengineFieldSecretHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "domains", Label: i18n.VolcengineFieldDomains, Help: i18n.VolcengineFieldDomainsHelp, Kind: providers.Text},
		},
		Credential: credentials.Use(credentials.Volcengine),
	}
}

// cdn is the CDN of an account.
type cdn struct {
	client  *http.Client
	base    string // https://cdn.volcengineapi.com; tests replace it
	cred    volc.Credentials
	domains []string // those set, or none for the certificate's own
}

var (
	_ contracts.Target = (*cdn)(nil)
	_ contracts.Tester = (*cdn)(nil)
)

func (m *Module) cdnFactory(_ context.Context, in providers.Instance) (any, error) {
	var domains []string
	for d := range strings.SplitSeq(in.Config["domains"], ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" && !slices.Contains(domains, d) {
			domains = append(domains, d)
		}
	}
	return &cdn{
		client: m.client, base: m.base,
		cred: volc.Credentials{
			AccessKeyID: strings.TrimSpace(in.Value("access_key_id")), SecretAccessKey: strings.TrimSpace(in.Value("secret_access_key")),
			Service: "CDN", Region: cdnRegion,
		},
		domains: domains,
	}, nil
}

// Close has nothing to release.
func (*cdn) Close() error { return nil }

// apiError is an error the API answered.
type apiError struct {
	status        int
	code, message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("volcengine: HTTP %d: %s: %s", e.status, e.code, e.message)
}

// call sends action with params as JSON, signed, and decodes the Result of
// the answer into out, unless nil.
func (c *cdn) call(ctx context.Context, action string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	u := c.base + "/?" + url.Values{"Action": {action}, "Version": {cdnVersion}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req = c.cred.Sign(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var env struct {
		ResponseMetadata struct {
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
		Result json.RawMessage `json:"Result"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return &apiError{status: resp.StatusCode, code: http.StatusText(resp.StatusCode), message: "not JSON"}
	}
	if e := env.ResponseMetadata.Error; e != nil && e.Code != "" {
		return &apiError{status: resp.StatusCode, code: e.Code, message: e.Message}
	}
	if resp.StatusCode >= 300 {
		return &apiError{status: resp.StatusCode, code: http.StatusText(resp.StatusCode)}
	}
	if out == nil || len(env.Result) == 0 {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// explain turns a failed call into a Problem: one that trying again
// cannot fix, as refused keys, ends the attempts.
func explain(err error) error {
	var ae *apiError
	if !errors.As(err, &ae) {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return &contracts.Problem{Key: i18n.VolcengineErrorNetwork, Params: map[string]any{"endpoint": cdnHost}, Err: httpx.Cause(err)}
	}
	lower := strings.ToLower(ae.code)
	switch {
	case ae.status == http.StatusUnauthorized, ae.status == http.StatusForbidden, strings.Contains(lower, "accesskey"),
		strings.Contains(lower, "signature"), strings.Contains(lower, "accessdenied"), strings.Contains(lower, "unauthorized"):
		return retry.Permanent(&contracts.Problem{Key: i18n.VolcengineErrorAuth, Err: err})
	case ae.status == http.StatusTooManyRequests, strings.Contains(lower, "throttl"), strings.Contains(lower, "limitexceeded"):
		return &contracts.Problem{Key: i18n.VolcengineErrorThrottled, Err: err}
	}
	p := &contracts.Problem{Key: i18n.VolcengineErrorApi, Params: map[string]any{"code": ae.code}, Err: err}
	if ae.status >= 500 {
		return p
	}
	return retry.Permanent(p)
}

// Test counts the accelerated domains of the account, changing nothing.
func (c *cdn) Test(ctx context.Context) (contracts.Message, error) {
	var out struct {
		Total int `json:"Total"`
	}
	if err := c.call(ctx, "ListCdnDomains", map[string]any{"PageNum": 1, "PageSize": 1}, &out); err != nil {
		return contracts.Message{}, explain(err)
	}
	return contracts.Message{Key: i18n.VolcengineTestCdnOk, Params: map[string]any{"count": out.Total}}, nil
}

// Deploy uploads the certificate to the certificate center, unless it is
// there, and deploys it to the accelerated domains.
func (c *cdn) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("volcengine: no certificate to deploy"))
	}
	domains := c.domains
	if len(domains) == 0 {
		for _, d := range req.Domains {
			if !strings.HasPrefix(d, "*.") {
				domains = append(domains, d)
			}
		}
	}
	if len(domains) == 0 {
		return none, retry.Permanent(&contracts.Problem{Key: i18n.VolcengineErrorNoDomains})
	}
	id, err := c.upload(ctx, req, log)
	if err != nil {
		return none, err
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.VolcengineLogDeploy, Params: map[string]any{"domains": strings.Join(domains, ", ")}})
	var out struct {
		DeployResult []struct {
			Domain   string `json:"Domain"`
			Status   string `json:"Status"`
			ErrorMsg string `json:"ErrorMsg"`
		} `json:"DeployResult"`
	}
	if err := c.call(ctx, "BatchDeployCert", map[string]any{"CertId": id, "Domain": strings.Join(domains, ",")}, &out); err != nil {
		return none, explain(err)
	}
	var failed, why []string
	for _, r := range out.DeployResult {
		if !strings.EqualFold(r.Status, "success") {
			failed, why = append(failed, r.Domain), append(why, r.Domain+": "+r.ErrorMsg)
		}
	}
	if len(failed) > 0 {
		return none, &contracts.Problem{Key: i18n.VolcengineErrorDeploy, Params: map[string]any{"domains": strings.Join(failed, ", ")},
			Err: errors.New(strings.Join(why, "; "))}
	}
	log(contracts.LogLine{Level: contracts.OK, Key: i18n.VolcengineLogDeployed})
	return contracts.DeployResult{Ref: id}, nil
}

// upload puts the certificate into the certificate center, unless an
// earlier attempt did, and returns its ID there.
func (c *cdn) upload(ctx context.Context, req contracts.DeployRequest, log func(contracts.LogLine)) (string, error) {
	fingerprint := cryptox.Fingerprint(req.Chain[0])
	for page := 1; page <= maxPages; page++ {
		var out struct {
			Total    int `json:"Total"`
			CertInfo []struct {
				CertID          string `json:"CertId"`
				CertFingerprint struct {
					Sha256 string `json:"Sha256"`
				} `json:"CertFingerprint"`
			} `json:"CertInfo"`
		}
		if err := c.call(ctx, "ListCertInfo", map[string]any{"Source": certCenter, "PageNum": page, "PageSize": 100}, &out); err != nil {
			return "", explain(err)
		}
		for _, cert := range out.CertInfo {
			if strings.EqualFold(strings.ReplaceAll(cert.CertFingerprint.Sha256, ":", ""), fingerprint) {
				log(contracts.LogLine{Level: contracts.Info, Key: i18n.VolcengineLogFound, Params: map[string]any{"id": cert.CertID}})
				return cert.CertID, nil
			}
		}
		if len(out.CertInfo) < 100 || page*100 >= out.Total {
			break
		}
	}
	key, err := cryptox.EncodeKeyTraditional(req.Key)
	if err != nil {
		return "", retry.Permanent(err)
	}
	name := "jec-" + strconv.FormatInt(req.CertificateID, 10) + "-" + fingerprint[:12]
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.VolcengineLogUpload, Params: map[string]any{"name": name}})
	var out struct {
		CertID string `json:"CertId"`
	}
	err = c.call(ctx, "AddCertificate", map[string]any{
		"Source": certCenter, "Certificate": string(cryptox.EncodeCertificates(req.Chain...)), "PrivateKey": string(key), "Desc": name,
	}, &out)
	if err != nil {
		return "", explain(err)
	}
	if out.CertID == "" {
		return "", errors.New("volcengine: AddCertificate answered no CertId")
	}
	return out.CertID, nil
}
