package aliyun

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // G501: OSS asks for Content-MD5, a checksum, not a signature
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/aliyunapi"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// OSS: each new certificate goes to the Certificate Management Service,
// as for aliyun.cas, and then to a custom domain of a bucket through
// PutCname, with its CAS identifier and its PEM, as certimate does; the
// certificates JEC uploaded before are deleted from CAS once the domain
// has the new one. The OSS API is XML over HTTPS, signed with OSS's own
// version 4.

func ossType() providers.Type {
	return providers.Type{
		ID: "aliyun.oss", Kind: providers.Target, Name: i18n.AliyunOssName, Description: i18n.AliyunOssDescription, Untested: true,
		Tags: []string{"china"}, Testable: true,
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.AliyunFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "access_key_secret", Label: i18n.AliyunFieldAccessKeySecret, Help: i18n.AliyunFieldOssSecretHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "region", Label: i18n.AliyunFieldRegion, Help: i18n.AliyunFieldOssRegionHelp, Kind: providers.Text, Required: true, Default: "cn-hangzhou"},
			{Key: "bucket", Label: i18n.AliyunFieldBucket, Kind: providers.Text, Required: true},
			{Key: "domain", Label: i18n.AliyunFieldOssDomain, Help: i18n.AliyunFieldOssDomainHelp, Kind: providers.Text, Required: true},
			{Key: "site", Label: i18n.AliyunFieldSite, Help: i18n.AliyunFieldCasSiteHelp, Kind: providers.Select, Required: true, Default: casChina, Options: []providers.Option{
				{Value: casChina, Label: i18n.AliyunSiteChina},
				{Value: casIntl, Label: i18n.AliyunSiteIntl},
			}},
			deleteOldField,
		},
		Credential: credentials.Use(credentials.Aliyun),
	}
}

// The forms of the names that go into the host name of a bucket.
var (
	bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	regionID   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)+$`)
)

// bucket is a bucket of OSS with a custom domain, and the Certificate
// Management Service its certificates come from.
type bucket struct {
	cas         rpc
	casEndpoint string
	casRegion   string
	http        *http.Client
	// base is the bucket's endpoint, such as
	// https://b.oss-cn-hangzhou.aliyuncs.com.
	base         string
	name, region string
	id, secret   string
	domain       string
	deleteOld    bool
	now          func() time.Time
}

var (
	_ contracts.Target = (*bucket)(nil)
	_ contracts.Tester = (*bucket)(nil)
)

func (m *Module) ossFactory(_ context.Context, in providers.Instance) (any, error) {
	name, region := strings.TrimSpace(in.Config["bucket"]), strings.TrimSpace(in.Config["region"])
	if !bucketName.MatchString(name) || !regionID.MatchString(region) {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorOssName, Params: map[string]any{"bucket": name, "region": region}})
	}
	site := in.Config["site"]
	endpoint := casEndpoint(site)
	id, secret := strings.TrimSpace(in.Value("access_key_id")), strings.TrimSpace(in.Value("access_key_secret"))
	cas, err := m.rpc(endpoint, casVersion, id, secret)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client, err := httpx.Client(httpx.Options{})
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	casRegion := casChina
	if site == casIntl {
		casRegion = casIntl
	}
	return &bucket{
		cas: cas, casEndpoint: endpoint, casRegion: casRegion, http: client,
		base: "https://" + name + ".oss-" + region + ".aliyuncs.com", name: name, region: region, id: id, secret: secret,
		domain: strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Config["domain"]), ".")), deleteOld: in.Config["delete_old"] != "false",
		now: time.Now,
	}, nil
}

// Close lets the connections go.
func (b *bucket) Close() error {
	b.http.CloseIdleConnections()
	return nil
}

// ossError is an error OSS answered.
type ossError struct {
	Status  int    `xml:"-"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func (e *ossError) Error() string {
	return fmt.Sprintf("oss: HTTP %d: %s: %s", e.Status, e.Code, e.Message)
}

// call sends a request about the bucket, with its sub-resources in query
// and body as XML unless nil, and decodes the XML answer into out unless
// nil.
func (b *bucket) call(ctx context.Context, method string, query map[string]string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = xml.Marshal(body); err != nil {
			return retry.Permanent(err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, b.base+"/?"+ossQuery(query), bytes.NewReader(payload))
	if err != nil {
		return retry.Permanent(err)
	}
	if body != nil {
		sum := md5.Sum(payload) //nolint:gosec // G401: as above
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(sum[:]))
	}
	signOSS(req, "/"+b.name+"/", b.region, b.id, b.secret, b.now())
	resp, err := b.http.Do(req)
	if err != nil {
		return &contracts.Problem{Key: i18n.AliyunErrorNetwork, Params: map[string]any{"endpoint": req.URL.Host}, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return &contracts.Problem{Key: i18n.AliyunErrorNetwork, Params: map[string]any{"endpoint": req.URL.Host}, Err: err}
	}
	if resp.StatusCode >= 300 {
		e := &ossError{Status: resp.StatusCode}
		_ = xml.Unmarshal(data, e)
		return b.explain(e)
	}
	if out != nil && len(data) > 0 {
		if err := xml.Unmarshal(data, out); err != nil {
			return fmt.Errorf("oss: %s: %w", method, err)
		}
	}
	return nil
}

// explain turns an error OSS answered into a Problem.
func (b *bucket) explain(e *ossError) error {
	switch {
	case e.Code == "InvalidAccessKeyId", e.Code == "SignatureDoesNotMatch", e.Code == "AccessDenied", e.Status == http.StatusForbidden:
		return retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorAuth, Err: e})
	case e.Code == "NoSuchBucket":
		return retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorBucket, Params: map[string]any{"bucket": b.name, "region": b.region}, Err: e})
	case e.Code == "NoSuchCname", e.Code == "CnameNotExist":
		return retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorCname, Params: map[string]any{"bucket": b.name, "domain": b.domain}, Err: e})
	case e.Status == http.StatusTooManyRequests, strings.Contains(strings.ToLower(e.Code), "throttl"):
		return &contracts.Problem{Key: i18n.AliyunErrorThrottled, Err: e}
	}
	p := &contracts.Problem{Key: i18n.AliyunErrorApi, Params: map[string]any{"code": e.Code}, Err: e}
	if e.Status >= 500 {
		return p
	}
	return retry.Permanent(p)
}

// cnames is the custom domains of a bucket, as ListCname answers.
type cnames struct {
	Cname []struct {
		Domain      string `xml:"Domain"`
		Certificate struct {
			CertID string `xml:"CertId"`
		} `xml:"Certificate"`
	} `xml:"Cname"`
}

// current returns the CAS identifier of the certificate the domain has,
// failing when the bucket does not have the domain.
func (b *bucket) current(ctx context.Context) (string, error) {
	var list cnames
	if err := b.call(ctx, http.MethodGet, map[string]string{"cname": ""}, nil, &list); err != nil {
		return "", err
	}
	for _, c := range list.Cname {
		if strings.EqualFold(c.Domain, b.domain) {
			return c.Certificate.CertID, nil
		}
	}
	return "", retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorCname, Params: map[string]any{"bucket": b.name, "domain": b.domain}})
}

// Test finds the custom domain on the bucket, changing nothing.
func (b *bucket) Test(ctx context.Context) (contracts.Message, error) {
	if _, err := b.current(ctx); err != nil {
		return contracts.Message{}, err
	}
	return contracts.Message{Key: i18n.AliyunTestOssOk, Params: map[string]any{"bucket": b.name, "domain": b.domain}}, nil
}

// putCname is what PutCname takes.
type putCname struct {
	XMLName xml.Name `xml:"BucketCnameConfiguration"`
	Cname   struct {
		Domain                   string `xml:"Domain"`
		CertificateConfiguration struct {
			CertID      string `xml:"CertId"`
			Certificate string `xml:"Certificate"`
			PrivateKey  string `xml:"PrivateKey"`
			Force       bool   `xml:"Force"`
		} `xml:"CertificateConfiguration"`
	} `xml:"Cname"`
}

// Deploy uploads the certificate to CAS, unless an earlier attempt did,
// sets it on the custom domain, unless the domain has it, and deletes
// those JEC uploaded before, if set to.
func (b *bucket) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := logger(req)
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("aliyun: no certificate to deploy"))
	}
	has, err := b.current(ctx)
	if err != nil {
		return none, err
	}
	id, err := casUpload(ctx, b.cas, b.casEndpoint, req, log)
	if err != nil {
		return none, err
	}
	identifier, err := casIdentifier(ctx, b.cas, b.casEndpoint, b.casRegion, id)
	if err != nil {
		return none, err
	}
	if has == identifier {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogOssCurrent, Params: map[string]any{"domain": b.domain}})
	} else {
		key, err := cryptox.EncodeKeyTraditional(req.Key)
		if err != nil {
			return none, retry.Permanent(err)
		}
		var body putCname
		body.Cname.Domain = b.domain
		c := &body.Cname.CertificateConfiguration
		c.CertID, c.Certificate, c.PrivateKey, c.Force = identifier, string(cryptox.EncodeCertificates(req.Chain...)), string(key), true
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AliyunLogOssSet, Params: map[string]any{"domain": b.domain, "bucket": b.name}})
		if err := b.call(ctx, http.MethodPost, map[string]string{"cname": "", "comp": "add"}, body, nil); err != nil {
			return none, err
		}
	}
	res := contracts.DeployResult{Ref: id}
	if b.deleteOld {
		res.Removed = removeOld(req.Previous, id, log, func(old string) (bool, error) {
			err := b.cas.do(ctx, "DeleteUserCertificate", map[string]string{"CertId": old}, nil, nil)
			code, _ := aliyunapi.ErrorCode(err)
			return missing(strings.ToLower(code)), err
		})
	}
	return res, nil
}

// casIdentifier is how the other products, such as OSS and ALB, name
// certificate id of the Certificate Management Service at endpoint, of
// region: with the region, as GetUserCertificateDetail tells.
func casIdentifier(ctx context.Context, cas rpc, endpoint, region, id string) (string, error) {
	var out struct {
		CertIdentifier string `json:"CertIdentifier"`
	}
	err := cas.do(ctx, "GetUserCertificateDetail", map[string]string{"CertId": id, "CertFilter": "true"}, nil, &out)
	if err != nil {
		return "", explainRPC(err, endpoint, noneAbsent)
	}
	if out.CertIdentifier == "" {
		return id + "-" + region, nil
	}
	return out.CertIdentifier, nil
}
