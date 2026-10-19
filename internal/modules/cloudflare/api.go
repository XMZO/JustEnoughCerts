package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/buildinfo"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// api is the address of Cloudflare's API.
const api = "https://api.cloudflare.com/client/v4"

// rest is a client of Cloudflare's API, with a token.
type rest struct {
	client *http.Client
	base   string
	token  string
}

// apiError is an error the API answered.
type apiError struct {
	status  int
	code    int
	message string
	after   time.Duration // as Retry-After asks, for 429
}

func (e *apiError) Error() string {
	return fmt.Sprintf("cloudflare: HTTP %d: [%d] %s", e.status, e.code, e.message)
}

// envelope is what every answer of the API comes in.
type envelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo struct {
		TotalCount int `json:"total_count"`
	} `json:"result_info"`
}

// call sends a request to path below the API, with body as JSON unless
// nil, and decodes the result into out unless nil. It returns the whole
// envelope too.
func (a *rest) call(ctx context.Context, method, path string, body, out any) (*envelope, error) {
	if body == nil {
		return a.send(ctx, method, path, "", nil, out)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	return a.send(ctx, method, path, "application/json", bytes.NewReader(b), out)
}

// send sends a request to path below the API with body as it is, of
// contentType, and decodes the result into out unless nil.
func (a *rest) send(ctx context.Context, method, path, contentType string, body io.Reader, out any) (*envelope, error) {
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, body)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("User-Agent", "JEC/"+buildinfo.Get().Version)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, explain(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env envelope
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, explain(err)
	}
	if json.Unmarshal(data, &env) != nil || !env.Success || resp.StatusCode >= 300 {
		e := &apiError{status: resp.StatusCode, message: http.StatusText(resp.StatusCode)}
		if len(env.Errors) > 0 {
			e.code, e.message = env.Errors[0].Code, env.Errors[0].Message
		}
		if d, ok := retry.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			e.after = d
		}
		return nil, e
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return nil, fmt.Errorf("cloudflare: %s %s: %w", method, path, err)
		}
	}
	return &env, nil
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as a token Cloudflare refuses, ends the attempts.
func explain(err error) error {
	var e *apiError
	if !errors.As(err, &e) {
		if errors.Is(err, httpx.ErrNotPublic) {
			return retry.Permanent(&contracts.Problem{Key: i18n.CloudflareErrorNetwork, Err: err})
		}
		return &contracts.Problem{Key: i18n.CloudflareErrorNetwork, Err: httpx.Cause(err)}
	}
	switch {
	case e.status == http.StatusTooManyRequests:
		p := &contracts.Problem{Key: i18n.CloudflareErrorThrottled, Err: err}
		if e.after > 0 {
			return retry.After(p, e.after)
		}
		return p
	case e.status == http.StatusUnauthorized, e.status == http.StatusForbidden, e.code == 10000, e.code == 9109:
		return retry.Permanent(&contracts.Problem{Key: i18n.CloudflareErrorAuth, Params: map[string]any{"message": e.message}, Err: err})
	}
	p := &contracts.Problem{Key: i18n.CloudflareErrorApi, Params: map[string]any{"code": e.code, "message": e.message}, Err: err}
	if e.status >= 500 {
		return p
	}
	return retry.Permanent(p)
}
