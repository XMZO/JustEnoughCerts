package offsite

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// folder is a folder of a WebDAV server, as Nextcloud, Synology, Alist and
// Jianguoyun have, which the backups are put in with the account's name and
// password.
type folder struct {
	client   *http.Client
	endpoint string // https://dav.example.com/remote.php/dav/files/me/jec
	user     string
	password string
}

// fileURL is the address of key in the folder.
func (f *folder) fileURL(key string) (string, error) {
	u, err := url.Parse(strings.TrimRight(f.endpoint, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("offsite: %q is no WebDAV address", f.endpoint)
	}
	return u.String() + "/" + strings.TrimPrefix(key, "/"), nil
}

// put stores data as key, making the folders of a key with slashes first.
func (f *folder) put(ctx context.Context, key string, data []byte) error {
	parts := strings.Split(strings.Trim(key, "/"), "/")
	for i := 1; i < len(parts); i++ {
		dir, err := f.fileURL(strings.Join(parts[:i], "/") + "/")
		if err != nil {
			return err
		}
		// 405 tells it is there already; anything else shows on the PUT.
		_ = f.do(ctx, "MKCOL", dir, nil)
	}
	target, err := f.fileURL(key)
	if err != nil {
		return err
	}
	return f.do(ctx, http.MethodPut, target, data)
}

// remove deletes key.
func (f *folder) remove(ctx context.Context, key string) error {
	target, err := f.fileURL(key)
	if err != nil {
		return err
	}
	return f.do(ctx, http.MethodDelete, target, nil)
}

func (f *folder) do(ctx context.Context, method, target string, data []byte) error {
	var body io.Reader
	if data != nil {
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	if data != nil {
		req.ContentLength = int64(len(data))
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	req.SetBasicAuth(f.user, f.password)
	res, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 == 2 {
		return nil
	}
	return fmt.Errorf("%s: HTTP %d %s", method, res.StatusCode, http.StatusText(res.StatusCode))
}
