package esa

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/alibabacloud-go/tea/dara"
	esaapi "github.com/go-acme/esa-20240910/v3/client"

	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// edgeAPI is what answering at the edge needs of ESA's API: the sites, Edge
// KV, and an Edge Routine with its code and routes. Tests replace it.
type edgeAPI interface {
	// sites lists the sites of the account.
	sites(ctx context.Context) ([]site, error)
	// namespace fails unless the Edge KV namespace exists.
	namespace(ctx context.Context, name string) error
	createNamespace(ctx context.Context, name, description string) error
	// putKV stores value under key for ttl.
	putKV(ctx context.Context, namespace, key string, value []byte, ttl time.Duration) error
	// deleteKV removes key, which may be gone already.
	deleteKV(ctx context.Context, namespace, key string) error
	// routine returns the code versions deployed to the production
	// environment of the routine, and fails unless it exists.
	routine(ctx context.Context, name string) ([]codeVersion, error)
	createRoutine(ctx context.Context, name, description string) error
	// codeVersions lists the code versions of a routine.
	codeVersions(ctx context.Context, routine string) ([]codeVersion, error)
	// uploadCode adds a code version to a routine and returns its number.
	uploadCode(ctx context.Context, routine, description string, code []byte) (string, error)
	// deployCode makes version the code of the routine's production
	// environment.
	deployCode(ctx context.Context, routine, version string) error
	deleteCode(ctx context.Context, routine, version string) error
	// routes lists the routes to a routine, on every site.
	routes(ctx context.Context, routine string) ([]route, error)
	// setRoute adds r, enabled and not bypassed, or makes the route
	// r.ConfigID so.
	setRoute(ctx context.Context, r route) error
}

type site struct {
	ID   int64
	Name string // in lowercase
}

type codeVersion struct{ Version, Description string }

// route sends the requests of a site that match Rule to Routine.
type route struct {
	ConfigID, SiteID    int64
	Name, Routine, Rule string
	Enabled, Bypass     bool
}

const envProduction = "production"

func (s *sdk) sites(ctx context.Context) ([]site, error) {
	const size = 100
	var out []site
	for page := int32(1); ; page++ {
		req := new(esaapi.ListSitesRequest).SetPageNumber(page).SetPageSize(size)
		resp, err := esaapi.ListSitesWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
		if err != nil {
			return nil, err
		}
		results := resp.Body.GetSites()
		for _, r := range results {
			out = append(out, site{ID: dara.Int64Value(r.GetSiteId()), Name: strings.ToLower(dara.StringValue(r.GetSiteName()))})
		}
		if len(results) < size || int64(len(out)) >= int64(dara.Int32Value(resp.Body.GetTotalCount())) {
			return out, nil
		}
	}
}

func (s *sdk) namespace(ctx context.Context, name string) error {
	_, err := esaapi.GetKvNamespaceWithContext(ctx, s.client, new(esaapi.GetKvNamespaceRequest).SetNamespace(name), &dara.RuntimeOptions{})
	return err
}

func (s *sdk) createNamespace(ctx context.Context, name, description string) error {
	req := new(esaapi.CreateKvNamespaceRequest).SetNamespace(name).SetDescription(description)
	_, err := esaapi.CreateKvNamespaceWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
	return err
}

// putKV sends the value in Base64, which Edge KV keeps as the bytes.
func (s *sdk) putKV(ctx context.Context, namespace, key string, value []byte, ttl time.Duration) error {
	req := new(esaapi.PutKvRequest).SetNamespace(namespace).SetKey(key).
		SetValue(base64.StdEncoding.EncodeToString(value)).SetBase64(true).SetExpirationTtl(int64(ttl / time.Second))
	_, err := esaapi.PutKvWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
	return err
}

func (s *sdk) deleteKV(ctx context.Context, namespace, key string) error {
	_, err := esaapi.DeleteKvWithContext(ctx, s.client, new(esaapi.DeleteKvRequest).SetNamespace(namespace).SetKey(key), &dara.RuntimeOptions{})
	if isNotFound(err) {
		return nil
	}
	return err
}

func (s *sdk) routine(ctx context.Context, name string) ([]codeVersion, error) {
	resp, err := esaapi.GetRoutineWithContext(ctx, s.client, new(esaapi.GetRoutineRequest).SetName(name), &dara.RuntimeOptions{})
	if err != nil {
		return nil, err
	}
	var out []codeVersion
	for _, env := range resp.Body.GetEnvs() {
		if dara.StringValue(env.GetEnv()) != envProduction || env.GetCodeDeploy() == nil {
			continue
		}
		for _, v := range env.GetCodeDeploy().GetCodeVersions() {
			out = append(out, codeVersion{Version: dara.StringValue(v.GetCodeVersion()), Description: dara.StringValue(v.GetDescription())})
		}
	}
	return out, nil
}

func (s *sdk) createRoutine(ctx context.Context, name, description string) error {
	req := new(esaapi.CreateRoutineRequest).SetName(name).SetDescription(description)
	_, err := esaapi.CreateRoutineWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
	return err
}

func (s *sdk) codeVersions(ctx context.Context, routine string) ([]codeVersion, error) {
	const size = 20
	var out []codeVersion
	for page := int64(1); ; page++ {
		req := new(esaapi.ListRoutineCodeVersionsRequest).SetName(routine).SetPageNumber(page).SetPageSize(size)
		resp, err := esaapi.ListRoutineCodeVersionsWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
		if err != nil {
			return nil, err
		}
		results := resp.Body.GetCodeVersions()
		for _, v := range results {
			out = append(out, codeVersion{Version: dara.StringValue(v.GetCodeVersion()), Description: dara.StringValue(v.GetCodeDescription())})
		}
		if len(results) < size || int64(len(out)) >= dara.Int64Value(resp.Body.GetTotalCount()) {
			return out, nil
		}
	}
}

// uploadCode stages the code through OSS, where ESA says to post it, then
// commits it as a new version.
func (s *sdk) uploadCode(ctx context.Context, routine, description string, code []byte) (string, error) {
	info, err := esaapi.GetRoutineStagingCodeUploadInfoWithContext(ctx, s.client,
		new(esaapi.GetRoutineStagingCodeUploadInfoRequest).SetName(routine).SetCodeDescription(description), &dara.RuntimeOptions{})
	if err != nil {
		return "", err
	}
	if err := s.post(ctx, info.Body.GetOssPostConfig(), code); err != nil {
		return "", err
	}
	resp, err := esaapi.CommitRoutineStagingCodeWithContext(ctx, s.client,
		new(esaapi.CommitRoutineStagingCodeRequest).SetName(routine).SetCodeDescription(description), &dara.RuntimeOptions{})
	if err != nil {
		return "", err
	}
	version := dara.StringValue(resp.Body.GetCodeVersion())
	if version == "" {
		return "", errors.New("esa: CommitRoutineStagingCode returned no code version")
	}
	return version, nil
}

// post uploads code to OSS as a form of every field of config but the
// address, with the file last. The code runs in production, so it goes over
// HTTPS whatever the scheme of the address.
func (s *sdk) post(ctx context.Context, config map[string]any, code []byte) error {
	var address string
	var fields []string
	for k, v := range config {
		if strings.EqualFold(k, "url") {
			address, _ = v.(string)
		} else if v != nil {
			fields = append(fields, k)
		}
	}
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return fmt.Errorf("esa: no address to upload the routine's code to, only %v", fields)
	}
	u.Scheme = "https"
	slices.Sort(fields)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, k := range fields {
		name, value := k, fmt.Sprint(config[k])
		if strings.EqualFold(k, "XOssSecurityToken") {
			if value == "" {
				continue
			}
			name = "x-oss-security-token"
		}
		if err := w.WriteField(name, value); err != nil {
			return err
		}
	}
	part, err := w.CreateFormFile("file", "index.js")
	if err != nil {
		return err
	}
	if _, err := part.Write(code); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := s.web.Do(req)
	if err != nil {
		return fmt.Errorf("esa: uploading the routine's code: %w", httpx.Cause(err))
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	}
	// 203 is OSS's word for an upload whose callback to ESA failed.
	return fmt.Errorf("esa: uploading the routine's code: status %d: %s", resp.StatusCode, bytes.TrimSpace(reply))
}

func (s *sdk) deployCode(ctx context.Context, routine, version string) error {
	req := new(esaapi.CreateRoutineCodeDeploymentRequest).SetName(routine).SetEnv(envProduction).SetStrategy("percentage").
		SetCodeVersions([]*esaapi.CreateRoutineCodeDeploymentRequestCodeVersions{
			new(esaapi.CreateRoutineCodeDeploymentRequestCodeVersions).SetCodeVersion(version).SetPercentage(100),
		})
	_, err := esaapi.CreateRoutineCodeDeploymentWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
	return err
}

func (s *sdk) deleteCode(ctx context.Context, routine, version string) error {
	req := new(esaapi.DeleteRoutineCodeVersionRequest).SetName(routine).SetCodeVersion(version)
	_, err := esaapi.DeleteRoutineCodeVersionWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
	return err
}

func (s *sdk) routes(ctx context.Context, routine string) ([]route, error) {
	const size = 100
	var out []route
	for page := int32(1); ; page++ {
		req := new(esaapi.ListRoutineRoutesRequest).SetRoutineName(routine).SetPageNumber(page).SetPageSize(size)
		resp, err := esaapi.ListRoutineRoutesWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
		if err != nil {
			return nil, err
		}
		results := resp.Body.GetConfigs()
		for _, c := range results {
			out = append(out, route{
				ConfigID: dara.Int64Value(c.GetConfigId()), SiteID: dara.Int64Value(c.GetSiteId()),
				Name: dara.StringValue(c.GetRouteName()), Routine: dara.StringValue(c.GetRoutineName()), Rule: dara.StringValue(c.GetRule()),
				Enabled: dara.StringValue(c.GetRouteEnable()) == "on", Bypass: dara.StringValue(c.GetBypass()) == "on",
			})
		}
		if len(results) < size || len(out) >= int(dara.Int32Value(resp.Body.GetTotalCount())) {
			return out, nil
		}
	}
}

// setRoute turns on falling back to the origin, which takes the requests
// when the routine fails.
func (s *sdk) setRoute(ctx context.Context, r route) error {
	if r.ConfigID == 0 {
		req := new(esaapi.CreateRoutineRouteRequest).SetSiteId(r.SiteID).SetRoutineName(r.Routine).SetRouteName(r.Name).
			SetRule(r.Rule).SetRouteEnable("on").SetBypass("off").SetFallback("on")
		_, err := esaapi.CreateRoutineRouteWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
		return err
	}
	req := new(esaapi.UpdateRoutineRouteRequest).SetConfigId(r.ConfigID).SetSiteId(r.SiteID).SetRoutineName(r.Routine).
		SetRouteName(r.Name).SetRule(r.Rule).SetRouteEnable("on").SetBypass("off").SetFallback("on")
	_, err := esaapi.UpdateRoutineRouteWithContext(ctx, s.client, req, &dara.RuntimeOptions{})
	return err
}

// isNotFound tells an error of ESA's API saying that what was asked for
// does not exist.
func isNotFound(err error) bool {
	code, status := apiErrorCode(err)
	return status == http.StatusNotFound || strings.Contains(code, "NotFound") || strings.Contains(code, "NotExist")
}
