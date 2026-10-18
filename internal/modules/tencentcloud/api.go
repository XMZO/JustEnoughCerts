package tencentcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	tchttp "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/http"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// Endpoints of the SSL Certificates service: the China site's and the
// international one's.
const (
	endpointChina = "ssl.tencentcloudapi.com"
	endpointIntl  = "ssl.intl.tencentcloudapi.com"
	apiVersion    = "2019-12-05"
)

// sslAPI is what the target needs of the API of SSL Certificates. Tests
// replace it with a fake.
type sslAPI interface {
	// upload adds a certificate, in PEM with its chain, and its key, and
	// returns its ID; that of the same certificate when it is there
	// already.
	upload(ctx context.Context, alias, chain, key string) (string, error)
	// update has the resources of types that certificate old is deployed
	// to take certificate id, in regions where types take regions. It
	// returns the ID of the task, 0 while the task is still being made.
	update(ctx context.Context, old, id string, types, regions []string) (int64, error)
	// remove deletes a certificate.
	remove(ctx context.Context, id string) error
	// count returns how many certificates the account has.
	count(ctx context.Context) (int, error)
}

// sdk is sslAPI over the common client of Tencent Cloud's SDK, which signs
// the requests (TC3-HMAC-SHA256), without the SDK of the service.
type sdk struct {
	client *common.Client
	scheme string
}

func newSDK(id, key, endpoint string) (sslAPI, error) { return dial(id, key, endpoint, "HTTPS") }

// dial makes the client of endpoint, over scheme.
func dial(id, key, endpoint, scheme string) (*sdk, error) {
	p := profile.NewClientProfile()
	p.HttpProfile.Endpoint = endpoint
	p.HttpProfile.Scheme = scheme
	p.HttpProfile.ReqTimeout = 30
	return &sdk{client: common.NewCommonClient(common.NewCredential(id, key), "", p), scheme: scheme}, nil
}

// call sends action with params and decodes the response into out.
func (s *sdk) call(ctx context.Context, action string, params map[string]any, out any) error {
	req := tchttp.NewCommonRequest("ssl", apiVersion, action)
	req.SetContext(ctx)
	if err := req.SetActionParameters(params); err != nil {
		return err
	}
	resp := tchttp.NewCommonResponse()
	if err := s.client.Send(req, resp); err != nil {
		return err
	}
	var body struct {
		Response json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(resp.GetBody(), &body); err != nil {
		return fmt.Errorf("tencentcloud: %s: %w", action, err)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body.Response, out)
}

func (s *sdk) upload(ctx context.Context, alias, chain, key string) (string, error) {
	var out struct {
		CertificateID string `json:"CertificateId"`
		RepeatCertID  string `json:"RepeatCertId"`
	}
	err := s.call(ctx, "UploadCertificate", map[string]any{
		"CertificatePublicKey": chain, "CertificatePrivateKey": key, "CertificateType": "SVR",
		"Alias": alias, "Repeatable": false,
	}, &out)
	if err != nil {
		return "", err
	}
	if out.RepeatCertID != "" {
		return out.RepeatCertID, nil
	}
	if out.CertificateID == "" {
		return "", errors.New("tencentcloud: UploadCertificate returned no certificate ID")
	}
	return out.CertificateID, nil
}

func (s *sdk) update(ctx context.Context, old, id string, types, regions []string) (int64, error) {
	params := map[string]any{"OldCertificateId": old, "CertificateId": id, "ResourceTypes": types}
	if len(regions) > 0 {
		var rr []map[string]any
		for _, t := range types {
			if regional[t] {
				rr = append(rr, map[string]any{"ResourceType": t, "Regions": regions})
			}
		}
		if len(rr) > 0 {
			params["ResourceTypesRegions"] = rr
		}
	}
	var out struct {
		DeployRecordID int64 `json:"DeployRecordId"`
	}
	if err := s.call(ctx, "UpdateCertificateInstance", params, &out); err != nil {
		return 0, err
	}
	return out.DeployRecordID, nil
}

func (s *sdk) remove(ctx context.Context, id string) error {
	return s.call(ctx, "DeleteCertificate", map[string]any{"CertificateId": id}, nil)
}

func (s *sdk) count(ctx context.Context) (int, error) {
	var out struct {
		TotalCount int `json:"TotalCount"`
	}
	err := s.call(ctx, "DescribeCertificates", map[string]any{"Limit": 1}, &out)
	return out.TotalCount, err
}

// errorCode returns the code of an error of the API, such as
// AuthFailure.SecretIdNotFound, or "" for another error.
func errorCode(err error) string {
	var e *tcerr.TencentCloudSDKError
	if errors.As(err, &e) {
		return e.GetCode()
	}
	return ""
}

// explain turns an error of the API into a Problem: one that trying again
// cannot fix, such as a refused key, ends the attempts.
func explain(err error, endpoint string) error {
	code := errorCode(err)
	switch {
	case code == "":
		return &contracts.Problem{Key: i18n.TencentcloudErrorNetwork, Params: map[string]any{"endpoint": endpoint}, Err: err}
	case strings.HasPrefix(code, "AuthFailure"), strings.HasPrefix(code, "UnauthorizedOperation"), strings.Contains(code, "NoAuth"):
		return retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorAuth, Err: err})
	case strings.HasPrefix(code, "LimitExceeded"), strings.HasPrefix(code, "RequestLimitExceeded"):
		return &contracts.Problem{Key: i18n.TencentcloudErrorThrottled, Err: err}
	case strings.HasPrefix(code, "InternalError"):
		return &contracts.Problem{Key: i18n.TencentcloudErrorApi, Params: map[string]any{"code": code}, Err: err}
	}
	return retry.Permanent(&contracts.Problem{Key: i18n.TencentcloudErrorApi, Params: map[string]any{"code": code}, Err: err})
}

// regional are the resource types that take regions.
var regional = map[string]bool{"clb": true, "tke": true, "apigateway": true, "waf": true, "tcb": true, "tse": true, "cos": true}

// resourceTypes are the types of resource the service updates.
var resourceTypes = []string{"cdn", "teo", "clb", "waf", "live", "ddos", "apigateway", "vod", "tke", "tcb", "tse", "cos"}
