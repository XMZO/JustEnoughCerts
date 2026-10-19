package namecheap

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // G505: the API names keys by the SHA-1 of their modulus
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
)

// How an issuance follows the vendor: it looks at the integration's status
// every poll, and lists the certificates again when the status changed, or
// at least every relook.
const (
	poll   = 5 * time.Minute
	relook = 6 * time.Hour
)

func sslManagerType() providers.Type {
	return providers.Type{
		ID: "namecheap.ssl_manager", Kind: providers.Source,
		Name: i18n.NamecheapSslManagerName, Description: i18n.NamecheapSslManagerDescription,
		Fields: []providers.Field{
			{Key: "token", Label: i18n.NamecheapFieldToken, Help: i18n.NamecheapFieldTokenHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "url", Label: i18n.NamecheapFieldUrl, Help: i18n.NamecheapFieldUrlHelp, Kind: providers.URL, Default: defaultURL, Advanced: true},
		},
		NeedsSolver: true, Challenges: []string{providers.ChallengeHTTP, providers.ChallengeRecord}, Testable: true, Experimental: true,
	}
}

// sslManager is a registration with Namecheap's SSL Manager API at work.
type sslManager struct {
	m  *Module
	in providers.Instance
}

var (
	_ contracts.Source = (*sslManager)(nil)
	_ contracts.Tester = (*sslManager)(nil)
)

func (m *Module) factory(_ context.Context, in providers.Instance) (any, error) {
	return &sslManager{m: m, in: in}, nil
}

// Close has nothing to release.
func (*sslManager) Close() error { return nil }

// Test registers, if that is still to do, and lists the certificates the
// registration sees. Registering uses up the one-time token, as the first
// issuance would.
func (s *sslManager) Test(ctx context.Context) (contracts.Message, error) {
	sess, err := s.m.session(ctx, s.in, func(contracts.LogLine) {})
	if err != nil {
		return contracts.Message{}, err
	}
	certs, err := sess.certificates(ctx, sess.access, "")
	if err != nil {
		return contracts.Message{}, explain(err)
	}
	if len(certs) == 0 {
		// Namecheap shows the certificate once it finds the markers.
		return contracts.Message{Key: i18n.NamecheapTestNone}, nil
	}
	return contracts.Message{Key: i18n.NamecheapTestOk, Params: map[string]any{"count": len(certs)}}, nil
}

// order is what an issuance keeps between its attempts.
type order struct {
	// Cert is the vendor's certificate the issuance fills, once it sent
	// the CSR for it.
	Cert string `json:"cert,omitempty"`
	// Files are on the website, and Records in the DNS, to remove once the
	// certificate is issued; Marked lists the challenges reported in
	// place.
	Files   []placed       `json:"files,omitempty"`
	Records []placedRecord `json:"records,omitempty"`
	Marked  []string       `json:"marked,omitempty"`
	// Fingerprint is the integration's status at the last look, at Looked.
	Fingerprint string `json:"fingerprint,omitempty"`
	Looked      int64  `json:"looked,omitempty"`
	// Seen is what the last look found, logged once.
	Seen string `json:"seen,omitempty"`
	// Markers tells that the markers of SSL Manager are on the website.
	Markers bool `json:"markers,omitempty"`
}

type placed struct {
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

// placedRecord is a record in the DNS, with the solver's ref for removing
// it.
type placedRecord struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	Ref   string `json:"ref,omitempty"`
}

func (p placedRecord) record() contracts.Record {
	return contracts.Record{Name: p.Name, Type: p.Type, Value: p.Value}
}

// wait ends an attempt that has to wait for the vendor.
func (o *order) wait() error {
	state, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return &contracts.Wait{State: state, After: poll}
}

// keep marks err, the failure of an attempt, with the order as far as it
// got, so that the next attempt goes on from there, looking at the vendor
// again.
func (o *order) keep(err error) error {
	o.Fingerprint = ""
	state, merr := json.Marshal(o)
	if merr != nil {
		return err
	}
	return contracts.KeepState(err, state)
}

// note logs line once for what the look found, which seen names.
func (o *order) note(log func(contracts.LogLine), seen string, line contracts.LogLine) {
	if o.Seen != seen {
		o.Seen = seen
		log(line)
	}
}

// Issue follows the vendor's certificate for the primary domain, as the
// official client does: it sends the CSR of req.Key when the certificate
// awaits one, puts the files of the domain validation in place through
// req.Files, or its records through req.Records, when it awaits them, and
// downloads it once issued. Between the steps it waits, for as long as the
// vendor takes.
func (s *sslManager) Issue(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	switch {
	case len(req.Domains) == 0:
		return nil, retry.Permanent(errors.New("namecheap: no domain to issue for"))
	case req.Files == nil && req.Records == nil:
		return nil, retry.Permanent(errors.New("namecheap: the domain validation needs a solver over HTTP or of DNS records"))
	}
	pub, ok := req.Key.Public().(*rsa.PublicKey)
	if !ok {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.NamecheapErrorKeyType})
	}
	var o order
	if len(req.State) > 0 && json.Unmarshal(req.State, &o) != nil {
		o = order{}
	}
	sess, err := s.m.session(ctx, s.in, log)
	if err != nil {
		return nil, err
	}
	fingerprint, err := sess.status(ctx)
	var refused *apiError
	switch {
	case errors.As(err, &refused) && refused.Status == http.StatusNotFound:
		fingerprint = "" // no status to go by: look every time
	case err != nil:
		return nil, explain(err)
	}
	now := s.m.now().Unix()
	if fingerprint != "" && fingerprint == o.Fingerprint && now-o.Looked < int64(relook/time.Second) {
		return nil, o.wait() // nothing changed at the vendor
	}
	o.Fingerprint, o.Looked = fingerprint, now

	domain := req.Domains[0]
	for range 3 { // a CSR moves the certificate on to its validation at once
		cert, err := s.current(ctx, sess, domain, &o)
		if err != nil {
			return nil, o.keep(err)
		}
		if cert == nil {
			if req.Files == nil {
				o.note(log, "unmarked", contracts.LogLine{Level: contracts.Warn, Step: contracts.StepOrder, Key: i18n.NamecheapLogMarkNeedsFiles,
					Params: map[string]any{"url": "http://" + domain + markerPath}})
				return nil, o.wait()
			}
			s.markEarly(ctx, req.Files, domain, &o, log)
			o.note(log, "none", contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.NamecheapLogNone, Params: map[string]any{"domain": domain}})
			return nil, o.wait()
		}
		switch cert.State {
		case stateAwaitingCSR:
			if err := s.request(ctx, sess, cert, req, domain, &o, log); err != nil {
				return nil, o.keep(err)
			}
			continue
		case stateAwaitingChallenges:
			if o.Cert != cert.ID {
				o.note(log, "other:"+cert.ID, contracts.LogLine{Level: contracts.Warn, Step: contracts.StepOrder, Key: i18n.NamecheapLogNotOurs})
				return nil, o.wait()
			}
			if err := s.validate(ctx, sess, cert, req, domain, &o, log); err != nil {
				return nil, o.keep(err)
			}
			return nil, o.wait()
		case stateIssued:
			chain, err := s.download(ctx, sess, cert, req, pub, &o, log)
			if errors.Is(err, errOtherKey) {
				o.note(log, "issued:"+cert.ID, contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.NamecheapLogNext, Params: map[string]any{"domain": domain}})
				return nil, o.wait()
			}
			if err != nil {
				return nil, o.keep(err)
			}
			return chain, nil
		// The certificate is spent: a later issuance looks for another.
		case stateRevoked:
			return nil, contracts.Restart(retry.Permanent(&contracts.Problem{Key: i18n.NamecheapErrorRevoked, Params: map[string]any{"domain": domain}}))
		case stateFailed:
			return nil, contracts.Restart(retry.Permanent(&contracts.Problem{Key: i18n.NamecheapErrorOrderFailed, Params: map[string]any{"domain": domain}}))
		default:
			o.note(log, "state:"+cert.State, contracts.LogLine{Level: contracts.Info, Step: contracts.StepValidate, Key: i18n.NamecheapLogState, Params: map[string]any{"state": cert.State}})
			return nil, o.wait()
		}
	}
	return nil, o.wait()
}

// current returns the vendor's certificate the issuance works on: the one
// it sent the CSR for, or else the newest of domain awaiting a CSR, or
// else the newest of domain. It is nil when domain has none.
func (s *sslManager) current(ctx context.Context, sess *session, domain string, o *order) (*certificate, error) {
	certs, err := sess.certificates(ctx, sess.access, domain)
	if err != nil {
		return nil, explain(err)
	}
	if o.Cert != "" {
		if i := slices.IndexFunc(certs, func(c certificate) bool { return c.ID == o.Cert }); i >= 0 {
			return &certs[i], nil
		}
		*o = order{Fingerprint: o.Fingerprint, Looked: o.Looked} // gone: start over
	}
	if i := slices.IndexFunc(certs, func(c certificate) bool { return c.State == stateAwaitingCSR }); i >= 0 {
		return &certs[i], nil
	}
	if len(certs) == 0 {
		return nil, nil
	}
	return &certs[0], nil
}

// request sends the CSR of req.Key for cert.
func (s *sslManager) request(ctx context.Context, sess *session, cert *certificate, req contracts.IssueRequest, domain string, o *order, log func(contracts.LogLine)) error {
	csr, err := cryptox.CSR(req.Key, []string{domain})
	if err != nil {
		return retry.Permanent(err)
	}
	log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.NamecheapLogRequest, Params: map[string]any{"domain": domain}})
	if _, err := sess.request(ctx, sess.access, cert.ID, csr); err != nil {
		return explain(err)
	}
	*o = order{Cert: cert.ID, Fingerprint: o.Fingerprint, Looked: o.Looked}
	return nil
}

// validate answers cert's challenges with the certificate's solver: over
// HTTP, it puts the files on the website, with the marks the official
// client leaves that it manages the domain, and checks that the website
// serves them; of DNS records, it adds the records and waits for the
// domain's authoritative servers to answer them. It reports each challenge
// in place once answered.
func (s *sslManager) validate(ctx context.Context, sess *session, cert *certificate, req contracts.IssueRequest, domain string, o *order, log func(contracts.LogLine)) error {
	challenges, err := sess.challenges(ctx, sess.access, cert.ID)
	if err != nil {
		return explain(err)
	}
	var methods []string
	var pending unseen
	answered := false
	for _, ch := range challenges {
		if ch.Method == methodDNS && req.Records != nil {
			if err := s.answerRecords(ctx, sess, ch, req.Records, cmp.Or(ch.Domain, domain), o, log, &pending); err != nil {
				return err
			}
			answered = true
			continue
		}
		if ch.Method != methodHTTP || req.Files == nil {
			methods = append(methods, ch.Method)
			continue
		}
		name := cmp.Or(ch.Domain, domain)
		for _, f := range ch.files() {
			file := contracts.File{Domain: name, Path: f.FilePath, Content: []byte(f.Content)}
			log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepChallenge, Key: i18n.NamecheapLogPutFile, Params: map[string]any{"domain": name, "path": f.FilePath}})
			if err := req.Files.PutFile(ctx, file); err != nil {
				return putFailed(err, name)
			}
			if !slices.Contains(o.Files, placed{Domain: name, Path: f.FilePath}) {
				o.Files = append(o.Files, placed{Domain: name, Path: f.FilePath})
			}
			s.check(ctx, file, i18n.NamecheapLogNotServed, log)
		}
		if _, err := s.mark(ctx, req.Files, name); err != nil {
			log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepChallenge, Key: i18n.NamecheapLogMarkFailed, Params: map[string]any{"domain": name}, Detail: err.Error()})
		}
		if !slices.Contains(o.Marked, ch.ID) {
			if err := sess.installed(ctx, sess.access, ch.ID); err != nil {
				return explain(err)
			}
			o.Marked = append(o.Marked, ch.ID)
		}
		answered = true
	}
	if !answered {
		return retry.Permanent(&contracts.Problem{Key: i18n.NamecheapErrorMethods, Params: map[string]any{"methods": strings.Join(methods, ", ")}})
	}
	if len(pending.names) > 0 {
		line := contracts.LogLine{Level: contracts.Info, Step: contracts.StepValidate, Key: i18n.NamecheapLogRecordWait, Params: map[string]any{"names": strings.Join(pending.names, ", ")}}
		if pending.err != nil {
			line.Detail = pending.err.Error()
		}
		o.note(log, "records:"+strings.Join(pending.names, " "), line)
		o.Fingerprint = "" // the vendor's status stays as it is: look again all the same
		return nil
	}
	o.note(log, "validating:"+cert.ID, contracts.LogLine{Level: contracts.Info, Step: contracts.StepValidate, Key: i18n.NamecheapLogValidate})
	return nil
}

// unseen gathers the records the authoritative servers do not answer yet,
// by name, and the last failure to ask them.
type unseen struct {
	names []string
	err   error
}

// answerRecords adds the records of a DNS challenge through records,
// unless an earlier attempt did, and reports the challenge in place once
// the authoritative servers answer each record; the records they do not
// answer yet go into pending.
func (s *sslManager) answerRecords(ctx context.Context, sess *session, ch challenge, records contracts.RecordSolver, domain string, o *order, log func(contracts.LogLine), pending *unseen) error {
	if slices.Contains(o.Marked, ch.ID) {
		return nil
	}
	list := ch.records()
	if len(list) == 0 {
		return retry.Permanent(&contracts.Problem{Key: i18n.NamecheapErrorMethods, Params: map[string]any{"methods": ch.Method}})
	}
	ready := true
	for _, r := range list {
		p := placedRecord{Name: dnscheck.RecordName(r.RecordName, domain), Type: strings.ToUpper(strings.TrimSpace(r.RecordType)), Value: r.RecordValue}
		if p.Name == "" || (p.Type != "TXT" && p.Type != "CNAME") || p.Value == "" {
			return retry.Permanent(&contracts.Problem{Key: i18n.NamecheapErrorRecord, Params: map[string]any{"type": r.RecordType, "name": r.RecordName}})
		}
		if !slices.ContainsFunc(o.Records, func(q placedRecord) bool { return q.record() == p.record() }) {
			log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepChallenge, Key: i18n.NamecheapLogAddRecord,
				Params: map[string]any{"name": p.Name, "type": p.Type, "value": p.Value}})
			ref, err := records.AddRecord(ctx, p.record())
			if err != nil {
				return addFailed(err, p.Name)
			}
			p.Ref = ref
			o.Records = append(o.Records, p)
		}
		ok, err := s.m.visible(ctx, p.Name, p.Type, p.Value)
		if !ok {
			ready = false
			pending.names = append(pending.names, p.Name)
			if err != nil {
				pending.err = err
			}
		}
	}
	if !ready {
		return nil
	}
	if err := sess.installed(ctx, sess.access, ch.ID); err != nil {
		return explain(err)
	}
	o.Marked = append(o.Marked, ch.ID)
	return nil
}

// addFailed explains a failure to add a record: the solver's own reason,
// if it gave one.
func addFailed(err error, name string) error {
	var problem *contracts.Problem
	if errors.As(err, &problem) {
		return err
	}
	return &contracts.Problem{Key: i18n.NamecheapErrorAddRecord, Params: map[string]any{"name": name}, Err: err}
}

// putFailed explains a failure to put a file on the website: the solver's
// own reason, if it gave one.
func putFailed(err error, domain string) error {
	var problem *contracts.Problem
	if errors.As(err, &problem) {
		return err
	}
	return &contracts.Problem{Key: i18n.NamecheapErrorPutFile, Params: map[string]any{"domain": domain}, Err: err}
}

// markerPath is where Namecheap looks for SSL Manager on a website.
const markerPath = "/.well-known/ssl-manager/installed.txt"

// mark puts the files the official client leaves on a website it manages,
// which Namecheap looks for, and returns the one at markerPath.
func (s *sslManager) mark(ctx context.Context, files contracts.FileSolver, domain string) (contracts.File, error) {
	now := s.m.now()
	data, _ := json.Marshal(map[string]any{"domain": domain, "manager_conflicts": []string{}, "timestamp": now.Format(time.RFC3339)})
	marker := contracts.File{Domain: domain, Path: markerPath, Content: []byte(strconv.FormatInt(now.Unix(), 10) + " " + domain)}
	for _, f := range []contracts.File{
		marker,
		{Domain: domain, Path: "/.well-known/ssl-manager/installed.b64.json", Content: []byte(base64.StdEncoding.EncodeToString(data))},
	} {
		if err := files.PutFile(ctx, f); err != nil {
			return marker, err
		}
	}
	return marker, nil
}

// markEarly puts the markers on the website before Namecheap shows a
// certificate: it shows one to the integration only once it finds them, as
// the official client puts them when installed. The certificate's way of
// putting files does it, so the website may be anywhere. A failure is
// logged and tried again at the next look.
func (s *sslManager) markEarly(ctx context.Context, files contracts.FileSolver, domain string, o *order, log func(contracts.LogLine)) {
	if o.Markers {
		return
	}
	marker, err := s.mark(ctx, files, domain)
	if err != nil {
		log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepOrder, Key: i18n.NamecheapLogMarkFailed, Params: map[string]any{"domain": domain}, Detail: err.Error()})
		return
	}
	o.Markers = true
	address := "http://" + domain + markerPath
	log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepOrder, Key: i18n.NamecheapLogMarked, Params: map[string]any{"url": address}})
	s.check(ctx, marker, i18n.NamecheapLogMarkerNotServed, log)
}

// check fetches a file from the website as the CA, or Namecheap, will, and
// logs notServed when the website does not serve it. It proves nothing when
// it passes, since JEC may see the website differently.
func (s *sslManager) check(ctx context.Context, f contracts.File, notServed i18n.Key, log func(contracts.LogLine)) {
	address := "http://" + f.Domain + f.Path
	got, err := s.m.fetch(ctx, address)
	switch {
	case err != nil:
		log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepChallenge, Key: notServed, Params: map[string]any{"url": address}, Detail: err.Error()})
	case !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(f.Content)):
		log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepChallenge, Key: notServed, Params: map[string]any{"url": address}, Detail: snippet(got)})
	}
}

// fetch GETs address from the public internet, following redirects as a CA
// may.
func (m *Module) fetch(ctx context.Context, address string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.web.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, snippet(data))
	}
	return data, nil
}

// errOtherKey: the vendor's certificate is for another key than the
// issuance's, such as the one before, which this issuance is to replace.
var errOtherKey = errors.New("namecheap: the certificate is for another key")

// download returns cert with its chain, if it is for the issuance's key,
// once the files of its validation are off the website. It tells Namecheap
// the certificate is installed, as the official client does once it put it
// in place.
func (s *sslManager) download(ctx context.Context, sess *session, cert *certificate, req contracts.IssueRequest, pub *rsa.PublicKey, o *order, log func(contracts.LogLine)) ([]*x509.Certificate, error) {
	mine := cert.ID == o.Cert
	if !mine && cert.ModulusSHA1 != "" && !strings.EqualFold(cert.ModulusSHA1, modulusSHA1(pub)) {
		return nil, errOtherKey
	}
	data, err := sess.pem(ctx, sess.access, cert.ID)
	if err != nil {
		return nil, explain(err)
	}
	certs, err := cryptox.ParseCertificates(data)
	if err != nil {
		return nil, fmt.Errorf("namecheap: the certificate: %w", err)
	}
	b, err := cryptox.NewBundle(certs, req.Key)
	switch {
	case errors.Is(err, cryptox.ErrKeyMismatch) && !mine:
		return nil, errOtherKey
	case errors.Is(err, cryptox.ErrKeyMismatch):
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.NamecheapErrorKeyMismatch, Err: err})
	case err != nil:
		return nil, fmt.Errorf("namecheap: the certificate: %w", err)
	}
	for _, f := range o.Files {
		if req.Files == nil { // put by a solver no longer chosen
			log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepCleanup, Key: i18n.NamecheapLogRemoveFailed, Params: map[string]any{"domain": f.Domain, "path": f.Path}})
			continue
		}
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepCleanup, Key: i18n.NamecheapLogRemoveFile, Params: map[string]any{"domain": f.Domain, "path": f.Path}})
		if err := req.Files.RemoveFile(ctx, contracts.File{Domain: f.Domain, Path: f.Path}); err != nil {
			log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepCleanup, Key: i18n.NamecheapLogRemoveFailed, Params: map[string]any{"domain": f.Domain, "path": f.Path}, Detail: err.Error()})
		}
	}
	o.Files = nil
	for _, r := range o.Records {
		if r.Ref == "" || req.Records == nil { // added by people, or by a solver no longer chosen
			log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepCleanup, Key: i18n.NamecheapLogRecordLeft, Params: map[string]any{"name": r.Name, "type": r.Type}})
			continue
		}
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepCleanup, Key: i18n.NamecheapLogRemoveRecord, Params: map[string]any{"name": r.Name, "type": r.Type}})
		if err := req.Records.RemoveRecord(ctx, r.record(), r.Ref); err != nil {
			log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepCleanup, Key: i18n.NamecheapLogRemoveRecordFailed, Params: map[string]any{"name": r.Name, "type": r.Type}, Detail: err.Error()})
		}
	}
	o.Records = nil
	log(contracts.LogLine{Level: contracts.OK, Step: contracts.StepDownload, Key: i18n.NamecheapLogIssued})
	if err := sess.confirm(ctx, sess.access, cert.ID, "jec-"+b.Leaf.SerialNumber.Text(16)); err != nil {
		log(contracts.LogLine{Level: contracts.Warn, Step: contracts.StepDownload, Key: i18n.NamecheapLogConfirmFailed, Detail: err.Error()})
	}
	return append([]*x509.Certificate{b.Leaf}, b.Chain...), nil
}

// modulusSHA1 names an RSA key as the API does: the SHA-1 of its modulus,
// in hexadecimal.
func modulusSHA1(pub *rsa.PublicKey) string {
	sum := sha1.Sum(pub.N.Bytes()) //nolint:gosec // G401: a name, not a protection
	return hex.EncodeToString(sum[:])
}

// explain turns a failed call into what the issuance shows: whether
// retrying can help, and for how long the API asked to wait.
func explain(err error) error {
	var refused *apiError
	if !errors.As(err, &refused) {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return &contracts.Problem{Key: i18n.NamecheapErrorConnect, Err: err}
	}
	problem := &contracts.Problem{Key: i18n.NamecheapErrorApi, Params: map[string]any{"status": refused.Status}, Err: err}
	switch {
	case refused.Status == http.StatusUnauthorized, refused.Status == http.StatusForbidden:
		return retry.Permanent(&contracts.Problem{Key: i18n.NamecheapErrorAuth, Err: err})
	case refused.Status == http.StatusTooManyRequests && refused.RetryAfter > 0:
		return retry.After(problem, refused.RetryAfter)
	case refused.Status == http.StatusTooManyRequests, refused.Status == http.StatusRequestTimeout, refused.Status >= 500:
		return problem
	}
	return retry.Permanent(problem)
}
