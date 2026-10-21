package kong

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // G505: UUIDs of version 5 are made with SHA-1
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// uuidForm is how Kong writes the ID of an object.
var uuidForm = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// namespace is the namespace of the UUIDs JEC names its certificates by
// (version 5, RFC 9562), so that each certificate of JEC has the same one
// at every deployment.
var namespace = [16]byte{0x6a, 0x65, 0x63, 0x2d, 0x6b, 0x6f, 0x6e, 0x67, 0x9c, 0x1d, 0x4e, 0x0b, 0x8a, 0x22, 0x51, 0x7e}

func certificateType() providers.Type {
	return providers.Type{
		ID: "kong.certificate", Kind: providers.Target, Name: i18n.KongCertificateName, Description: i18n.KongCertificateDescription,
		Tags: []string{"self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "url", Label: i18n.KongFieldUrl, Help: i18n.KongFieldUrlHelp, Kind: providers.URL, Required: true},
			{Key: "admin_token", Label: i18n.KongFieldAdminToken, Help: i18n.KongFieldAdminTokenHelp, Kind: providers.Text, Secret: true},
			{Key: "id", Label: i18n.KongFieldId, Help: i18n.KongFieldIdHelp, Kind: providers.Text, Advanced: true},
			{Key: "fingerprint", Label: i18n.KongFieldFingerprint, Help: i18n.KongFieldFingerprintHelp, Kind: providers.Text, Advanced: true},
			{Key: "allow_private", Label: i18n.KongFieldAllowPrivate, Help: i18n.KongFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
		},
	}
}

// gateway is a Kong whose certificates JEC keeps.
type gateway struct {
	client *http.Client
	base   *url.URL // the Admin API
	host   string
	token  string
	id     string // the certificate object, or "" for one named after JEC's certificate
}

var (
	_ contracts.Target = (*gateway)(nil)
	_ contracts.Tester = (*gateway)(nil)
)

// factory reads the settings of in. Over plain HTTP, which carries the
// private key in the clear, JEC reaches private addresses only; over
// HTTPS, public ones unless private ones are allowed, and a certificate of
// the gateway's own making when its fingerprint is pinned.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := strings.TrimSpace(in.Config["url"])
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.KongErrorUrl, Params: map[string]any{"url": raw}})
	}
	opts := httpx.Options{Timeout: time.Minute}
	if u.Scheme == "http" {
		opts.PrivateOnly = true
	} else {
		opts.PublicOnly = in.Config["allow_private"] != "true"
		opts.Pin = strings.TrimSpace(in.Config["fingerprint"])
	}
	client, err := httpx.Client(opts)
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.KongErrorFingerprint, Params: map[string]any{"fingerprint": opts.Pin}})
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	g := &gateway{client: client, base: u, host: u.Host, token: strings.TrimSpace(in.Value("admin_token")), id: strings.ToLower(strings.TrimSpace(in.Config["id"]))}
	if g.id != "" && !uuidForm.MatchString(g.id) {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.KongErrorId, Params: map[string]any{"id": in.Config["id"]}})
	}
	return g, nil
}

// Close lets go of idle connections.
func (g *gateway) Close() error {
	g.client.CloseIdleConnections()
	return nil
}

// Test asks the gateway what it is, with the token, changing nothing.
func (g *gateway) Test(ctx context.Context) (contracts.Message, error) {
	var node struct {
		Version string `json:"version"`
	}
	if err := g.call(ctx, http.MethodGet, "", nil, &node); err != nil {
		return contracts.Message{}, g.explain(err)
	}
	// The certificates too, which a token of too few rights may not read.
	if err := g.call(ctx, http.MethodGet, "certificates", nil, nil); err != nil {
		return contracts.Message{}, g.explain(err)
	}
	return contracts.Message{Key: i18n.KongTestOk, Params: map[string]any{"version": node.Version}}, nil
}

// objectFor is the ID of the certificate object of JEC's certificate
// certificate: the one set, else a UUID made of its ID.
func (g *gateway) objectFor(certificate int64) string {
	if g.id != "" {
		return g.id
	}
	return nameUUID("jec-certificate-" + strconv.FormatInt(certificate, 10))
}

// nameUUID is the UUID of version 5 of name in namespace (RFC 9562).
func nameUUID(name string) string {
	h := sha1.New() //nolint:gosec // G401: UUIDs of version 5 are made with SHA-1
	h.Write(namespace[:])
	h.Write([]byte(name))
	u := h.Sum(nil)[:16]
	u[6] = u[6]&0x0f | 0x50
	u[8] = u[8]&0x3f | 0x80
	s := hex.EncodeToString(u)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// Deploy puts the certificate in its object, made or replaced at once,
// with the certificate's names as its SNIs: Kong serves it for them, so
// there is nothing to remove afterwards.
func (g *gateway) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("kong: no certificate to deploy"))
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	id := g.objectFor(req.CertificateID)
	names := cryptox.Names(req.Chain[0])
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.KongLogPut, Params: map[string]any{"id": id, "names": strings.Join(names, ", ")}})
	body := map[string]any{
		"cert": string(cryptox.EncodeCertificates(req.Chain...)), "key": string(key), "snis": names, "tags": []string{"jec"},
	}
	if err := g.call(ctx, http.MethodPut, "certificates/"+id, body, nil); err != nil {
		return none, g.explain(err)
	}
	return none, nil
}

// apiError is an answer of the Admin API that says it failed.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("kong: HTTP %d %s", e.status, e.message) }

// call sends body, if any, as JSON to path of the Admin API with the
// token, if any, and decodes the answer into out.
func (g *gateway) call(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	target := g.base.String() + "/"
	if path != "" {
		target = g.base.JoinPath(path).String()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if g.token != "" {
		req.Header.Set("Kong-Admin-Token", g.token)
	}
	resp, err := g.client.Do(req)
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
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &answer)
		return &apiError{status: resp.StatusCode, message: answer.Message}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// explain turns an error of a call into a Problem: one that trying again
// cannot fix, such as a refused token, ends the attempts.
func (g *gateway) explain(err error) error {
	var ae *apiError
	switch fp, unverified := httpx.Fingerprint(err); {
	case errors.As(err, &ae) && (ae.status == http.StatusUnauthorized || ae.status == http.StatusForbidden):
		return retry.Permanent(&contracts.Problem{Key: i18n.KongErrorToken, Err: err})
	case errors.As(err, &ae) && (ae.status == http.StatusConflict || strings.Contains(ae.message, "already associated with existing certificate")):
		return retry.Permanent(&contracts.Problem{Key: i18n.KongErrorConflict, Params: map[string]any{"message": ae.message}, Err: err})
	case errors.As(err, &ae) && ae.status == http.StatusMethodNotAllowed:
		return retry.Permanent(&contracts.Problem{Key: i18n.KongErrorDbless, Err: err})
	case errors.As(err, &ae) && ae.message == "":
		problem := &contracts.Problem{Key: i18n.KongErrorStatus, Params: map[string]any{"status": ae.status}, Err: err}
		if ae.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.As(err, &ae):
		problem := &contracts.Problem{Key: i18n.KongErrorApi, Params: map[string]any{"message": ae.message}, Err: err}
		if ae.status >= 500 {
			return problem
		}
		return retry.Permanent(problem)
	case errors.Is(err, httpx.ErrNotPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.KongErrorPrivate, Params: map[string]any{"host": g.host}})
	case errors.Is(err, httpx.ErrPublic):
		return retry.Permanent(&contracts.Problem{Key: i18n.KongErrorPlain, Params: map[string]any{"host": g.host}})
	case errors.Is(err, httpx.ErrPin):
		return retry.Permanent(&contracts.Problem{Key: i18n.KongErrorPin, Err: httpx.Cause(err)})
	case unverified:
		return retry.Permanent(&contracts.Problem{Key: i18n.KongErrorUnverified, Params: map[string]any{"fingerprint": fp}})
	}
	return &contracts.Problem{Key: i18n.KongErrorNetwork, Params: map[string]any{"host": g.host}, Err: httpx.Cause(err)}
}
