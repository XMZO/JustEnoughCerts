package kubernetes

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// tlsType is the type of Secret that holds a certificate and its key.
const tlsType = "kubernetes.io/tls"

// fieldManager names JEC in the managed fields of what it writes.
const fieldManager = "jec"

// In a pod, Kubernetes says where the API server is in the environment,
// and mounts the service account's token, which it renews, and the
// cluster's CA at accountDir.
var (
	getenv     = os.Getenv
	accountDir = "/var/run/secrets/kubernetes.io/serviceaccount"
)

func secretType() providers.Type {
	return providers.Type{
		ID: "kubernetes.secret", Kind: providers.Target, Name: i18n.KubernetesSecretName, Description: i18n.KubernetesSecretDescription,
		Tags: []string{"self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.KubernetesFieldUrl, Help: i18n.KubernetesFieldUrlHelp, Kind: providers.URL},
			{Key: "token", Label: i18n.KubernetesFieldToken, Help: i18n.KubernetesFieldTokenHelp, Kind: providers.Text, Secret: true},
			{Key: "ca", Label: i18n.KubernetesFieldCa, Help: i18n.KubernetesFieldCaHelp, Kind: providers.Multiline},
			{Key: "namespace", Label: i18n.KubernetesFieldNamespace, Kind: providers.Text, Required: true, Default: "default"},
			{Key: "secret", Label: i18n.KubernetesFieldSecret, Help: i18n.KubernetesFieldSecretHelp, Kind: providers.Text, Required: true},
			{Key: "allow_private", Label: i18n.KubernetesFieldAllowPrivate, Help: i18n.KubernetesFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		},
	}
}

// cluster is the API server of a cluster, with a Secret to keep up to date.
type cluster struct {
	client    *http.Client
	base      *url.URL
	host      string
	token     string // empty in the cluster, where the account's file has it
	namespace string
	secret    string
}

var (
	_ contracts.Target = (*cluster)(nil)
	_ contracts.Tester = (*cluster)(nil)
)

// factory reads the settings of in. Without an address, JEC runs in the
// cluster and uses the service account of its pod, with the CA mounted
// there. Elsewhere it trusts the CA given, or else those of the system;
// it reaches public addresses only, unless private ones are allowed.
func factory(_ context.Context, in providers.Instance) (any, error) {
	c := &cluster{token: strings.TrimSpace(in.Value("token")), namespace: strings.TrimSpace(in.Config["namespace"]), secret: strings.TrimSpace(in.Config["secret"])}
	opts := httpx.Options{Timeout: time.Minute}
	raw := strings.TrimSpace(in.Config["url"])
	if raw == "" {
		host, port := getenv("KUBERNETES_SERVICE_HOST"), getenv("KUBERNETES_SERVICE_PORT")
		if host == "" || port == "" {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorOutside})
		}
		raw, c.token = "https://"+net.JoinHostPort(host, port), ""
		ca, err := os.ReadFile(filepath.Join(accountDir, "ca.crt"))
		if err != nil {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorAccount, Err: err})
		}
		if opts.Roots, err = pool(string(ca)); err != nil {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorAccount, Err: err})
		}
	} else {
		var err error
		if opts.Roots, err = pool(in.Config["ca"]); err != nil {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorCa})
		}
		opts.PublicOnly = in.Config["allow_private"] != "true"
		if c.token == "" {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorNoToken})
		}
	}
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorUrl, Params: map[string]any{"url": raw}})
	}
	c.base, c.host = u, u.Host
	if c.client, err = httpx.Client(opts); err != nil {
		return nil, err
	}
	c.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c, nil
}

// pool reads the certificates of a CA, in PEM or, as kubeconfig files
// hold them, in the Base64 of PEM; nil for none.
func pool(s string) (*x509.CertPool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	data := []byte(s)
	if !strings.HasPrefix(s, "-----BEGIN") {
		decoded, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
		if err != nil {
			return nil, err
		}
		data = decoded
	}
	certs, err := cryptox.ParseCertificates(data)
	if err != nil {
		return nil, err
	}
	if len(certs) == 0 {
		return nil, errors.New("kubernetes: no certificate")
	}
	roots := x509.NewCertPool()
	for _, c := range certs {
		roots.AddCert(c)
	}
	return roots, nil
}

// Close lets go of idle connections.
func (c *cluster) Close() error {
	c.client.CloseIdleConnections()
	return nil
}

// name is the Secret as kubectl names it.
func (c *cluster) name() string { return c.namespace + "/" + c.secret }

// bearer returns the token: the one set, or in the cluster the service
// account's, read anew each time, as Kubernetes renews it.
func (c *cluster) bearer() (string, error) {
	if c.token != "" {
		return c.token, nil
	}
	token, err := os.ReadFile(filepath.Join(accountDir, "token"))
	if err != nil {
		return "", retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorAccount, Err: err})
	}
	return strings.TrimSpace(string(token)), nil
}

// secretObject is a Secret, as far as JEC reads it.
type secretObject struct {
	Type string `json:"type"`
}

// get gets the Secret, and tells whether it is there.
func (c *cluster) get(ctx context.Context, token string) (secretObject, bool, error) {
	var s secretObject
	err := c.call(ctx, token, http.MethodGet, nil, "", nil, &s, "api", "v1", "namespaces", c.namespace, "secrets", c.secret)
	var ae *apiError
	if errors.As(err, &ae) && ae.status == http.StatusNotFound {
		return s, false, nil
	}
	return s, err == nil, err
}

// may asks the API server whether the token may verb the Secret.
func (c *cluster) may(ctx context.Context, token, verb string) (bool, error) {
	attributes := map[string]string{"namespace": c.namespace, "verb": verb, "resource": "secrets"}
	if verb != "create" {
		attributes["name"] = c.secret
	}
	review := map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview",
		"spec": map[string]any{"resourceAttributes": attributes}}
	var answer struct {
		Status struct {
			Allowed bool `json:"allowed"`
		} `json:"status"`
	}
	err := c.call(ctx, token, http.MethodPost, nil, "application/json", review, &answer, "apis", "authorization.k8s.io", "v1", "selfsubjectaccessreviews")
	return answer.Status.Allowed, err
}

// Test asks the API server whether the token may get, create and patch the
// Secret, and whether it is there, without changing anything.
func (c *cluster) Test(ctx context.Context) (contracts.Message, error) {
	token, err := c.bearer()
	if err != nil {
		return contracts.Message{}, err
	}
	var denied []string
	for _, verb := range []string{"get", "create", "patch"} {
		allowed, err := c.may(ctx, token, verb)
		if err != nil {
			return contracts.Message{}, c.explain(err)
		}
		if !allowed {
			denied = append(denied, verb)
		}
	}
	if len(denied) > 0 {
		return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorVerbs,
			Params: map[string]any{"verbs": strings.Join(denied, ", "), "secret": c.name()}})
	}
	s, found, err := c.get(ctx, token)
	switch {
	case err != nil:
		return contracts.Message{}, c.explain(err)
	case !found:
		return contracts.Message{Key: i18n.KubernetesTestNew, Params: map[string]any{"secret": c.name()}}, nil
	case s.Type != tlsType && s.Type != "Opaque":
		return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorType, Params: map[string]any{"secret": c.name(), "type": s.Type}})
	}
	return contracts.Message{Key: i18n.KubernetesTestFound, Params: map[string]any{"secret": c.name()}}, nil
}

// Deploy writes the certificate chain and its key into the Secret, as
// tls.crt and tls.key: it creates a TLS Secret when there is none, and
// otherwise patches those two keys, leaving the rest as it is. Workloads
// that read the Secret, such as Ingress controllers, pick up the change
// themselves. The Secret is overwritten, so nothing is left to remove.
func (c *cluster) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("kubernetes: no certificate to deploy"))
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	data := map[string]string{
		"tls.crt": base64.StdEncoding.EncodeToString(cryptox.EncodeCertificates(req.Chain...)),
		"tls.key": base64.StdEncoding.EncodeToString(key),
	}
	token, err := c.bearer()
	if err != nil {
		return none, err
	}
	s, found, err := c.get(ctx, token)
	switch {
	case err != nil:
		return none, c.explain(err)
	case found && s.Type != tlsType && s.Type != "Opaque":
		return none, retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorType, Params: map[string]any{"secret": c.name(), "type": s.Type}})
	}
	manager := url.Values{"fieldManager": {fieldManager}}
	if !found {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.KubernetesLogCreate, Params: map[string]any{"secret": c.name()}})
		object := map[string]any{"apiVersion": "v1", "kind": "Secret", "type": tlsType, "data": data,
			"metadata": map[string]any{"name": c.secret, "namespace": c.namespace, "labels": map[string]string{"app.kubernetes.io/managed-by": "jec"}}}
		err := c.call(ctx, token, http.MethodPost, manager, "application/json", object, nil, "api", "v1", "namespaces", c.namespace, "secrets")
		var ae *apiError
		switch {
		case err == nil:
			return none, nil
		case !errors.As(err, &ae) || ae.reason != "AlreadyExists":
			return none, c.explain(err)
		}
		// Someone created it meanwhile: patch it, as if it were there.
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.KubernetesLogPatch, Params: map[string]any{"secret": c.name()}})
	err = c.call(ctx, token, http.MethodPatch, manager, "application/merge-patch+json", map[string]any{"data": data}, nil,
		"api", "v1", "namespaces", c.namespace, "secrets", c.secret)
	if err != nil {
		return none, c.explain(err)
	}
	return none, nil
}

// apiError is an answer of the API server that says it failed: a Status.
type apiError struct {
	status          int
	reason, message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("kubernetes: HTTP %d %s: %s", e.status, e.reason, e.message)
}

// statusError is an answer that is no answer of the API server.
type statusError struct{ status int }

func (e *statusError) Error() string { return fmt.Sprintf("kubernetes: HTTP %d", e.status) }

// call sends body as contentType, when not nil, to the API server at
// path, with query and the token, and decodes the answer into out.
func (c *cluster) call(ctx context.Context, token, method string, query url.Values, contentType string, body, out any, path ...string) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(data)
	}
	u := c.base.JoinPath(path...)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var status struct {
			Kind    string `json:"kind"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &status) == nil && status.Kind == "Status" {
			return &apiError{status: resp.StatusCode, reason: status.Reason, message: status.Message}
		}
		return &statusError{status: resp.StatusCode}
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return &statusError{status: resp.StatusCode}
	}
	return nil
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as a refused token or a missing namespace, ends the
// attempts.
func (c *cluster) explain(err error) error {
	var ae *apiError
	var se *statusError
	var he x509.HostnameError
	switch fp, unverified := httpx.Fingerprint(err); {
	case errors.As(err, &ae) && ae.status == http.StatusUnauthorized:
		return retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorToken, Err: err})
	case errors.As(err, &ae) && ae.status == http.StatusForbidden:
		return retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorForbidden, Params: map[string]any{"message": ae.message}, Err: err})
	case errors.As(err, &ae):
		problem := &contracts.Problem{Key: i18n.KubernetesErrorApi, Params: map[string]any{"message": ae.message, "status": ae.status}, Err: err}
		if ae.status >= 500 || ae.status == http.StatusConflict || ae.status == http.StatusTooManyRequests {
			return problem
		}
		return retry.Permanent(problem)
	case errors.As(err, &se):
		problem := &contracts.Problem{Key: i18n.KubernetesErrorStatus, Params: map[string]any{"status": se.status}, Err: err}
		if se.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.Is(err, httpx.ErrNotPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorPrivate, Params: map[string]any{"host": c.host}})
	case errors.As(err, &he):
		return retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorHostname, Params: map[string]any{"host": c.host}, Err: httpx.Cause(err)})
	case unverified:
		return retry.Permanent(&contracts.Problem{Key: i18n.KubernetesErrorUnverified, Params: map[string]any{"fingerprint": fp}})
	}
	return &contracts.Problem{Key: i18n.KubernetesErrorNetwork, Params: map[string]any{"host": c.host}, Err: httpx.Cause(err)}
}
