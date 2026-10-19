package gcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// defaultBase is Gcore's API.
const defaultBase = "https://api.gcore.com"

func cdnType() providers.Type {
	return providers.Type{
		ID: "gcore.cdn", Kind: providers.Target, Name: i18n.GcoreCdnName, Description: i18n.GcoreCdnDescription,
		Testable: true,
		Fields: []providers.Field{
			{Key: "api_token", Label: i18n.GcoreFieldApiToken, Help: i18n.GcoreFieldApiTokenHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "resources", Label: i18n.GcoreFieldResources, Help: i18n.GcoreFieldResourcesHelp, Kind: providers.Text, Required: true},
			{Key: "delete_old", Label: i18n.GcoreFieldDeleteOld, Help: i18n.GcoreFieldDeleteOldHelp, Kind: providers.Switch, Default: "true"},
			httpx.ProxyField(),
		},
		Credential: credentials.Use(credentials.Gcore),
	}
}

// cdn is the CDN of a Gcore account.
type cdn struct {
	client    *http.Client
	base      string
	token     string
	resources []int64
	deleteOld bool
}

var (
	_ contracts.Target = (*cdn)(nil)
	_ contracts.Tester = (*cdn)(nil)
)

func factory(_ context.Context, in providers.Instance) (any, error) {
	var resources []int64
	for _, s := range strings.FieldsFunc(in.Config["resources"], func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.GcoreErrorResources, Params: map[string]any{"value": s}})
		}
		if !slices.Contains(resources, id) {
			resources = append(resources, id)
		}
	}
	if len(resources) == 0 {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.GcoreErrorResources, Params: map[string]any{"value": "-"}})
	}
	client, err := httpx.ClientFor(httpx.Options{Timeout: time.Minute, PublicOnly: true}, in.Config)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &cdn{client: client, base: defaultBase, token: strings.TrimSpace(in.Value("api_token")),
		resources: resources, deleteOld: in.Config["delete_old"] != "false"}, nil
}

// Close lets go of idle connections.
func (c *cdn) Close() error {
	c.client.CloseIdleConnections()
	return nil
}

// resource is a CDN resource, as GET /cdn/resources/<id> reports it.
type resource struct {
	ID         int64  `json:"id"`
	CNAME      string `json:"cname"`
	SSLEnabled bool   `json:"sslEnabled"`
	SSLData    *int64 `json:"sslData"`
}

// serves says whether the resource serves the certificate id over HTTPS.
func (r resource) serves(id int64) bool {
	return r.SSLEnabled && r.SSLData != nil && *r.SSLData == id
}

// Test reads the CDN resources, without changing anything.
func (c *cdn) Test(ctx context.Context) (contracts.Message, error) {
	var names []string
	for _, id := range c.resources {
		r, err := c.resource(ctx, id)
		if err != nil {
			return contracts.Message{}, err
		}
		names = append(names, r.CNAME)
	}
	return contracts.Message{Key: i18n.GcoreTestOk, Params: map[string]any{"names": strings.Join(names, ", ")}}, nil
}

func (c *cdn) resource(ctx context.Context, id int64) (resource, error) {
	var r resource
	if err := c.call(ctx, http.MethodGet, "/cdn/resources/"+strconv.FormatInt(id, 10), nil, &r); err != nil {
		return r, c.explain(err, id)
	}
	return r, nil
}

// Deploy uploads the certificate and has each resource serve it over
// HTTPS, turning HTTPS on where it is off. Then it deletes the
// certificates earlier deployments uploaded, if set to.
func (c *cdn) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("gcore: no certificate to deploy"))
	}
	id, err := c.upload(ctx, req, log)
	if err != nil {
		return none, err
	}
	for _, rid := range c.resources {
		r, err := c.resource(ctx, rid)
		if err != nil {
			return none, err
		}
		if r.serves(id) {
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.GcoreLogCurrent, Params: map[string]any{"name": r.CNAME}})
			continue
		}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.GcoreLogAttach, Params: map[string]any{"name": r.CNAME}})
		body := map[string]any{"sslEnabled": true, "sslData": id}
		if err := c.call(ctx, http.MethodPatch, "/cdn/resources/"+strconv.FormatInt(rid, 10), body, nil); err != nil {
			return none, c.explain(err, rid)
		}
	}
	result := contracts.DeployResult{Ref: strconv.FormatInt(id, 10)}
	if c.deleteOld {
		result.Removed = c.removeOld(ctx, req.Previous, id, log)
	}
	return result, nil
}

// storedCert is a certificate in Gcore's store.
type storedCert struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// upload stores the certificate under a name made of its ID in JEC and its
// fingerprint, and returns its ID at Gcore. Names are unique there: when
// Gcore refuses the name, an earlier attempt that failed later on stored
// it already, and JEC looks it up.
func (c *cdn) upload(ctx context.Context, req contracts.DeployRequest, log func(contracts.LogLine)) (int64, error) {
	name := "jec-" + strconv.FormatInt(req.CertificateID, 10) + "-" + cryptox.Fingerprint(req.Chain[0])[:12]
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return 0, retry.Permanent(err)
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.GcoreLogUpload, Params: map[string]any{"name": name}})
	var stored storedCert
	body := map[string]any{"name": name, "sslCertificate": string(cryptox.EncodeCertificates(req.Chain...)), "sslPrivateKey": string(key), "validate_root_ca": false}
	err = c.call(ctx, http.MethodPost, "/cdn/sslData", body, &stored)
	if status(err) == http.StatusBadRequest {
		var all []storedCert
		if c.call(ctx, http.MethodGet, "/cdn/sslData", nil, &all) == nil {
			if i := slices.IndexFunc(all, func(s storedCert) bool { return s.Name == name }); i >= 0 {
				log(contracts.LogLine{Level: contracts.Info, Key: i18n.GcoreLogFound, Params: map[string]any{"id": strconv.FormatInt(all[i].ID, 10)}})
				return all[i].ID, nil
			}
		}
	}
	if err != nil {
		return 0, c.explain(err, 0)
	}
	if stored.ID == 0 {
		return 0, c.explain(&statusError{status: http.StatusOK}, 0)
	}
	log(contracts.LogLine{Level: contracts.OK, Key: i18n.GcoreLogUploaded, Params: map[string]any{"id": strconv.FormatInt(stored.ID, 10)}})
	return stored.ID, nil
}

// removeOld deletes the certificates of earlier deployments, but id, and
// returns those it deleted. One that a resource JEC does not manage still
// serves may be refused; that is only logged.
func (c *cdn) removeOld(ctx context.Context, previous []string, id int64, log func(contracts.LogLine)) []string {
	var removed []string
	for _, ref := range previous {
		old, err := strconv.ParseInt(ref, 10, 64)
		if err != nil || old == id || slices.Contains(removed, ref) {
			continue
		}
		if err := c.call(ctx, http.MethodDelete, "/cdn/sslData/"+ref, nil, nil); err != nil {
			if status(err) == http.StatusNotFound {
				removed = append(removed, ref)
				continue
			}
			log(contracts.LogLine{Level: contracts.Warn, Key: i18n.GcoreLogRemoveFailed, Params: map[string]any{"id": ref}, Detail: err.Error()})
			continue
		}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.GcoreLogRemoved, Params: map[string]any{"id": ref}})
		removed = append(removed, ref)
	}
	return removed
}

// apiError is an answer of the API that says it failed.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("gcore: HTTP %d: %s", e.status, e.message) }

// statusError is an answer that is no answer of the API.
type statusError struct{ status int }

func (e *statusError) Error() string { return fmt.Sprintf("gcore: HTTP %d", e.status) }

// status is the HTTP status of an answer that err is about, else 0.
func status(err error) int {
	var ae *apiError
	var se *statusError
	switch {
	case errors.As(err, &ae):
		return ae.status
	case errors.As(err, &se):
		return se.status
	}
	return 0
}

// call sends body as JSON, when not nil, to path of the API, with the API
// token, and decodes the answer into out.
func (c *cdn) call(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "APIKey "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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
		if message := problemOf(raw); message != "" {
			return &apiError{status: resp.StatusCode, message: message}
		}
		return &statusError{status: resp.StatusCode}
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, out) != nil {
		return &statusError{status: resp.StatusCode}
	}
	return nil
}

// problemOf reads what an answer of the API says went wrong: its message
// or detail, or its errors, by field as Gcore lists them.
func problemOf(raw []byte) string {
	var answer struct {
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
		Errors  json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(raw, &answer) != nil {
		return ""
	}
	if s := strings.TrimSpace(answer.Message + " " + answer.Detail); s != "" {
		return s
	}
	var fields map[string][]string
	if json.Unmarshal(answer.Errors, &fields) == nil && len(fields) > 0 {
		var parts []string
		for _, k := range slices.Sorted(maps.Keys(fields)) {
			parts = append(parts, k+": "+strings.Join(fields[k], " "))
		}
		return strings.Join(parts, "; ")
	}
	var list []string
	if json.Unmarshal(answer.Errors, &list) == nil {
		return strings.Join(list, "; ")
	}
	return ""
}

// explain turns an error of a call about the resource id, if not 0, into a
// Problem: one that trying again cannot fix, such as a refused token or a
// resource the account does not have, ends the attempts.
func (c *cdn) explain(err error, id int64) error {
	var ae *apiError
	var se *statusError
	switch {
	case status(err) == http.StatusUnauthorized || status(err) == http.StatusForbidden:
		message := http.StatusText(status(err))
		if errors.As(err, &ae) {
			message = ae.message
		}
		return retry.Permanent(&contracts.Problem{Key: i18n.GcoreErrorToken, Params: map[string]any{"message": message}, Err: err})
	case status(err) == http.StatusNotFound && id != 0:
		return retry.Permanent(&contracts.Problem{Key: i18n.GcoreErrorResource, Params: map[string]any{"id": strconv.FormatInt(id, 10)}, Err: err})
	case errors.As(err, &ae):
		problem := &contracts.Problem{Key: i18n.GcoreErrorApi, Params: map[string]any{"message": ae.message, "status": ae.status}, Err: err}
		if ae.status >= 500 || ae.status == http.StatusTooManyRequests || ae.status == http.StatusRequestTimeout {
			return problem
		}
		return retry.Permanent(problem)
	case errors.As(err, &se):
		problem := &contracts.Problem{Key: i18n.GcoreErrorStatus, Params: map[string]any{"status": se.status}, Err: err}
		if se.status >= 500 || se.status == http.StatusTooManyRequests {
			return problem
		}
		return retry.Permanent(problem)
	}
	return &contracts.Problem{Key: i18n.GcoreErrorNetwork, Err: httpx.Cause(err)}
}
