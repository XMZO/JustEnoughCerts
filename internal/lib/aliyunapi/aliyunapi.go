// Package aliyunapi holds what the modules of Alibaba Cloud's services
// share about its API.
package aliyunapi

import (
	"errors"

	"github.com/alibabacloud-go/tea/dara"
	"github.com/alibabacloud-go/tea/tea"
)

// ErrorCode returns the code of an error of Alibaba Cloud's API, such as
// InvalidAccessKeyId.NotFound, and its HTTP status; "" and 0 for another
// error, as of the network. The OpenAPI client, which the SDKs of the
// services call, returns *tea.SDKError, though the SDKs speak of
// *dara.SDKError: both count.
func ErrorCode(err error) (string, int) {
	var t *tea.SDKError
	if errors.As(err, &t) {
		return tea.StringValue(t.Code), tea.IntValue(t.StatusCode)
	}
	var d *dara.SDKError
	if errors.As(err, &d) {
		return dara.StringValue(d.Code), dara.IntValue(d.StatusCode)
	}
	return "", 0
}
