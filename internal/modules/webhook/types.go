package webhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

func notifyType() providers.Type {
	return providers.Type{
		ID: "webhook.notify", Kind: providers.Notifier, Name: i18n.WebhookNotifyName, Description: i18n.WebhookNotifyDescription,
		Tags: []string{"self_hosted"},
		Fields: append([]providers.Field{
			{Key: "url", Label: i18n.WebhookFieldUrl, Help: i18n.WebhookFieldUrlHelp, Kind: providers.URL, Required: true},
			secretField, headerField, allowField, methodField, bodyField(i18n.WebhookFieldBodyHelp), typeField,
		}, contracts.NotifierFields()...),
	}
}

func deployType() providers.Type {
	return providers.Type{
		ID: "webhook.deploy", Kind: providers.Target, Name: i18n.WebhookDeployName, Description: i18n.WebhookDeployDescription,
		Tags: []string{"self_hosted"},
		Fields: []providers.Field{
			{Key: "url", Label: i18n.WebhookFieldUrl, Help: i18n.WebhookFieldDeployUrlHelp, Kind: providers.URL, Required: true},
			secretField, headerField, allowField, methodField, bodyField(i18n.WebhookFieldDeployBodyHelp), typeField,
		},
	}
}

// notifier posts notes: contracts.Note as JSON, or through a template.
type notifier struct{ e *endpoint }

var _ contracts.Notifier = (*notifier)(nil)

func newNotifier(_ context.Context, in providers.Instance) (any, error) {
	e, err := newEndpoint(in)
	if err != nil {
		return nil, err
	}
	return &notifier{e: e}, nil
}

func (n *notifier) Notify(ctx context.Context, note contracts.Note) error {
	// A note without a link or data has them empty for templates.
	body, err := n.e.body(note, map[string]any{"link": "", "data": map[string]any{}})
	if err != nil {
		return err
	}
	_, err = n.e.post(ctx, note.Event, body)
	return err
}

func (n *notifier) Close() error {
	n.e.client.CloseIdleConnections()
	return nil
}

// DeployEvent is the X-JEC-Event of a deployment.
const DeployEvent = "certificate.deploy"

// Deployment is what a deployment posts: the certificate with its chain
// and private key, and what earlier deployments left at the receiver.
type Deployment struct {
	Event       string    `json:"event"`
	Certificate int64     `json:"certificate"`
	Domains     []string  `json:"domains"`
	Serial      string    `json:"serial"`
	Fingerprint string    `json:"fingerprint"` // SHA-256 of the leaf, in hex
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	// The PEM files, as certbot names them.
	Cert      string `json:"cert"`
	Chain     string `json:"chain"`
	Fullchain string `json:"fullchain"`
	PrivKey   string `json:"privkey"`
	// Previous holds the refs the receiver answered for earlier versions,
	// the newest first.
	Previous []string `json:"previous"`
}

// reply is what a receiver may answer: a ref for what it holds now, and
// the previous refs it removed.
type reply struct {
	Ref     string   `json:"ref"`
	Removed []string `json:"removed"`
}

// deployer posts new versions to a receiver that installs them. Its URL
// must be https: the requests carry the private key.
type deployer struct{ e *endpoint }

var _ contracts.Target = (*deployer)(nil)

func newDeployer(_ context.Context, in providers.Instance) (any, error) {
	e, err := newEndpoint(in)
	if err != nil {
		return nil, err
	}
	if e.url.Scheme != "https" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.WebhookErrorHttps})
	}
	return &deployer{e: e}, nil
}

func (d *deployer) Close() error {
	d.e.client.CloseIdleConnections()
	return nil
}

func (d *deployer) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("webhook: no certificate to deploy"))
	}
	leaf := req.Chain[0]
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}
	fp := sha256.Sum256(leaf.Raw)
	body, err := d.e.body(Deployment{
		Event: DeployEvent, Certificate: req.CertificateID, Domains: req.Domains,
		Serial: leaf.SerialNumber.Text(16), Fingerprint: hex.EncodeToString(fp[:]),
		NotBefore: leaf.NotBefore.UTC(), NotAfter: leaf.NotAfter.UTC(),
		Cert:      string(cryptox.EncodeCertificates(leaf)),
		Chain:     string(cryptox.EncodeCertificates(req.Chain[1:]...)),
		Fullchain: string(cryptox.EncodeCertificates(req.Chain...)),
		PrivKey:   string(key),
		Previous:  append([]string{}, req.Previous...),
	}, nil)
	if err != nil {
		return none, err
	}
	log(contracts.LogLine{Level: contracts.Info, Key: i18n.WebhookLogPost, Params: map[string]any{"url": d.e.shown()}})
	data, err := d.e.post(ctx, DeployEvent, body)
	if err != nil {
		return none, err
	}
	// The answer is optional; one that is not JSON says nothing.
	var r reply
	if json.Unmarshal(data, &r) != nil {
		r = reply{}
	}
	if r.Ref != "" {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.WebhookLogRef, Params: map[string]any{"ref": r.Ref}})
	} else {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.WebhookLogAccepted})
	}
	return contracts.DeployResult{Ref: r.Ref, Removed: r.Removed}, nil
}
