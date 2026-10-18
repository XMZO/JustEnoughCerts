package docker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// defaultEndpoint is the socket Docker listens on, mounted into the
// container of JEC.
const defaultEndpoint = "unix:///var/run/docker.sock"

// requestTimeout bounds a request, a restart included.
const requestTimeout = 2 * time.Minute

// errEndpoint is an endpoint JEC cannot reach Docker at.
var errEndpoint = errors.New("docker: not an endpoint")

// engine talks to the Docker Engine API, in its current version: the paths
// carry none, so that Docker answers in its own.
type engine struct {
	client *http.Client
	base   string // such as http://docker
	socket string // the path of the socket; empty over TCP
}

// newEngine reaches Docker at endpoint: unix:// and the path of a socket,
// or tcp://, http:// or https:// and a host and port, as of a socket proxy.
func newEngine(endpoint string) (*engine, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errEndpoint
	}
	switch u.Scheme {
	case "unix":
		if u.Host != "" || u.Path == "" {
			return nil, errEndpoint
		}
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		t := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", u.Path)
			},
			MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second,
		}
		return &engine{client: &http.Client{Transport: t, Timeout: requestTimeout, CheckRedirect: noRedirect}, base: "http://docker", socket: u.Path}, nil
	case "tcp", "http", "https":
		if u.Host == "" || (u.Path != "" && u.Path != "/") {
			return nil, errEndpoint
		}
		scheme := "http"
		if u.Scheme == "https" {
			scheme = "https"
		}
		// Docker is on this machine or its network: no PublicOnly.
		client, err := httpx.Client(httpx.Options{Timeout: requestTimeout})
		if err != nil {
			return nil, err
		}
		client.CheckRedirect = noRedirect
		return &engine{client: client, base: scheme + "://" + u.Host}, nil
	}
	return nil, errEndpoint
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// apiError is an answer of Docker that is no success.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("docker: HTTP %d: %s", e.status, e.message) }

// do sends a request to the API and returns the answer of a success,
// whose body the caller closes, or the error Docker gives.
func (e *engine) do(ctx context.Context, method, path string, query url.Values, body []byte, contentType string) (*http.Response, error) {
	target := e.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	var reply struct {
		Message string `json:"message"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(data, &reply) != nil {
		reply.Message = string(bytes.TrimSpace(data))
	}
	return nil, &apiError{status: resp.StatusCode, message: reply.Message}
}

// call sends a request whose answer has nothing to read.
func (e *engine) call(ctx context.Context, method, path string, query url.Values, body []byte, contentType string) error {
	resp, err := e.do(ctx, method, path, query, body, contentType)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.Body.Close()
}

// inspect returns the name of a container and whether it runs, with its
// status, such as exited.
func (e *engine) inspect(ctx context.Context, container string) (name string, running bool, status string, err error) {
	resp, err := e.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(container)+"/json", nil, nil, "")
	if err != nil {
		return "", false, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var c struct {
		Name  string `json:"Name"`
		State struct {
			Status  string `json:"Status"`
			Running bool   `json:"Running"`
		} `json:"State"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&c); err != nil {
		return "", false, "", fmt.Errorf("docker: inspecting %s: %w", container, err)
	}
	if len(c.Name) > 1 && c.Name[0] == '/' {
		c.Name = c.Name[1:]
	}
	return c.Name, c.State.Running, c.State.Status, nil
}

// isDir reports whether path is a directory in the container.
func (e *engine) isDir(ctx context.Context, container, path string) (bool, error) {
	resp, err := e.do(ctx, http.MethodHead, "/containers/"+url.PathEscape(container)+"/archive", url.Values{"path": {path}}, nil, "")
	if err != nil {
		return false, err
	}
	_ = resp.Body.Close()
	raw, err := base64.StdEncoding.DecodeString(resp.Header.Get("X-Docker-Container-Path-Stat"))
	if err != nil {
		return false, fmt.Errorf("docker: the stat of %s: %w", path, err)
	}
	var stat struct {
		Mode uint32 `json:"mode"`
	}
	if err := json.Unmarshal(raw, &stat); err != nil {
		return false, fmt.Errorf("docker: the stat of %s: %w", path, err)
	}
	return fs.FileMode(stat.Mode).IsDir(), nil
}

// extract unpacks a tar archive into dir in the container.
func (e *engine) extract(ctx context.Context, container, dir string, archive []byte) error {
	return e.call(ctx, http.MethodPut, "/containers/"+url.PathEscape(container)+"/archive", url.Values{"path": {dir}}, archive, "application/x-tar")
}

// kill sends signal to the container.
func (e *engine) kill(ctx context.Context, container, signal string) error {
	return e.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/kill", url.Values{"signal": {signal}}, nil, "")
}

// restart restarts the container, or starts it.
func (e *engine) restart(ctx context.Context, container string) error {
	return e.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/restart", nil, nil, "")
}
