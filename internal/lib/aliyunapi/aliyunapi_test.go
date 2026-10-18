package aliyunapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/alibabacloud-go/tea/tea"
)

func TestErrorCode(t *testing.T) {
	for _, c := range []struct {
		err    error
		code   string
		status int
	}{
		{tea.NewSDKError(map[string]any{"code": "Throttling.User", "statusCode": 400, "message": "slow down"}), "Throttling.User", 400},
		{fmt.Errorf("upload: %w", tea.NewSDKError(map[string]any{"code": "Forbidden", "statusCode": 403})), "Forbidden", 403},
		{&dara.SDKError{Code: dara.String("InvalidDomain.NotFound"), StatusCode: dara.Int(404)}, "InvalidDomain.NotFound", 404},
		{errors.New("dial tcp: i/o timeout"), "", 0},
	} {
		if code, status := ErrorCode(c.err); code != c.code || status != c.status {
			t.Errorf("%v: %q %d", c.err, code, status)
		}
	}
}

// What the OpenAPI client returns for an error of the API is what
// ErrorCode reads.
func TestOpenAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"RequestId":"r","Code":"InvalidDomain.NotFound","Message":"The domain provided does not belong to you."}`)
	}))
	defer srv.Close()
	client, err := openapi.NewClient(new(openapiutil.Config).SetAccessKeyId("id").SetAccessKeySecret("secret").
		SetEndpoint(strings.TrimPrefix(srv.URL, "http://")).SetProtocol("HTTP"))
	if err != nil {
		t.Fatal(err)
	}
	params := &openapiutil.Params{
		Action: dara.String("DescribeDomainCertificateInfo"), Version: dara.String("2018-05-10"), Protocol: dara.String("HTTP"),
		Pathname: dara.String("/"), Method: dara.String("POST"), AuthType: dara.String("AK"),
		Style: dara.String("RPC"), ReqBodyType: dara.String("formData"), BodyType: dara.String("json"),
	}
	_, err = client.CallApiWithCtx(t.Context(), params, &openapiutil.OpenApiRequest{}, &dara.RuntimeOptions{})
	if code, status := ErrorCode(err); code != "InvalidDomain.NotFound" || status != http.StatusNotFound {
		t.Errorf("%T %v: %q %d", err, err, code, status)
	}
}
