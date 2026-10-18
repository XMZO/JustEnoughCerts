package aws

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// defaultRegion is where CloudFront takes its certificates from.
const defaultRegion = "us-east-1"

// region is an AWS region, such as us-east-1 or cn-north-1.
var region = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-\d$`)

func acmType() providers.Type {
	return providers.Type{
		ID: "aws.acm", Kind: providers.Target, Name: i18n.AwsAcmName, Description: i18n.AwsAcmDescription,
		Testable: true,
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.AwsFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "secret_access_key", Label: i18n.AwsFieldSecretAccessKey, Help: i18n.AwsFieldSecretAccessKeyHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "region", Label: i18n.AwsFieldRegion, Help: i18n.AwsFieldRegionHelp, Kind: providers.Text, Required: true, Default: defaultRegion},
			{Key: "certificate_arn", Label: i18n.AwsFieldCertificateArn, Help: i18n.AwsFieldCertificateArnHelp, Kind: providers.Text},
		},
		Credential: credentials.Use(credentials.AWS),
	}
}

// acm is AWS Certificate Manager in a region of an account.
type acm struct {
	client      *http.Client
	endpoint    string
	region      string
	credentials awssdk.Credentials
	arn         string // the certificate set, or "" for JEC's own
	now         func() time.Time
}

var (
	_ contracts.Target = (*acm)(nil)
	_ contracts.Tester = (*acm)(nil)
)

func factory(_ context.Context, in providers.Instance) (any, error) {
	r := strings.TrimSpace(cmp.Or(in.Config["region"], defaultRegion))
	if !region.MatchString(r) {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.AwsErrorRegion, Params: map[string]any{"region": r}})
	}
	client, err := httpx.Client(httpx.Options{Timeout: time.Minute, PublicOnly: true})
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	host := "acm." + r + ".amazonaws.com"
	if strings.HasPrefix(r, "cn-") {
		host += ".cn"
	}
	return &acm{client: client, endpoint: "https://" + host + "/", region: r, now: time.Now,
		credentials: awssdk.Credentials{AccessKeyID: strings.TrimSpace(in.Value("access_key_id")), SecretAccessKey: strings.TrimSpace(in.Value("secret_access_key"))},
		arn:         strings.TrimSpace(in.Config["certificate_arn"])}, nil
}

// Close lets go of idle connections.
func (a *acm) Close() error {
	a.client.CloseIdleConnections()
	return nil
}

// certificateDetail is what DescribeCertificate tells of a certificate.
type certificateDetail struct {
	CertificateArn string   `json:"CertificateArn"`
	DomainName     string   `json:"DomainName"`
	Status         string   `json:"Status"`
	Type           string   `json:"Type"`
	InUseBy        []string `json:"InUseBy"`
}

// describe gets the certificate arn, and tells whether ACM has it.
func (a *acm) describe(ctx context.Context, arn string) (certificateDetail, bool, error) {
	var out struct {
		Certificate certificateDetail `json:"Certificate"`
	}
	err := a.call(ctx, "DescribeCertificate", map[string]string{"CertificateArn": arn}, &out)
	var ae *apiError
	if errors.As(err, &ae) && ae.kind == "ResourceNotFoundException" {
		return out.Certificate, false, nil
	}
	return out.Certificate, err == nil, err
}

// Test finds the certificate set, or else makes sure the keys work, without
// changing anything.
func (a *acm) Test(ctx context.Context) (contracts.Message, error) {
	if a.arn == "" {
		if err := a.call(ctx, "ListCertificates", map[string]any{"MaxItems": 1}, nil); err != nil {
			return contracts.Message{}, a.explain(err)
		}
		return contracts.Message{Key: i18n.AwsTestKeys, Params: map[string]any{"region": a.region}}, nil
	}
	c, err := a.set(ctx)
	if err != nil {
		return contracts.Message{}, err
	}
	return contracts.Message{Key: i18n.AwsTestFound, Params: map[string]any{"domain": c.DomainName, "count": len(c.InUseBy)}}, nil
}

// set gets the certificate set, which has to be one imported to ACM: those
// ACM issues itself it renews itself.
func (a *acm) set(ctx context.Context) (certificateDetail, error) {
	c, found, err := a.describe(ctx, a.arn)
	switch {
	case err != nil:
		return c, a.explain(err)
	case !found:
		return c, retry.Permanent(&contracts.Problem{Key: i18n.AwsErrorNoCertificate, Params: map[string]any{"arn": a.arn, "region": a.region}})
	case c.Type != "IMPORTED":
		return c, retry.Permanent(&contracts.Problem{Key: i18n.AwsErrorNotImported, Params: map[string]any{"domain": c.DomainName}})
	}
	return c, nil
}

// Deploy imports the new version over the certificate set, or over the one
// JEC imported at an earlier deployment, or else as a new certificate.
// ACM keeps the ARN, so whatever uses the certificate serves the new
// version; nothing is left to remove. The ref names JEC's own.
func (a *acm) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("aws: no certificate to deploy"))
	}
	arn := a.arn
	if arn != "" {
		if _, err := a.set(ctx); err != nil {
			return none, err
		}
	} else if i := slices.IndexFunc(req.Previous, func(ref string) bool { return ref != "" }); i >= 0 {
		// The newest ref names the certificate JEC imported.
		ref := req.Previous[i]
		c, found, err := a.describe(ctx, ref)
		if err != nil {
			return none, a.explain(err)
		}
		if found && c.Type == "IMPORTED" {
			arn = ref
		} else {
			log(contracts.LogLine{Level: contracts.Warn, Key: i18n.AwsLogGone, Params: map[string]any{"arn": ref}})
		}
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	// Blobs go as Base64 in the JSON of the API.
	body := map[string]any{"Certificate": cryptox.EncodeCertificates(req.Chain[0]), "PrivateKey": key}
	if len(req.Chain) > 1 {
		body["CertificateChain"] = cryptox.EncodeCertificates(req.Chain[1:]...)
	}
	if arn != "" {
		body["CertificateArn"] = arn
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AwsLogReimport, Params: map[string]any{"arn": arn}})
	} else {
		name := "jec-" + strconv.FormatInt(req.CertificateID, 10)
		body["Tags"] = []map[string]string{{"Key": "Name", "Value": name}, {"Key": "managed-by", "Value": "jec"}}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.AwsLogImport, Params: map[string]any{"name": name, "region": a.region}})
	}
	var out struct {
		CertificateArn string `json:"CertificateArn"`
	}
	if err := a.call(ctx, "ImportCertificate", body, &out); err != nil {
		return none, a.explain(err)
	}
	if a.arn != "" {
		return none, nil
	}
	return contracts.DeployResult{Ref: cmp.Or(out.CertificateArn, arn)}, nil
}

// apiError is an answer of ACM that says it failed: the kind of error, from
// its __type, and its message.
type apiError struct {
	status        int
	kind, message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("aws: HTTP %d %s: %s", e.status, e.kind, e.message)
}

// statusError is an answer that is no answer of the API.
type statusError struct{ status int }

func (e *statusError) Error() string { return fmt.Sprintf("aws: HTTP %d", e.status) }

// call sends body to the action of ACM's JSON API, signed with Signature
// Version 4, and decodes the answer into out.
func (a *acm) call(ctx context.Context, action string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "CertificateManager."+action)
	sum := sha256.Sum256(data)
	if err := v4.NewSigner().SignHTTP(ctx, a.credentials, req, hex.EncodeToString(sum[:]), "acm", a.region, a.now()); err != nil {
		return err
	}
	resp, err := a.client.Do(req)
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
			Type     string `json:"__type"`
			Message  string `json:"message"`
			MessageU string `json:"Message"`
		}
		if json.Unmarshal(raw, &answer) == nil && answer.Type != "" {
			// The type may come with the namespace: com.amazonaws...#Kind.
			kind := answer.Type[strings.LastIndex(answer.Type, "#")+1:]
			return &apiError{status: resp.StatusCode, kind: kind, message: cmp.Or(answer.Message, answer.MessageU)}
		}
		return &statusError{status: resp.StatusCode}
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return &statusError{status: resp.StatusCode}
	}
	return nil
}

// Kinds of errors that trying again cannot fix.
var permanent = []string{"ValidationException", "InvalidParameterException", "InvalidArnException", "LimitExceededException",
	"InvalidTagException", "TooManyTagsException", "TagPolicyException", "ResourceNotFoundException"}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as refused keys or a certificate ACM rejects, ends the
// attempts.
func (a *acm) explain(err error) error {
	var ae *apiError
	var se *statusError
	switch {
	case errors.As(err, &ae) && (ae.kind == "UnrecognizedClientException" || ae.kind == "InvalidSignatureException" ||
		ae.kind == "InvalidClientTokenId" || ae.kind == "SignatureDoesNotMatch"):
		return retry.Permanent(&contracts.Problem{Key: i18n.AwsErrorKeys, Params: map[string]any{"message": ae.message}, Err: err})
	case errors.As(err, &ae) && (ae.kind == "AccessDeniedException" || ae.status == http.StatusForbidden):
		return retry.Permanent(&contracts.Problem{Key: i18n.AwsErrorDenied, Params: map[string]any{"message": ae.message}, Err: err})
	case errors.As(err, &ae):
		problem := &contracts.Problem{Key: i18n.AwsErrorApi, Params: map[string]any{"kind": ae.kind, "message": ae.message}, Err: err}
		if slices.Contains(permanent, ae.kind) {
			return retry.Permanent(problem)
		}
		return problem
	case errors.As(err, &se):
		problem := &contracts.Problem{Key: i18n.AwsErrorStatus, Params: map[string]any{"status": se.status}, Err: err}
		if se.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	}
	return &contracts.Problem{Key: i18n.AwsErrorNetwork, Params: map[string]any{"region": a.region}, Err: httpx.Cause(err)}
}
