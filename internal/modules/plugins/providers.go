package plugins

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// factory makes the providers of a plugin's type: each action runs the
// plugin once.
func factory(p *plugin, spec typeSpec) providers.Factory {
	return func(_ context.Context, in providers.Instance) (any, error) {
		base := instance{plugin: p, spec: spec, config: in.Config, secrets: in.Secrets}
		switch {
		case providers.Kind(spec.Kind) == providers.Source && spec.OwnKey:
			return &keySource{base}, nil
		case providers.Kind(spec.Kind) == providers.Source:
			return &source{base}, nil
		case providers.Kind(spec.Kind) == providers.Target:
			return &target{base}, nil
		case providers.Kind(spec.Kind) == providers.Notifier:
			return &notifier{base}, nil
		case spec.Challenge == providers.ChallengeHTTP:
			return &fileSolver{base}, nil
		case spec.Records:
			return &recordSolver{dnsSolver{base}}, nil
		}
		return &dnsSolver{base}, nil
	}
}

// instance is a configured provider of a plugin's type.
type instance struct {
	plugin  *plugin
	spec    typeSpec
	config  map[string]string
	secrets map[string]string
}

var (
	_ contracts.Source       = (*source)(nil)
	_ contracts.KeySource    = (*keySource)(nil)
	_ contracts.Target       = (*target)(nil)
	_ contracts.Notifier     = (*notifier)(nil)
	_ contracts.Solver       = (*dnsSolver)(nil)
	_ contracts.RecordSolver = (*recordSolver)(nil)
	_ contracts.FileSolver   = (*fileSolver)(nil)
	_ contracts.Tester       = (*instance)(nil)
	_ contracts.Revoker      = (*source)(nil)
	_ contracts.Revoker      = (*keySource)(nil)
)

// request is a request of action about the instance.
func (in *instance) request(action string) request {
	return request{Action: action, Type: in.spec.ID, Config: in.config, Secrets: in.secrets}
}

// do runs the plugin for req and returns its answer, or the error the
// answer or the call makes. The log lines of the answer go to log, with
// what the plugin wrote on its standard error when it failed.
func (in *instance) do(ctx context.Context, timeout time.Duration, req request, log func(contracts.LogLine)) (*answer, error) {
	var a answer
	stderr, err := in.plugin.call(ctx, timeout, req, &a)
	if err == nil {
		err = failure(in.plugin.id, &a)
		for _, l := range a.Log[:min(len(a.Log), maxLog)] {
			if log != nil {
				log(logLineOf(l))
			}
		}
	}
	if err != nil && len(stderr) > 0 && log != nil {
		log(contracts.LogLine{Level: contracts.Warn, Key: i18n.PluginsLogStderr, Params: map[string]any{"plugin": in.plugin.id}, Detail: string(stderr)})
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// logLineOf is a line of a plugin's log, as JEC keeps it.
func logLineOf(l logLine) contracts.LogLine {
	level := contracts.Level(l.Level)
	if !slices.Contains([]contracts.Level{contracts.Info, contracts.OK, contracts.Warn, contracts.Error}, level) {
		level = contracts.Info
	}
	return contracts.LogLine{Level: level, Key: i18n.PluginsLogLine, Params: map[string]any{"text": clip(l.Text)}}
}

// Test has the plugin check the settings without changing anything.
func (in *instance) Test(ctx context.Context) (contracts.Message, error) {
	a, err := in.do(ctx, testTimeout, in.request(actionTest), nil)
	if err != nil {
		return contracts.Message{}, err
	}
	if m := clip(a.Message); m != "" {
		return contracts.Message{Key: i18n.PluginsTestOk, Params: map[string]any{"plugin": in.plugin.id, "message": m}}, nil
	}
	return contracts.Message{Key: i18n.PluginsTestDone, Params: map[string]any{"plugin": in.plugin.id}}, nil
}

// revoke has the plugin revoke leaf, which its source issued, for reason.
// Only the providers of a type that is revocable are asked.
func (in *instance) revoke(ctx context.Context, leaf *x509.Certificate, reason contracts.RevocationReason) error {
	fp := sha256.Sum256(leaf.Raw)
	req := in.request(actionRevoke)
	req.Revoke = &revokeOrder{
		Certificate: string(cryptox.EncodeCertificates(leaf)), Serial: leaf.SerialNumber.Text(16),
		Fingerprint: hex.EncodeToString(fp[:]), Reason: reason.String(),
	}
	_, err := in.do(ctx, actionTimeout, req, nil)
	return err
}

// Revoke has the plugin revoke leaf, which this source issued.
func (s *source) Revoke(ctx context.Context, leaf *x509.Certificate, reason contracts.RevocationReason) error {
	return s.revoke(ctx, leaf, reason)
}

// Revoke has the plugin revoke leaf, which this source provided.
func (s *keySource) Revoke(ctx context.Context, leaf *x509.Certificate, reason contracts.RevocationReason) error {
	return s.revoke(ctx, leaf, reason)
}

// Close has nothing to release: each action runs the plugin anew.
func (*instance) Close() error { return nil }

// target deploys through a plugin.
type target struct{ instance }

// Deploy hands the plugin the version to install, as webhooks get it.
func (t *target) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	c, err := certificateOf(req)
	if err != nil {
		return contracts.DeployResult{}, retry.Permanent(err)
	}
	r := t.request(actionDeploy)
	r.Certificate, r.Previous = c, req.Previous
	a, err := t.do(ctx, actionTimeout, r, req.Log)
	if err != nil {
		return contracts.DeployResult{}, err
	}
	res := contracts.DeployResult{Ref: clip(a.Ref)}
	for _, ref := range a.Removed {
		// Only what earlier deployments left can be removed.
		if slices.Contains(req.Previous, ref) {
			res.Removed = append(res.Removed, ref)
		}
	}
	return res, nil
}

// certificateOf is the version of req, in PEM.
func certificateOf(req contracts.DeployRequest) (*certificate, error) {
	if len(req.Chain) == 0 {
		return nil, errors.New("plugins: no certificate to deploy")
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return nil, err
	}
	leaf := req.Chain[0]
	fp := sha256.Sum256(leaf.Raw)
	return &certificate{
		ID: req.CertificateID, Domains: req.Domains,
		Serial: leaf.SerialNumber.Text(16), Fingerprint: hex.EncodeToString(fp[:]),
		NotBefore: leaf.NotBefore.UTC(), NotAfter: leaf.NotAfter.UTC(),
		Cert:      string(cryptox.EncodeCertificates(leaf)),
		Chain:     string(cryptox.EncodeCertificates(req.Chain[1:]...)),
		Fullchain: string(cryptox.EncodeCertificates(req.Chain...)),
		Privkey:   string(key),
	}, nil
}

// notifier sends notes through a plugin.
type notifier struct{ instance }

// Notify hands the plugin the note, written in the notifier's language.
func (n *notifier) Notify(ctx context.Context, note contracts.Note) error {
	r := n.request(actionNotify)
	r.Note = &note
	_, err := n.do(ctx, actionTimeout, r, nil)
	return err
}

// dnsSolver adds and removes the TXT records of DNS-01 through a plugin.
type dnsSolver struct{ instance }

// Present has the plugin add the record of ch.
func (s *dnsSolver) Present(ctx context.Context, ch contracts.Challenge) error {
	return s.record(ctx, actionDNSPresent, ch)
}

// CleanUp has the plugin remove the record of ch.
func (s *dnsSolver) CleanUp(ctx context.Context, ch contracts.Challenge) error {
	return s.record(ctx, actionDNSCleanUp, ch)
}

// Wait is what the plugin described, or two minutes, looking every five
// seconds.
func (s *dnsSolver) Wait() (time.Duration, time.Duration) { return wait(s.spec) }

// record sends the record of ch: the TXT record at _acme-challenge of the
// domain, holding the SHA-256 of the key authorization in base64url (RFC
// 8555 section 8.4).
func (s *dnsSolver) record(ctx context.Context, action string, ch contracts.Challenge) error {
	sum := sha256.Sum256([]byte(ch.KeyAuth))
	r := s.request(action)
	r.Record = &record{Domain: ch.Domain, FQDN: "_acme-challenge." + ch.Domain + ".", Value: base64.RawURLEncoding.EncodeToString(sum[:])}
	_, err := s.do(ctx, actionTimeout, r, nil)
	return err
}

// recordSolver is a dnsSolver that adds and removes records of any type
// too, through the plugin.
type recordSolver struct{ dnsSolver }

// AddRecord has the plugin add r, and returns the ref it answers.
func (s *recordSolver) AddRecord(ctx context.Context, r contracts.Record) (string, error) {
	req := s.request(actionDNSAddRecord)
	req.DNSRecord = &dnsRecord{Name: r.Name, Type: r.Type, Value: r.Value}
	a, err := s.do(ctx, actionTimeout, req, nil)
	if err != nil {
		return "", err
	}
	return a.Ref, nil
}

// RemoveRecord has the plugin remove the record it added as ref.
func (s *recordSolver) RemoveRecord(ctx context.Context, r contracts.Record, ref string) error {
	req := s.request(actionDNSRemoveRecord)
	req.DNSRecord, req.Ref = &dnsRecord{Name: r.Name, Type: r.Type, Value: r.Value}, ref
	_, err := s.do(ctx, actionTimeout, req, nil)
	return err
}

// fileSolver puts and removes the files of HTTP validations through a
// plugin.
type fileSolver struct{ instance }

// PutFile has the plugin put f on the domain's web server.
func (s *fileSolver) PutFile(ctx context.Context, f contracts.File) error {
	return s.file(ctx, actionFilePut, f)
}

// RemoveFile has the plugin remove f.
func (s *fileSolver) RemoveFile(ctx context.Context, f contracts.File) error {
	return s.file(ctx, actionFileRemove, f)
}

func (s *fileSolver) file(ctx context.Context, action string, f contracts.File) error {
	if !f.Valid() {
		return retry.Permanent(fmt.Errorf("plugins: %s is not a file under %s", f.Path, contracts.WellKnown))
	}
	r := s.request(action)
	r.File = &file{Domain: f.Domain, Path: f.Path, Content: string(f.Content)}
	_, err := s.do(ctx, actionTimeout, r, nil)
	return err
}
