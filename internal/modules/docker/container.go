package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
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

// Layouts of the directory in the container.
const (
	byDomain = "by_domain" // a subdirectory for each certificate, named after its primary domain
	flat     = "flat"      // the files right in the directory
)

// What is done once the files are in place.
const (
	actionSignal  = "signal"
	actionRestart = "restart"
	actionNone    = "none"
)

var (
	// namePattern is what Docker takes as the name or the ID of a container.
	namePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	signalPattern = regexp.MustCompile(`^[A-Za-z0-9+]+$`)
	ownerPattern  = regexp.MustCompile(`^[0-9]+:[0-9]+$`)
)

func containerType() providers.Type {
	return providers.Type{
		ID: "docker.container", Kind: providers.Target, Name: i18n.DockerContainerName, Description: i18n.DockerContainerDescription,
		Tags: []string{"self_hosted"}, Testable: true,
		Fields: []providers.Field{
			{Key: "container", Label: i18n.DockerFieldContainer, Help: i18n.DockerFieldContainerHelp, Kind: providers.Text, Required: true},
			{Key: "directory", Label: i18n.DockerFieldDirectory, Help: i18n.DockerFieldDirectoryHelp, Kind: providers.Text},
			{Key: "layout", Label: i18n.DockerFieldLayout, Kind: providers.Select, Required: true, Default: byDomain, Options: []providers.Option{
				{Value: byDomain, Label: i18n.DockerLayoutByDomain},
				{Value: flat, Label: i18n.DockerLayoutFlat},
			}},
			{Key: "action", Label: i18n.DockerFieldAction, Kind: providers.Select, Required: true, Default: actionSignal, Options: []providers.Option{
				{Value: actionSignal, Label: i18n.DockerActionSignal},
				{Value: actionRestart, Label: i18n.DockerActionRestart},
				{Value: actionNone, Label: i18n.DockerActionNone},
			}},
			{Key: "signal", Label: i18n.DockerFieldSignal, Help: i18n.DockerFieldSignalHelp, Kind: providers.Text, Default: "HUP", When: &providers.When{Key: "action", Value: actionSignal}},
			{Key: "owner", Label: i18n.DockerFieldOwner, Help: i18n.DockerFieldOwnerHelp, Kind: providers.Text, Advanced: true},
			{Key: "endpoint", Label: i18n.DockerFieldEndpoint, Help: i18n.DockerFieldEndpointHelp, Kind: providers.Text, Default: defaultEndpoint, Advanced: true},
		},
	}
}

// container is a container to deploy to.
type container struct {
	engine   *engine
	endpoint string
	name     string
	dir      string
	layout   string
	action   string
	signal   string
	uid, gid int
}

var (
	_ contracts.Target = (*container)(nil)
	_ contracts.Tester = (*container)(nil)
)

func factory(_ context.Context, in providers.Instance) (any, error) {
	endpoint := strings.TrimSpace(in.Config["endpoint"])
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	e, err := newEngine(endpoint)
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.DockerErrorEndpoint, Params: map[string]any{"endpoint": endpoint}})
	}
	c := &container{
		engine: e, endpoint: endpoint,
		name:   strings.TrimSpace(in.Config["container"]),
		dir:    strings.TrimSpace(in.Config["directory"]),
		layout: in.Config["layout"], action: in.Config["action"],
		signal: strings.TrimSpace(in.Config["signal"]),
	}
	if c.dir != "" {
		c.dir = path.Clean(c.dir)
	}
	if c.action == "" {
		c.action = actionSignal
	}
	if c.signal == "" {
		c.signal = "HUP"
	}
	if owner := strings.TrimSpace(in.Config["owner"]); owner != "" {
		if !ownerPattern.MatchString(owner) {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.DockerErrorOwner, Params: map[string]any{"owner": owner}})
		}
		uid, gid, _ := strings.Cut(owner, ":")
		c.uid, _ = strconv.Atoi(uid)
		c.gid, _ = strconv.Atoi(gid)
	}
	return c, nil
}

// Close lets go of idle connections.
func (c *container) Close() error {
	c.engine.client.CloseIdleConnections()
	return nil
}

// check refuses settings Docker would take for something else.
func (c *container) check() error {
	switch {
	case !namePattern.MatchString(c.name):
		return retry.Permanent(&contracts.Problem{Key: i18n.DockerErrorName, Params: map[string]any{"container": c.name}})
	case c.dir != "" && !path.IsAbs(c.dir):
		return retry.Permanent(&contracts.Problem{Key: i18n.DockerErrorRelative, Params: map[string]any{"directory": c.dir}})
	case c.action == actionSignal && !signalPattern.MatchString(c.signal):
		return retry.Permanent(&contracts.Problem{Key: i18n.DockerErrorSignal, Params: map[string]any{"signal": c.signal}})
	case c.dir == "" && c.action == actionNone:
		return retry.Permanent(&contracts.Problem{Key: i18n.DockerErrorNothing})
	}
	return nil
}

// Test finds the container, and the directory in it, without changing
// anything.
func (c *container) Test(ctx context.Context) (contracts.Message, error) {
	if err := c.check(); err != nil {
		return contracts.Message{}, err
	}
	name, running, status, err := c.engine.inspect(ctx, c.name)
	if err != nil {
		return contracts.Message{}, c.explain(err)
	}
	if c.dir != "" {
		ok, err := c.engine.isDir(ctx, c.name, c.dir)
		var ae *apiError
		if errors.As(err, &ae) && ae.status == http.StatusNotFound {
			ok, err = false, nil
		}
		if err != nil {
			return contracts.Message{}, c.explain(err)
		}
		if !ok {
			return contracts.Message{}, retry.Permanent(&contracts.Problem{Key: i18n.DockerErrorDirectory, Params: map[string]any{"container": name, "directory": c.dir}})
		}
	}
	if !running {
		return contracts.Message{Key: i18n.DockerTestStopped, Params: map[string]any{"container": name, "status": status}}, nil
	}
	return contracts.Message{Key: i18n.DockerTestOk, Params: map[string]any{"container": name}}, nil
}

// Deploy puts the files into the container, as certbot lays them out, and
// then signals or restarts it. A container that is not running reads the
// files when it starts, so there is nothing to signal then. Files are
// overwritten, so there is nothing to remove afterwards.
func (c *container) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if err := c.check(); err != nil {
		return none, err
	}
	if c.dir != "" {
		archive, target, err := c.archive(req)
		if err != nil {
			return none, retry.Permanent(err)
		}
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.DockerLogCopy, Params: map[string]any{"container": c.name, "directory": target}})
		if err := c.engine.extract(ctx, c.name, c.dir, archive); err != nil {
			return none, c.explain(err)
		}
	}
	switch c.action {
	case actionSignal:
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.DockerLogSignal, Params: map[string]any{"container": c.name, "signal": c.signal}})
		err := c.engine.kill(ctx, c.name, c.signal)
		var ae *apiError
		if errors.As(err, &ae) && ae.status == http.StatusConflict && strings.Contains(ae.message, "not running") {
			log(contracts.LogLine{Level: contracts.Warn, Key: i18n.DockerLogStopped, Params: map[string]any{"container": c.name}})
			return none, nil
		}
		if err != nil {
			return none, c.explain(err)
		}
	case actionRestart:
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.DockerLogRestart, Params: map[string]any{"container": c.name}})
		if err := c.engine.restart(ctx, c.name); err != nil {
			return none, c.explain(err)
		}
	}
	return none, nil
}

// archive is the tar archive of the files of req, and where they go.
func (c *container) archive(req contracts.DeployRequest) ([]byte, string, error) {
	if len(req.Chain) == 0 {
		return nil, "", errors.New("docker: no certificate to deploy")
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return nil, "", err
	}
	prefix := ""
	if c.layout != flat {
		name, err := dirName(req.Domains)
		if err != nil {
			return nil, "", err
		}
		prefix = name + "/"
	}
	files := []struct {
		name string
		data []byte
		mode int64
	}{
		{"cert.pem", cryptox.EncodeCertificates(req.Chain[0]), 0o644},
		{"chain.pem", cryptox.EncodeCertificates(req.Chain[1:]...), 0o644},
		{"fullchain.pem", cryptox.EncodeCertificates(req.Chain...), 0o644},
		{"privkey.pem", key, 0o600},
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	if prefix != "" {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: prefix, Mode: 0o755, Uid: c.uid, Gid: c.gid, ModTime: now}); err != nil {
			return nil, "", err
		}
	}
	for _, f := range files {
		h := &tar.Header{Typeflag: tar.TypeReg, Name: prefix + f.name, Mode: f.mode, Size: int64(len(f.data)), Uid: c.uid, Gid: c.gid, ModTime: now}
		if err := tw.WriteHeader(h); err != nil {
			return nil, "", err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, "", err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), path.Join(c.dir, prefix), nil
}

// explain turns an error of the engine into a Problem: one that trying
// again cannot fix, such as a container that does not exist, ends the
// attempts.
func (c *container) explain(err error) error {
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		lower := strings.ToLower(ae.message)
		switch {
		case ae.status == http.StatusNotFound && strings.Contains(lower, "no such container"):
			return retry.Permanent(&contracts.Problem{Key: i18n.DockerErrorContainer, Params: map[string]any{"container": c.name}})
		case ae.status == http.StatusNotFound:
			return retry.Permanent(&contracts.Problem{Key: i18n.DockerErrorDirectory, Params: map[string]any{"container": c.name, "directory": c.dir}})
		case ae.status == http.StatusForbidden:
			return retry.Permanent(&contracts.Problem{Key: i18n.DockerErrorForbidden, Params: map[string]any{"message": ae.message}})
		}
		p := &contracts.Problem{Key: i18n.DockerErrorApi, Params: map[string]any{"status": ae.status, "message": ae.message}}
		if ae.status >= 500 {
			return p
		}
		return retry.Permanent(p)
	case c.engine.socket != "" && errors.Is(err, fs.ErrNotExist):
		return retry.Permanent(&contracts.Problem{Key: i18n.DockerErrorSocket, Params: map[string]any{"path": c.engine.socket}})
	case c.engine.socket != "" && errors.Is(err, fs.ErrPermission):
		return retry.Permanent(&contracts.Problem{Key: i18n.DockerErrorPermission, Params: map[string]any{"path": c.engine.socket}})
	}
	return &contracts.Problem{Key: i18n.DockerErrorNetwork, Params: map[string]any{"endpoint": c.endpoint}, Err: httpx.Cause(err)}
}

// dirName names the subdirectory of a certificate after its primary
// domain, as a file name: *.example.com becomes star.example.com.
func dirName(domains []string) (string, error) {
	if len(domains) == 0 {
		return "", errors.New("docker: a certificate without domains")
	}
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		}
		return '_'
	}, strings.Replace(strings.ToLower(domains[0]), "*", "star", 1))
	if strings.Trim(name, ".") == "" {
		return "", fmt.Errorf("docker: %q makes no directory name", domains[0])
	}
	return name, nil
}
