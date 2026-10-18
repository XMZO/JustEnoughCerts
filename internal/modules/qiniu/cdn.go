package qiniu

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // G505: Qiniu signs requests with HMAC-SHA1
	"encoding/base64"
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
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// defaultBase is the API of Qiniu's CDN and certificates.
const defaultBase = "https://api.qiniu.com"

// Bounds of the listing of certificates, which JEC looks through for one
// it uploaded already, when an earlier attempt failed after uploading.
const (
	pageSize = 100
	maxPages = 10
)

func cdnType() providers.Type {
	return providers.Type{
		ID: "qiniu.cdn", Kind: providers.Target, Name: i18n.QiniuCdnName, Description: i18n.QiniuCdnDescription,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "access_key", Label: i18n.QiniuFieldAccessKey, Kind: providers.Text, Required: true},
			{Key: "secret_key", Label: i18n.QiniuFieldSecretKey, Help: i18n.QiniuFieldSecretKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "domains", Label: i18n.QiniuFieldDomains, Help: i18n.QiniuFieldDomainsHelp, Kind: providers.Text},
			{Key: "delete_old", Label: i18n.QiniuFieldDeleteOld, Help: i18n.QiniuFieldDeleteOldHelp, Kind: providers.Switch, Default: "true"},
		},
	}
}

// cdn is the CDN of a Qiniu account.
type cdn struct {
	client    *http.Client
	base      string
	accessKey string
	secretKey []byte
	domains   []string
	deleteOld bool
}

var (
	_ contracts.Target = (*cdn)(nil)
	_ contracts.Tester = (*cdn)(nil)
)

func factory(_ context.Context, in providers.Instance) (any, error) {
	client, err := httpx.Client(httpx.Options{Timeout: time.Minute, PublicOnly: true})
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &cdn{client: client, base: defaultBase, accessKey: strings.TrimSpace(in.Value("access_key")),
		secretKey: []byte(strings.TrimSpace(in.Value("secret_key"))), domains: split(in.Config["domains"]),
		deleteOld: in.Config["delete_old"] != "false"}, nil
}

// split reads a list of domains, separated by commas or spaces.
func split(s string) []string {
	var out []string
	for _, d := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		out = append(out, strings.ToLower(strings.TrimSuffix(d, ".")))
	}
	return out
}

// Close lets go of idle connections.
func (c *cdn) Close() error {
	c.client.CloseIdleConnections()
	return nil
}

// domainHTTPS is how a domain serves HTTPS, as Qiniu reports and takes it.
type domainHTTPS struct {
	CertID      string `json:"certId"`
	ForceHTTPS  bool   `json:"forceHttps"`
	HTTP2Enable bool   `json:"http2Enable"`
}

// domain is an accelerated domain, as GET /domain/<name> reports it.
type domain struct {
	Name     string      `json:"name"`
	Protocol string      `json:"protocol"`
	HTTPS    domainHTTPS `json:"https"`
}

// Test makes sure the account has the accelerated domains set, or else
// that the keys are good, without changing anything.
func (c *cdn) Test(ctx context.Context) (contracts.Message, error) {
	if len(c.domains) == 0 {
		var page struct {
			Certs []json.RawMessage `json:"certs"`
		}
		if err := c.call(ctx, http.MethodGet, "/sslcert?limit=1", nil, &page); err != nil {
			return contracts.Message{}, c.explain(err, "")
		}
		return contracts.Message{Key: i18n.QiniuTestKeys}, nil
	}
	for _, d := range c.domains {
		if _, err := c.domain(ctx, d); err != nil {
			return contracts.Message{}, err
		}
	}
	return contracts.Message{Key: i18n.QiniuTestOk, Params: map[string]any{"domains": strings.Join(c.domains, ", ")}}, nil
}

// domain gets the accelerated domain name.
func (c *cdn) domain(ctx context.Context, name string) (domain, error) {
	var d domain
	if err := c.call(ctx, http.MethodGet, "/domain/"+url.PathEscape(name), nil, &d); err != nil {
		return d, c.explain(err, name)
	}
	return d, nil
}

// Deploy uploads the certificate, unless an earlier attempt did, and sets
// it on each accelerated domain: those set, or else the certificate's own,
// but for wildcards. A domain without HTTPS yet gets it, with HTTP/2. Then
// it removes the certificates earlier deployments uploaded, if set to;
// Qiniu keeps those that a domain still uses.
func (c *cdn) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("qiniu: no certificate to deploy"))
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
		return none, retry.Permanent(&contracts.Problem{Key: i18n.QiniuErrorNoDomains})
	}
	id, err := c.upload(ctx, req, log)
	if err != nil {
		return none, err
	}
	for _, name := range domains {
		d, err := c.domain(ctx, name)
		if err != nil {
			return none, err
		}
		if d.HTTPS.CertID == id {
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.QiniuLogCurrent, Params: map[string]any{"domain": name}})
			continue
		}
		path, conf := "/domain/"+url.PathEscape(name)+"/httpsconf", d.HTTPS
		if d.Protocol != "https" {
			path, conf = "/domain/"+url.PathEscape(name)+"/sslize", domainHTTPS{HTTP2Enable: true}
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.QiniuLogSslize, Params: map[string]any{"domain": name}})
		} else {
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.QiniuLogSwitch, Params: map[string]any{"domain": name}})
		}
		conf.CertID = id
		if err := c.call(ctx, http.MethodPut, path, conf, nil); err != nil {
			return none, c.explain(err, name)
		}
	}
	result := contracts.DeployResult{Ref: id}
	if c.deleteOld {
		result.Removed = c.removeOld(ctx, req.Previous, id, log)
	}
	return result, nil
}

// storedCert is a certificate in Qiniu's store, as it lists them.
type storedCert struct {
	CertID string `json:"certid"`
	Name   string `json:"name"`
}

// upload stores the certificate under a name made of its ID in JEC and its
// fingerprint, or finds it stored so by an earlier attempt, and returns
// its ID at Qiniu.
func (c *cdn) upload(ctx context.Context, req contracts.DeployRequest, log func(contracts.LogLine)) (string, error) {
	name := "jec-" + strconv.FormatInt(req.CertificateID, 10) + "-" + cryptox.Fingerprint(req.Chain[0])[:12]
	marker := ""
	for range maxPages {
		var page struct {
			Marker string       `json:"marker"`
			Certs  []storedCert `json:"certs"`
		}
		path := "/sslcert?" + url.Values{"marker": {marker}, "limit": {strconv.Itoa(pageSize)}}.Encode()
		if err := c.call(ctx, http.MethodGet, path, nil, &page); err != nil {
			return "", c.explain(err, "")
		}
		if i := slices.IndexFunc(page.Certs, func(s storedCert) bool { return s.Name == name }); i >= 0 {
			return page.Certs[i].CertID, nil
		}
		if marker = page.Marker; marker == "" || len(page.Certs) == 0 {
			break
		}
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return "", retry.Permanent(err)
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.QiniuLogUpload, Params: map[string]any{"name": name}})
	var answer struct {
		CertID string `json:"certID"`
	}
	body := map[string]string{"name": name, "common_name": cryptox.Names(req.Chain[0])[0], "ca": string(cryptox.EncodeCertificates(req.Chain...)), "pri": string(key)}
	if err := c.call(ctx, http.MethodPost, "/sslcert", body, &answer); err != nil {
		return "", c.explain(err, "")
	}
	if answer.CertID == "" {
		return "", c.explain(&statusError{status: http.StatusOK}, "")
	}
	return answer.CertID, nil
}

// removeOld deletes the certificates of earlier deployments, but id, and
// returns those it deleted. Qiniu refuses to delete one that a domain
// uses, as one JEC does not manage may; that is only logged.
func (c *cdn) removeOld(ctx context.Context, previous []string, id string, log func(contracts.LogLine)) []string {
	var removed []string
	for _, ref := range previous {
		if ref == "" || ref == id || slices.Contains(removed, ref) {
			continue
		}
		if err := c.call(ctx, http.MethodDelete, "/sslcert/"+url.PathEscape(ref), nil, nil); err != nil {
			var ae *apiError
			if errors.As(err, &ae) && ae.status == http.StatusNotFound {
				removed = append(removed, ref)
				continue
			}
			log(contracts.LogLine{Level: contracts.Warn, Key: i18n.QiniuLogRemoveFailed, Params: map[string]any{"id": ref}, Detail: err.Error()})
			continue
		}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.QiniuLogRemoved, Params: map[string]any{"id": ref}})
		removed = append(removed, ref)
	}
	return removed
}

// apiError is an answer of the API that says it failed.
type apiError struct {
	status  int
	code    int
	message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("qiniu: HTTP %d: %d %s", e.status, e.code, e.message)
}

// statusError is an answer that is no answer of the API.
type statusError struct{ status int }

func (e *statusError) Error() string { return fmt.Sprintf("qiniu: HTTP %d", e.status) }

// call sends body as JSON, when not nil, to path of the API, signed with
// the keys as Qiniu's QBox scheme has it: the HMAC-SHA1 of the path and
// query and a newline, as JSON bodies are not signed. It decodes the
// answer into out.
func (c *cdn) call(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	mac := hmac.New(sha1.New, c.secretKey)
	mac.Write([]byte(path + "\n"))
	req.Header.Set("Authorization", "QBox "+c.accessKey+":"+base64.URLEncoding.EncodeToString(mac.Sum(nil)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var answer struct {
			Code  int    `json:"code"`
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &answer) == nil && answer.Error != "" {
			return &apiError{status: resp.StatusCode, code: answer.Code, message: answer.Error}
		}
		return &statusError{status: resp.StatusCode}
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, out) != nil {
		return &statusError{status: resp.StatusCode}
	}
	return nil
}

// explain turns an error of a call about domain, if any, into a Problem:
// one that trying again cannot fix, such as refused keys or a domain the
// account does not have, ends the attempts.
func (c *cdn) explain(err error, domain string) error {
	var ae *apiError
	var se *statusError
	switch {
	case errors.As(err, &ae) && (ae.status == http.StatusUnauthorized || ae.status == http.StatusForbidden):
		return retry.Permanent(&contracts.Problem{Key: i18n.QiniuErrorKeys, Params: map[string]any{"message": ae.message}, Err: err})
	case errors.As(err, &ae) && ae.status == http.StatusNotFound && domain != "":
		return retry.Permanent(&contracts.Problem{Key: i18n.QiniuErrorDomain, Params: map[string]any{"domain": domain, "message": ae.message}, Err: err})
	case errors.As(err, &ae):
		problem := &contracts.Problem{Key: i18n.QiniuErrorApi, Params: map[string]any{"message": ae.message, "status": ae.status}, Err: err}
		if ae.status >= 500 || ae.status == http.StatusTooManyRequests || ae.status == http.StatusRequestTimeout {
			return problem
		}
		return retry.Permanent(problem)
	case errors.As(err, &se):
		problem := &contracts.Problem{Key: i18n.QiniuErrorStatus, Params: map[string]any{"status": se.status}, Err: err}
		if se.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	}
	return &contracts.Problem{Key: i18n.QiniuErrorNetwork, Err: httpx.Cause(err)}
}
