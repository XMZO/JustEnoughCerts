package aws

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// cloudFrontAPI is CloudFront's REST API, which has no regions.
const cloudFrontAPI = "https://cloudfront.amazonaws.com/2020-05-31/distribution/"

// distributionID is the form of a CloudFront distribution's ID.
var distributionID = regexp.MustCompile(`^[A-Z0-9]{8,20}$`)

func cloudFrontType() providers.Type {
	return providers.Type{
		ID: "aws.cloudfront", Kind: providers.Target, Name: i18n.AwsCloudfrontName, Description: i18n.AwsCloudfrontDescription,
		Testable: true,
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.AwsFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "secret_access_key", Label: i18n.AwsFieldSecretAccessKey, Help: i18n.AwsFieldCloudfrontKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "distribution", Label: i18n.AwsFieldDistribution, Help: i18n.AwsFieldDistributionHelp, Kind: providers.Text, Required: true},
			httpx.ProxyField(),
		},
		Credential: credentials.Use(credentials.AWS),
	}
}

// cloudFront serves certificates on a CloudFront distribution: imported to
// ACM in us-east-1, where CloudFront takes them from, and set as the
// distribution's certificate the first time; later versions go over the
// same ARN, which the distribution keeps.
type cloudFront struct {
	acm          *acm
	endpoint     string
	distribution string
}

var (
	_ contracts.Target = (*cloudFront)(nil)
	_ contracts.Tester = (*cloudFront)(nil)
)

func cloudFrontFactory(ctx context.Context, in providers.Instance) (any, error) {
	id := strings.TrimSpace(in.Config["distribution"])
	if !distributionID.MatchString(id) {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.AwsErrorDistribution, Params: map[string]any{"id": id}})
	}
	config := map[string]string{}
	for k, v := range in.Config {
		config[k] = v
	}
	config["region"], config["certificate_arn"] = defaultRegion, ""
	p, err := factory(ctx, providers.Instance{ID: in.ID, Type: in.Type, Name: in.Name, Config: config, Secrets: in.Secrets})
	if err != nil {
		return nil, err
	}
	return &cloudFront{acm: p.(*acm), endpoint: cloudFrontAPI, distribution: id}, nil
}

// Close lets go of idle connections.
func (c *cloudFront) Close() error { return c.acm.Close() }

// Test reads the distribution: its domains and the certificate it serves.
func (c *cloudFront) Test(ctx context.Context) (contracts.Message, error) {
	d, err := c.config(ctx)
	if err != nil {
		return contracts.Message{}, err
	}
	return contracts.Message{Key: i18n.AwsTestDistribution, Params: map[string]any{
		"id": c.distribution, "aliases": cmp.Or(strings.Join(d.aliases(), ", "), "-"), "certificate": cmp.Or(d.arn(), "CloudFront"),
	}}, nil
}

// Deploy imports the version to ACM, over the certificate JEC imported
// before if any, then makes it the distribution's certificate unless it
// is already.
func (c *cloudFront) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	imported, err := c.acm.Deploy(ctx, req)
	if err != nil {
		return contracts.DeployResult{}, err
	}
	arn := imported.Ref
	d, err := c.config(ctx)
	if err != nil {
		return imported, err
	}
	if d.arn() == arn {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AwsLogAttachedAlready, Params: map[string]any{"id": c.distribution}})
		return imported, nil
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.AwsLogAttach, Params: map[string]any{"id": c.distribution}})
	if err := c.update(ctx, d, d.withCertificate(arn)); err != nil {
		return imported, err
	}
	return imported, nil
}

// distributionConfig is a distribution's configuration as CloudFront
// answers it, with the ETag a change has to name.
type distributionConfig struct {
	raw  []byte
	etag string
}

var (
	viewerCertificate = regexp.MustCompile(`(?s)<ViewerCertificate>.*?</ViewerCertificate>`)
	acmARN            = regexp.MustCompile(`<ACMCertificateArn>([^<]*)</ACMCertificateArn>`)
	minimumProtocol   = regexp.MustCompile(`<MinimumProtocolVersion>([^<]*)</MinimumProtocolVersion>`)
	alias             = regexp.MustCompile(`(?s)<Aliases>.*?</Aliases>`)
	cname             = regexp.MustCompile(`<CNAME>([^<]*)</CNAME>`)
)

// arn is the ACM certificate the distribution serves, or "" for none, as
// CloudFront's own.
func (d distributionConfig) arn() string {
	block := viewerCertificate.Find(d.raw)
	if m := acmARN.FindSubmatch(block); m != nil {
		return html.UnescapeString(string(m[1]))
	}
	return ""
}

func (d distributionConfig) aliases() []string {
	var out []string
	for _, m := range cname.FindAllSubmatch(alias.Find(d.raw), -1) {
		out = append(out, html.UnescapeString(string(m[1])))
	}
	return out
}

// withCertificate is the configuration with arn as its certificate, served
// to clients that send SNI, at no less than TLS 1.2 unless set higher.
func (d distributionConfig) withCertificate(arn string) []byte {
	protocol := "TLSv1.2_2021"
	if m := minimumProtocol.FindSubmatch(viewerCertificate.Find(d.raw)); m != nil && strings.HasPrefix(string(m[1]), "TLSv1.2_") && string(m[1]) > protocol {
		protocol = string(m[1])
	}
	var b strings.Builder
	b.WriteString("<ViewerCertificate><CloudFrontDefaultCertificate>false</CloudFrontDefaultCertificate><ACMCertificateArn>")
	_ = xml.EscapeText(&b, []byte(arn))
	b.WriteString("</ACMCertificateArn><SSLSupportMethod>sni-only</SSLSupportMethod><MinimumProtocolVersion>")
	b.WriteString(protocol)
	b.WriteString("</MinimumProtocolVersion></ViewerCertificate>")
	if viewerCertificate.Match(d.raw) {
		return viewerCertificate.ReplaceAllLiteral(d.raw, []byte(b.String()))
	}
	return bytes.Replace(d.raw, []byte("</DistributionConfig>"), []byte(b.String()+"</DistributionConfig>"), 1)
}

func (c *cloudFront) config(ctx context.Context) (distributionConfig, error) {
	raw, etag, err := c.call(ctx, http.MethodGet, nil, "")
	if err != nil {
		return distributionConfig{}, c.explain(err)
	}
	return distributionConfig{raw: raw, etag: etag}, nil
}

func (c *cloudFront) update(ctx context.Context, d distributionConfig, body []byte) error {
	if _, _, err := c.call(ctx, http.MethodPut, body, d.etag); err != nil {
		return c.explain(err)
	}
	return nil
}

// call gets or puts the distribution's configuration, signed with
// Signature Version 4, and returns the answer with its ETag.
func (c *cloudFront) call(ctx context.Context, method string, body []byte, ifMatch string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+c.distribution+"/config", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "text/xml")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	sum := sha256.Sum256(body)
	if err := v4.NewSigner().SignHTTP(ctx, c.acm.credentials, req, hex.EncodeToString(sum[:]), "cloudfront", defaultRegion, c.acm.now()); err != nil {
		return nil, "", err
	}
	resp, err := c.acm.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode/100 != 2 {
		var answer struct {
			Error struct {
				Code    string `xml:"Code"`
				Message string `xml:"Message"`
			} `xml:"Error"`
		}
		if xml.Unmarshal(raw, &answer) == nil && answer.Error.Code != "" {
			return nil, "", &apiError{status: resp.StatusCode, kind: answer.Error.Code, message: answer.Error.Message}
		}
		return nil, "", &statusError{status: resp.StatusCode}
	}
	return raw, resp.Header.Get("ETag"), nil
}

// explain says what went wrong with the distribution: one that is not
// there, a certificate it refuses, else as for ACM.
func (c *cloudFront) explain(err error) error {
	var ae *apiError
	switch {
	case errors.As(err, &ae) && ae.kind == "NoSuchDistribution":
		return retry.Permanent(&contracts.Problem{Key: i18n.AwsErrorNoDistribution, Params: map[string]any{"id": c.distribution}, Err: err})
	case errors.As(err, &ae) && (ae.kind == "InvalidViewerCertificate" || ae.kind == "InvalidArgument" || ae.kind == "CNAMEAlreadyExists"):
		return retry.Permanent(&contracts.Problem{Key: i18n.AwsErrorApi, Params: map[string]any{"kind": ae.kind, "message": ae.message}, Err: err})
	case errors.As(err, &ae) && ae.kind == "PreconditionFailed":
		// Changed meanwhile: the next attempt reads it anew.
		return &contracts.Problem{Key: i18n.AwsErrorApi, Params: map[string]any{"kind": ae.kind, "message": ae.message}, Err: err}
	}
	return c.acm.explain(err)
}
