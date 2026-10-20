package offsite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// bucket is an S3 compatible bucket: AWS S3, Cloudflare R2, MinIO, and the
// S3 interfaces of Alibaba Cloud OSS and Tencent Cloud COS among them.
type bucket struct {
	client    *http.Client
	endpoint  string // https://s3.example.com
	region    string
	name      string
	pathStyle bool // the bucket in the path rather than the host
	access    string
	secret    string
	now       func() time.Time
}

// objectURL is the address of key in the bucket.
func (b *bucket) objectURL(key string) (string, error) {
	u, err := url.Parse(strings.TrimRight(b.endpoint, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("offsite: %q is no endpoint", b.endpoint)
	}
	key = strings.TrimPrefix(key, "/")
	if b.pathStyle {
		u.Path = "/" + b.name + "/" + key
	} else {
		u.Host = b.name + "." + u.Host
		u.Path = "/" + key
	}
	return u.String(), nil
}

// put stores data as key.
func (b *bucket) put(ctx context.Context, key string, data []byte) error {
	return b.do(ctx, http.MethodPut, key, data)
}

// remove deletes key.
func (b *bucket) remove(ctx context.Context, key string) error {
	return b.do(ctx, http.MethodDelete, key, nil)
}

func (b *bucket) do(ctx context.Context, method, key string, data []byte) error {
	target, err := b.objectURL(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(data))
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	sum := sha256.Sum256(data)
	payload := hex.EncodeToString(sum[:])
	req.Header.Set("X-Amz-Content-Sha256", payload)
	creds := aws.Credentials{AccessKeyID: b.access, SecretAccessKey: b.secret}
	if err := v4.NewSigner().SignHTTP(ctx, creds, req, payload, "s3", b.region, b.now()); err != nil {
		return err
	}
	res, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 == 2 {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if xml.Unmarshal(body, &e) == nil && e.Code != "" {
		return fmt.Errorf("HTTP %d %s: %s", res.StatusCode, e.Code, e.Message)
	}
	return fmt.Errorf("HTTP %d", res.StatusCode)
}
