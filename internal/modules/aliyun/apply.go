package aliyun

import (
	"bytes"
	"cmp"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/aliyunapi"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
)

// Certificates are applied for through the resource plans of the
// Certificate Management Service, by default the free personal test ones
// (DigiCert DV, three months, twenty a year, China site only): JEC sends
// the CSR of the issuance's key, so the key never leaves JEC, puts the
// validation file or record in place through the certificate's solver,
// and waits for the CA, which looks for the validation by itself
// (CreateCertificateForPackageRequest, DescribeCertificateState). An
// order the CA refused is deleted, which gives its quota back, and the
// next issuance applies anew.

const (
	// freeProduct is the product code of the free personal test
	// certificates.
	freeProduct = "digicert-free-1-free"
	// applyPoll is how often an order is looked at while the CA
	// validates and reviews it.
	applyPoll = 2 * time.Minute
)

func applyType() providers.Type {
	return providers.Type{
		ID: "aliyun.apply", Kind: providers.Source, Name: i18n.AliyunApplyName, Description: i18n.AliyunApplyDescription,
		Tags: []string{"china"}, Testable: true,
		NeedsSolver: true, Challenges: []string{providers.ChallengeHTTP, providers.ChallengeRecord},
		Fields: []providers.Field{
			{Key: "access_key_id", Label: i18n.AliyunFieldAccessKeyId, Kind: providers.Text, Required: true},
			{Key: "access_key_secret", Label: i18n.AliyunFieldAccessKeySecret, Help: i18n.AliyunFieldApplySecretHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "product", Label: i18n.AliyunFieldApplyProduct, Help: i18n.AliyunFieldApplyProductHelp, Kind: providers.Text, Default: freeProduct, Advanced: true},
			{Key: "site", Label: i18n.AliyunFieldSite, Kind: providers.Select, Default: casChina, Advanced: true, Options: []providers.Option{
				{Value: casChina, Label: i18n.AliyunSiteChina},
				{Value: casIntl, Label: i18n.AliyunSiteIntl},
			}},
			{Key: "username", Label: i18n.AliyunFieldApplyUsername, Help: i18n.AliyunFieldApplyContactHelp, Kind: providers.Text, Advanced: true},
			{Key: "phone", Label: i18n.AliyunFieldApplyPhone, Kind: providers.Text, Advanced: true},
			{Key: "email", Label: i18n.AliyunFieldApplyEmail, Kind: providers.Text, Advanced: true},
		},
	}
}

// applier applies for certificates through the resource plans of an
// account.
type applier struct {
	api      rpc
	endpoint string
	product  string
	contact  map[string]string // Username, Phone and Email, as set
	// web fetches the validation file as the CA does, and visible tells
	// whether the authoritative servers answer a record; tests replace
	// them.
	web     *http.Client
	visible func(ctx context.Context, name, typ, value string) (bool, error)
}

var (
	_ contracts.Source = (*applier)(nil)
	_ contracts.Tester = (*applier)(nil)
)

func (m *Module) applyFactory(_ context.Context, in providers.Instance) (any, error) {
	endpoint := casEndpoint(in.Config["site"])
	api, err := m.rpc(endpoint, casVersion, strings.TrimSpace(in.Value("access_key_id")), strings.TrimSpace(in.Value("access_key_secret")))
	if err != nil {
		return nil, retry.Permanent(err)
	}
	contact := map[string]string{}
	for key, param := range map[string]string{"username": "Username", "phone": "Phone", "email": "Email"} {
		if v := strings.TrimSpace(in.Config[key]); v != "" {
			contact[param] = v
		}
	}
	return &applier{
		api: api, endpoint: endpoint, product: cmp.Or(strings.TrimSpace(in.Config["product"]), freeProduct),
		contact: contact, web: m.web, visible: m.visible,
	}, nil
}

// Close has nothing to release.
func (*applier) Close() error { return nil }

// Test tells how many certificates the resource plans of the product
// hold, and how many were applied for.
func (a *applier) Test(ctx context.Context) (contracts.Message, error) {
	var out struct {
		TotalCount int `json:"TotalCount"`
		UsedCount  int `json:"UsedCount"`
	}
	if err := a.api.do(ctx, "DescribePackageState", map[string]string{"ProductCode": a.product}, nil, &out); err != nil {
		return contracts.Message{}, explainRPC(err, a.endpoint, noneAbsent)
	}
	return contracts.Message{Key: i18n.AliyunTestApplyOk, Params: map[string]any{"product": a.product, "total": out.TotalCount, "used": out.UsedCount}}, nil
}

// applied is what an issuance keeps between its attempts.
type applied struct {
	// Order is the ID of the order, once applied for.
	Order string `json:"order,omitempty"`
	// Files are on the website, and Records in the DNS, to remove once the
	// certificate is issued.
	Files   []contracts.File `json:"files,omitempty"`
	Records []placedRecord   `json:"records,omitempty"`
	// Seen is what the last look found, logged once.
	Seen string `json:"seen,omitempty"`
}

// placedRecord is a record in the DNS, with the solver's ref for removing
// it.
type placedRecord struct {
	contracts.Record
	Ref string `json:"ref,omitempty"`
}

// wait ends an attempt that has to wait for the CA.
func (o *applied) wait() error {
	state, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return &contracts.Wait{State: state, After: applyPoll}
}

// keep marks err, the failure of an attempt, with the order as far as it
// got, so that the next attempt goes on with it rather than apply again.
func (o *applied) keep(err error) error {
	state, merr := json.Marshal(o)
	if merr != nil {
		return err
	}
	return contracts.KeepState(err, state)
}

// note logs line once for what the look found, which seen names.
func (o *applied) note(log func(contracts.LogLine), seen string, line contracts.LogLine) {
	if o.Seen != seen {
		o.Seen = seen
		log(line)
	}
}

// orderState is the state of an order, as DescribeCertificateState
// answers it.
type orderState struct {
	Type         string `json:"Type"`
	ValidateType string `json:"ValidateType"`
	Domain       string `json:"Domain"`
	URI          string `json:"Uri"`
	Content      string `json:"Content"`
	RecordDomain string `json:"RecordDomain"`
	RecordType   string `json:"RecordType"`
	RecordValue  string `json:"RecordValue"`
	Certificate  string `json:"Certificate"`
}

// Issue applies for the certificate unless an earlier attempt did, puts
// the validation in place when the order awaits it, and downloads the
// certificate once issued. Between the steps it waits.
func (a *applier) Issue(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	switch {
	case len(req.Domains) == 0:
		return nil, retry.Permanent(errors.New("aliyun: no domain to issue for"))
	case req.Files == nil && req.Records == nil:
		return nil, retry.Permanent(errors.New("aliyun: the domain validation needs a solver over HTTP or of DNS records"))
	}
	var o applied
	if len(req.State) > 0 && json.Unmarshal(req.State, &o) != nil {
		o = applied{}
	}
	if o.Order == "" {
		if err := a.apply(ctx, req, &o, log); err != nil {
			return nil, err
		}
	}
	var st orderState
	if err := a.api.do(ctx, "DescribeCertificateState", map[string]string{"OrderId": o.Order}, nil, &st); err != nil {
		code, _ := aliyunapi.ErrorCode(err)
		if missing(strings.ToLower(code)) {
			// The order is gone, as one deleted in the console: apply anew.
			return nil, contracts.Restart(&contracts.Problem{Key: i18n.AliyunErrorApplyGone, Params: map[string]any{"order": o.Order}, Err: err})
		}
		return nil, o.keep(explainRPC(err, a.endpoint, noneAbsent))
	}
	switch st.Type {
	case "domain_verify":
		if err := a.validate(ctx, req, st, &o, log); err != nil {
			return nil, o.keep(err)
		}
		return nil, o.wait()
	case "certificate":
		chain, err := a.download(ctx, req, st, &o, log)
		if err != nil {
			return nil, o.keep(err)
		}
		return chain, nil
	case "verify_fail":
		a.refused(ctx, req, &o, log)
		return nil, contracts.Restart(retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorApplyRefused, Params: map[string]any{"order": o.Order}}))
	case "process":
		o.note(log, "process", contracts.LogLine{Level: contracts.Info, Step: contracts.StepValidate, Key: i18n.AliyunLogApplyReview})
	default:
		o.note(log, "state:"+st.Type, contracts.LogLine{Level: contracts.Info, Step: contracts.StepValidate, Key: i18n.AliyunLogApplyState, Params: map[string]any{"state": st.Type}})
	}
	return nil, o.wait()
}

// apply sends the CSR of req.Key for the domains, with the validation the
// solver does: of DNS records or over HTTP.
func (a *applier) apply(ctx context.Context, req contracts.IssueRequest, o *applied, log func(contracts.LogLine)) error {
	csr, err := cryptox.CSR(req.Key, req.Domains)
	if err != nil {
		return retry.Permanent(err)
	}
	validate := "FILE"
	if req.Records != nil {
		validate = "DNS"
	}
	query := map[string]string{"ProductCode": a.product, "ValidateType": validate, "Domain": strings.Join(req.Domains, ",")}
	for k, v := range a.contact {
		query[k] = v
	}
	log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.AliyunLogApplyOrder,
		Params: map[string]any{"domains": strings.Join(req.Domains, ", "), "product": a.product}})
	var out struct {
		OrderID anyID `json:"OrderId"`
	}
	if err := a.api.do(ctx, "CreateCertificateForPackageRequest", query, map[string]string{"Csr": string(csr)}, &out); err != nil {
		return explainRPC(err, a.endpoint, noneAbsent)
	}
	if out.OrderID == "" {
		return errors.New("aliyun: CreateCertificateForPackageRequest answered no OrderId")
	}
	*o = applied{Order: string(out.OrderID)}
	return nil
}

// validate puts the validation of the order in place, unless an earlier
// attempt did: the record through req.Records, waiting for the
// authoritative servers to answer it, or the file through req.Files,
// checking that the website serves it. The CA then looks for it.
func (a *applier) validate(ctx context.Context, req contracts.IssueRequest, st orderState, o *applied, log func(contracts.LogLine)) error {
	domain := req.Domains[0]
	switch {
	case st.ValidateType == "DNS" && req.Records != nil:
		r := contracts.Record{Name: dnscheck.RecordName(st.RecordDomain, domain), Type: strings.ToUpper(strings.TrimSpace(st.RecordType)), Value: st.RecordValue}
		if r.Name == "" || (r.Type != "TXT" && r.Type != "CNAME") || r.Value == "" {
			return retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorApplyRecord, Params: map[string]any{"type": st.RecordType, "name": st.RecordDomain}})
		}
		if !slices.ContainsFunc(o.Records, func(p placedRecord) bool { return p.Record == r }) {
			log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepChallenge, Key: i18n.AliyunLogApplyRecord,
				Params: map[string]any{"name": r.Name, "type": r.Type, "value": r.Value}})
			ref, err := req.Records.AddRecord(ctx, r)
			if err != nil {
				return addFailed(err, r.Name)
			}
			o.Records = append(o.Records, placedRecord{Record: r, Ref: ref})
		}
		ok, err := a.visible(ctx, r.Name, r.Type, r.Value)
		if !ok {
			line := contracts.LogLine{Level: contracts.Info, Step: contracts.StepValidate, Key: i18n.AliyunLogApplyRecordWait, Params: map[string]any{"name": r.Name}}
			if err != nil {
				line.Detail = err.Error()
			}
			o.note(log, "record:"+r.Name+":"+r.Value, line)
			return nil
		}
	case st.ValidateType == "FILE" && req.Files != nil:
		f := contracts.File{Domain: cmp.Or(st.Domain, domain), Path: st.URI, Content: []byte(st.Content)}
		if !f.Valid() {
			return retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorApplyFile, Params: map[string]any{"path": st.URI}})
		}
		if !slices.ContainsFunc(o.Files, func(p contracts.File) bool { return p.Domain == f.Domain && p.Path == f.Path }) {
			log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepChallenge, Key: i18n.AliyunLogApplyFile, Params: map[string]any{"domain": f.Domain, "path": f.Path}})
			if err := req.Files.PutFile(ctx, f); err != nil {
				return putFailed(err, f.Domain)
			}
			o.Files = append(o.Files, contracts.File{Domain: f.Domain, Path: f.Path})
			if !a.serves(ctx, f) {
				log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepChallenge, Key: i18n.AliyunLogApplyNotServed, Params: map[string]any{"url": "http://" + f.Domain + f.Path}})
			}
		}
	default:
		// The order was made for another kind of validation than the
		// solver of the certificate does now.
		return contracts.Restart(retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorApplyMethod, Params: map[string]any{"method": st.ValidateType}}))
	}
	o.note(log, "validating", contracts.LogLine{Level: contracts.Info, Step: contracts.StepValidate, Key: i18n.AliyunLogApplyValidate})
	return nil
}

// serves tells whether the website serves f, as the CA fetches it: over
// plain HTTP, without following redirects.
func (a *applier) serves(ctx context.Context, f contracts.File) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+f.Domain+f.Path, nil)
	if err != nil {
		return false
	}
	resp, err := a.web.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return err == nil && resp.StatusCode == http.StatusOK && bytes.Equal(bytes.TrimSpace(body), bytes.TrimSpace(f.Content))
}

// download takes the issued certificate, which must be of req.Key, and
// removes the validation.
func (a *applier) download(ctx context.Context, req contracts.IssueRequest, st orderState, o *applied, log func(contracts.LogLine)) ([]*x509.Certificate, error) {
	certs, err := cryptox.ParseCertificates([]byte(st.Certificate))
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorApplyBad, Params: map[string]any{"order": o.Order}, Err: err})
	}
	b, err := cryptox.NewBundle(certs, req.Key)
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.AliyunErrorApplyBad, Params: map[string]any{"order": o.Order}, Err: err})
	}
	a.cleanup(ctx, req, o, log)
	log(contracts.LogLine{Level: contracts.OK, Step: contracts.StepDownload, Key: i18n.AliyunLogApplyIssued, Params: map[string]any{"order": o.Order}})
	return append([]*x509.Certificate{b.Leaf}, b.Chain...), nil
}

// refused deletes an order the CA refused, which gives its quota back,
// and removes its validation.
func (a *applier) refused(ctx context.Context, req contracts.IssueRequest, o *applied, log func(contracts.LogLine)) {
	if err := a.api.do(ctx, "DeleteCertificateRequest", map[string]string{"OrderId": o.Order}, nil, nil); err != nil {
		log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepCleanup, Key: i18n.AliyunLogApplyKept, Params: map[string]any{"order": o.Order}, Detail: err.Error()})
	}
	a.cleanup(ctx, req, o, log)
}

// cleanup removes the validation files and records, but those people
// added themselves, which it leaves to them.
func (a *applier) cleanup(ctx context.Context, req contracts.IssueRequest, o *applied, log func(contracts.LogLine)) {
	for _, f := range o.Files {
		params := map[string]any{"domain": f.Domain, "path": f.Path}
		if req.Files == nil {
			log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepCleanup, Key: i18n.AliyunLogApplyFileLeft, Params: params})
			continue
		}
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepCleanup, Key: i18n.AliyunLogApplyRemoveFile, Params: params})
		if err := req.Files.RemoveFile(ctx, f); err != nil {
			log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepCleanup, Key: i18n.AliyunLogApplyFileLeft, Params: params, Detail: err.Error()})
		}
	}
	for _, r := range o.Records {
		params := map[string]any{"name": r.Name, "type": r.Type}
		if r.Ref == "" || req.Records == nil {
			log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepCleanup, Key: i18n.AliyunLogApplyRecordLeft, Params: params})
			continue
		}
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepCleanup, Key: i18n.AliyunLogApplyRemoveRecord, Params: params})
		if err := req.Records.RemoveRecord(ctx, r.Record, r.Ref); err != nil {
			log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepCleanup, Key: i18n.AliyunLogApplyRecordLeft, Params: params, Detail: err.Error()})
		}
	}
	o.Files, o.Records = nil, nil
}

// putFailed and addFailed explain a failure of the solver: its own
// reason, if it gave one.
func putFailed(err error, domain string) error {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return err
	}
	return &contracts.Problem{Key: i18n.AliyunErrorApplyPut, Params: map[string]any{"domain": domain}, Err: err}
}

func addFailed(err error, name string) error {
	var p *contracts.Problem
	if errors.As(err, &p) {
		return err
	}
	return &contracts.Problem{Key: i18n.AliyunErrorApplyAdd, Params: map[string]any{"name": name}, Err: err}
}
