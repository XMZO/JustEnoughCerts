package baiducloud

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
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// cdnHost is the address of the CDN API, and maxPages bounds the pages of
// a list JEC reads.
const (
	cdnHost  = "cdn.baidubce.com"
	maxPages = 20
)

func cdnType() providers.Type {
	return providers.Type{
		ID: "baiducloud.cdn", Kind: providers.Target, Name: i18n.BaiducloudCdnName, Description: i18n.BaiducloudCdnDescription, Untested: true,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "access_key", Label: i18n.BaiducloudFieldAccessKey, Kind: providers.Text, Required: true},
			{Key: "secret_key", Label: i18n.BaiducloudFieldSecretKey, Help: i18n.BaiducloudFieldSecretHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "domains", Label: i18n.BaiducloudFieldDomains, Help: i18n.BaiducloudFieldDomainsHelp, Kind: providers.Text},
		},
		Credential: credentials.Use(credentials.BaiduCloud),
	}
}

// cdn is the CDN of an account.
type cdn struct {
	client  *http.Client
	base    string // https://cdn.baidubce.com; tests replace it
	ak, sk  string
	domains []string // those set, or none for the certificate's own
	now     func() time.Time
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
		client: m.client, base: m.base, now: time.Now, domains: domains,
		ak: strings.TrimSpace(in.Value("access_key")), sk: strings.TrimSpace(in.Value("secret_key")),
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
	return fmt.Sprintf("baiducloud: HTTP %d: %s: %s", e.status, e.code, e.message)
}

// call sends a signed request to path, with query, and body as JSON
// unless nil, and decodes the answer of a success into out, unless nil.
func (c *cdn) call(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var data []byte
	if body != nil {
		var err error
		if data, err = json.Marshal(body); err != nil {
			return err
		}
	}
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json;charset=utf-8")
	sign(req, c.ak, c.sk, data, c.now())
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(answer, &e)
		if e.Code == "" {
			e.Code = http.StatusText(resp.StatusCode)
		}
		return &apiError{status: resp.StatusCode, code: e.Code, message: e.Message}
	}
	if out == nil || len(bytes.TrimSpace(answer)) == 0 {
		return nil
	}
	return json.Unmarshal(answer, out)
}

// explain turns a failed call about domain, if any, into a Problem: one
// that trying again cannot fix, as refused keys, ends the attempts.
func explain(err error, domain string) error {
	var ae *apiError
	if !errors.As(err, &ae) {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return &contracts.Problem{Key: i18n.BaiducloudErrorNetwork, Params: map[string]any{"endpoint": cdnHost}, Err: httpx.Cause(err)}
	}
	lower := strings.ToLower(ae.code)
	switch {
	case ae.status == http.StatusUnauthorized, ae.status == http.StatusForbidden, strings.Contains(lower, "accessdenied"), strings.Contains(lower, "signature"):
		return retry.Permanent(&contracts.Problem{Key: i18n.BaiducloudErrorAuth, Err: err})
	case ae.status == http.StatusTooManyRequests, strings.Contains(lower, "throttl"), strings.Contains(lower, "toomanyrequests"):
		return &contracts.Problem{Key: i18n.BaiducloudErrorThrottled, Err: err}
	case ae.status == http.StatusNotFound && domain != "":
		return retry.Permanent(&contracts.Problem{Key: i18n.BaiducloudErrorDomain, Params: map[string]any{"domain": domain}, Err: err})
	}
	p := &contracts.Problem{Key: i18n.BaiducloudErrorApi, Params: map[string]any{"code": ae.code}, Err: err}
	if ae.status >= 500 {
		return p
	}
	return retry.Permanent(p)
}

// Test counts the accelerated domains of the account, changing nothing.
func (c *cdn) Test(ctx context.Context) (contracts.Message, error) {
	n, marker := 0, ""
	for range maxPages {
		var out struct {
			Domains    []struct{} `json:"domains"`
			NextMarker string     `json:"nextMarker"`
		}
		query := url.Values{}
		if marker != "" {
			query.Set("marker", marker)
		}
		if err := c.call(ctx, http.MethodGet, "/v2/domain", query, nil, &out); err != nil {
			return contracts.Message{}, explain(err, "")
		}
		n += len(out.Domains)
		if marker = out.NextMarker; marker == "" {
			break
		}
	}
	return contracts.Message{Key: i18n.BaiducloudTestCdnOk, Params: map[string]any{"count": n}}, nil
}

// Deploy sets the certificate on each accelerated domain, with HTTPS on,
// unless the domain has it already from an earlier attempt.
func (c *cdn) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("baiducloud: no certificate to deploy"))
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
		return none, retry.Permanent(&contracts.Problem{Key: i18n.BaiducloudErrorNoDomains})
	}
	key, err := cryptox.EncodeKeyTraditional(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	name := "jec-" + strconv.FormatInt(req.CertificateID, 10) + "-" + cryptox.Fingerprint(req.Chain[0])[:12]
	var ids []string
	for _, d := range domains {
		path := "/v2/" + url.PathEscape(d) + "/certificates"
		var have struct {
			CertID   string `json:"certId"`
			CertName string `json:"certName"`
		}
		if err := c.call(ctx, http.MethodGet, path, nil, nil, &have); err != nil {
			var ae *apiError
			if !errors.As(err, &ae) || ae.status != http.StatusNotFound || ae.code == "NoSuchDomain" {
				return none, explain(err, d)
			}
			// A domain with no certificate yet.
		}
		if have.CertName == name {
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.BaiducloudLogAlready, Params: map[string]any{"domain": d}})
			ids = append(ids, have.CertID)
			continue
		}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.BaiducloudLogSet, Params: map[string]any{"domain": d}})
		var out struct {
			CertID string `json:"certId"`
		}
		err := c.call(ctx, http.MethodPut, path, nil, map[string]any{
			"httpsEnable": "ON",
			"certificate": map[string]string{"certName": name, "certServerData": string(cryptox.EncodeCertificates(req.Chain...)), "certPrivateData": string(key)},
		}, &out)
		if err != nil {
			return none, explain(err, d)
		}
		ids = append(ids, out.CertID)
	}
	log(contracts.LogLine{Level: contracts.OK, Key: i18n.BaiducloudLogDeployed})
	return contracts.DeployResult{Ref: strings.Join(ids, ",")}, nil
}
