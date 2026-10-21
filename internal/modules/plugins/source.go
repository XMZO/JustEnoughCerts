package plugins

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
)

// Limits of what the validation of a source may ask for, and how long
// looking its records up may take before each attempt.
const (
	maxPlaced   = 100      // files, and records, at a time
	maxFileSize = 16 << 10 // bytes of a file
	maxValue    = 1024     // bytes of a record's value
	lookTimeout = 30 * time.Second
)

// visible tells whether the authoritative servers of its zone answer a
// record; the tests replace it.
var visible = dnscheck.Visible

// issuance is what JEC keeps between the attempts of an issuance through
// a source: the plugin's own state, and what JEC put in place for the
// validation it asked for, to remove once done.
type issuance struct {
	State   string         `json:"state,omitempty"`
	Files   []placedFile   `json:"files,omitempty"`
	Records []placedRecord `json:"records,omitempty"`
}

// placedFile is a file on a web server; Sum, the SHA-256 of its content,
// tells whether the plugin asks for the same file again.
type placedFile struct {
	Domain string `json:"domain"`
	Path   string `json:"path"`
	Sum    string `json:"sum"`
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

func (is *issuance) state() []byte {
	b, _ := json.Marshal(is)
	return b
}

// source issues certificates through a plugin, for the issuance's key.
type source struct{ instance }

// Issue sends the plugin the CSR of req.Key, with what JEC put in place
// for its validation, and takes the chain it answers, removing what JEC
// put in place. Or it waits as the plugin asks, once the files and records
// the plugin's validation asks for are in place, through the certificate's
// solver.
func (s *source) Issue(ctx context.Context, req contracts.IssueRequest) ([]*x509.Certificate, error) {
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	var is issuance
	if len(req.State) > 0 && json.Unmarshal(req.State, &is) != nil {
		is = issuance{}
	}
	csr, err := cryptox.CSR(req.Key, req.Domains)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	r := s.request(actionIssue)
	r.Issue = &issueOrder{Domains: req.Domains, CSR: string(csr), State: is.State, Records: look(ctx, is.Records)}
	for _, f := range is.Files {
		r.Issue.Files = append(r.Issue.Files, fileInPlace{Domain: f.Domain, Path: f.Path})
	}
	a, err := s.do(ctx, actionTimeout, r, log)
	if err != nil {
		return nil, err
	}
	if a.Wait != nil {
		is.State = a.Wait.State
		if err := s.place(ctx, req, &is, a.Wait, log); err != nil {
			return nil, contracts.KeepState(err, is.state())
		}
		return nil, &contracts.Wait{State: is.state(), After: waitFor(a.Wait.After)}
	}
	chain, err := cryptox.ParseCertificates([]byte(a.Chain))
	var b *cryptox.Bundle
	if err == nil {
		b, err = cryptox.NewBundle(chain, req.Key)
	}
	if err == nil && !cryptox.Covers(cryptox.Names(b.Leaf), req.Domains) {
		err = errors.New("the certificate does not cover the domains")
	}
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.PluginsErrorChain, Params: map[string]any{"plugin": s.plugin.id}, Err: err})
	}
	for _, f := range is.Files {
		removeFile(ctx, req.Files, f, contracts.StepCleanup, log)
	}
	for _, p := range is.Records {
		removeRecord(ctx, req.Records, p, contracts.StepCleanup, log)
	}
	return append([]*x509.Certificate{b.Leaf}, b.Chain...), nil
}

// look tells, of each record JEC added, whether the authoritative servers
// of its zone answer it yet.
func look(ctx context.Context, records []placedRecord) []recordInPlace {
	if len(records) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, lookTimeout)
	defer cancel()
	out := make([]recordInPlace, 0, len(records))
	for _, r := range records {
		ok, _ := visible(ctx, r.Name, r.Type, r.Value)
		out = append(out, recordInPlace{Name: r.Name, Type: r.Type, Value: r.Value, Visible: ok})
	}
	return out
}

// place makes what JEC has in place for the plugin's validation what w
// asks for: it puts the files and adds the records it does not have yet,
// through the certificate's solver, then removes those no longer asked
// for. is keeps track of each, even when placing fails halfway.
func (s *source) place(ctx context.Context, req contracts.IssueRequest, is *issuance, w *waitAnswer, log func(contracts.LogLine)) error {
	files, records, err := s.asked(req.Domains, w)
	if err != nil {
		return err
	}
	for _, f := range files {
		sum := sha256.Sum256(f.Content)
		p := placedFile{Domain: f.Domain, Path: f.Path, Sum: hex.EncodeToString(sum[:])}
		i := slices.IndexFunc(is.Files, func(q placedFile) bool { return q.Domain == p.Domain && q.Path == p.Path })
		if i >= 0 && is.Files[i].Sum == p.Sum {
			continue
		}
		if req.Files == nil {
			return retry.Permanent(&contracts.Problem{Key: i18n.PluginsErrorNoFiles, Params: map[string]any{"plugin": s.plugin.id}})
		}
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepChallenge, Key: i18n.PluginsLogPutFile, Params: map[string]any{"domain": p.Domain, "path": p.Path}})
		if err := req.Files.PutFile(ctx, f); err != nil {
			return putFailed(err, p.Domain)
		}
		if i >= 0 {
			is.Files[i] = p
		} else {
			is.Files = append(is.Files, p)
		}
	}
	for _, r := range records {
		if slices.ContainsFunc(is.Records, func(q placedRecord) bool { return q.record() == r }) {
			continue
		}
		if req.Records == nil {
			return retry.Permanent(&contracts.Problem{Key: i18n.PluginsErrorNoRecords, Params: map[string]any{"plugin": s.plugin.id}})
		}
		log(contracts.LogLine{Level: contracts.Info, Step: contracts.StepChallenge, Key: i18n.PluginsLogAddRecord, Params: map[string]any{"name": r.Name, "type": r.Type, "value": r.Value}})
		ref, err := req.Records.AddRecord(ctx, r)
		if err != nil {
			return addFailed(err, r.Name)
		}
		is.Records = append(is.Records, placedRecord{Name: r.Name, Type: r.Type, Value: r.Value, Ref: ref})
	}
	is.Files = slices.DeleteFunc(is.Files, func(p placedFile) bool {
		if slices.ContainsFunc(files, func(f contracts.File) bool { return f.Domain == p.Domain && f.Path == p.Path }) {
			return false
		}
		removeFile(ctx, req.Files, p, contracts.StepChallenge, log)
		return true
	})
	is.Records = slices.DeleteFunc(is.Records, func(p placedRecord) bool {
		if slices.Contains(records, p.record()) {
			return false
		}
		removeRecord(ctx, req.Records, p, contracts.StepChallenge, log)
		return true
	})
	return nil
}

// asked returns the files and records that w asks for, as JEC puts them
// in place: each for a domain of the certificate, a file below
// /.well-known/, a record of TXT or CNAME within the registrable domain.
func (s *source) asked(domains []string, w *waitAnswer) ([]contracts.File, []contracts.Record, error) {
	bad := func(format string, args ...any) error {
		return retry.Permanent(&contracts.Problem{Key: i18n.PluginsErrorValidation, Params: map[string]any{"plugin": s.plugin.id, "problem": fmt.Sprintf(format, args...)}})
	}
	if len(w.Files) > maxPlaced || len(w.Records) > maxPlaced {
		return nil, nil, bad("more than %d files or records", maxPlaced)
	}
	// ours returns d as one of domains, wildcards aside.
	ours := func(d string) (string, bool) {
		d = strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), ".")), "*.")
		return d, d != "" && slices.ContainsFunc(domains, func(x string) bool { return strings.EqualFold(strings.TrimPrefix(x, "*."), d) })
	}
	var files []contracts.File
	for _, f := range w.Files {
		d, ok := ours(f.Domain)
		file := contracts.File{Domain: d, Path: f.Path, Content: []byte(f.Content)}
		switch {
		case !ok:
			return nil, nil, bad("the file %s is for %q, not a domain of the certificate", f.Path, f.Domain)
		case !file.Valid():
			return nil, nil, bad("the file %q is not below %s", f.Path, contracts.WellKnown)
		case len(f.Content) > maxFileSize:
			return nil, nil, bad("the file %s is larger than %d bytes", f.Path, maxFileSize)
		case slices.ContainsFunc(files, func(g contracts.File) bool { return g.Domain == d && g.Path == f.Path }):
			return nil, nil, bad("the file %s of %s comes twice", f.Path, d)
		}
		files = append(files, file)
	}
	var records []contracts.Record
	for _, r := range w.Records {
		d, ok := ours(r.Domain)
		if !ok {
			return nil, nil, bad("the record %s is for %q, not a domain of the certificate", r.Name, r.Domain)
		}
		rec := contracts.Record{Name: dnscheck.RecordName(r.Name, d), Type: strings.ToUpper(strings.TrimSpace(r.Type)), Value: r.Value}
		if rec.Name == "" || (rec.Type != "TXT" && rec.Type != "CNAME") || rec.Value == "" || len(rec.Value) > maxValue {
			return nil, nil, bad("JEC does not add the record %q of type %q with that value: only TXT and CNAME records with a value of %d bytes at most", r.Name, r.Type, maxValue)
		}
		if !slices.Contains(records, rec) {
			records = append(records, rec)
		}
	}
	return files, records, nil
}

// removeFile removes a file JEC put in place, at step of the log; failing
// to is only logged.
func removeFile(ctx context.Context, files contracts.FileSolver, p placedFile, step contracts.Step, log func(contracts.LogLine)) {
	params := map[string]any{"domain": p.Domain, "path": p.Path}
	if files == nil { // put by a solver no longer chosen
		log(contracts.LogLine{Level: contracts.Warn, Step: step, Key: i18n.PluginsLogRemoveFileFailed, Params: params})
		return
	}
	log(contracts.LogLine{Level: contracts.Info, Step: step, Key: i18n.PluginsLogRemoveFile, Params: params})
	if err := files.RemoveFile(ctx, contracts.File{Domain: p.Domain, Path: p.Path}); err != nil {
		log(contracts.LogLine{Level: contracts.Warn, Step: step, Key: i18n.PluginsLogRemoveFileFailed, Params: params, Detail: err.Error()})
	}
}

// removeRecord removes a record JEC added, at step of the log, unless
// people added it; failing to is only logged.
func removeRecord(ctx context.Context, records contracts.RecordSolver, p placedRecord, step contracts.Step, log func(contracts.LogLine)) {
	params := map[string]any{"name": p.Name, "type": p.Type}
	if p.Ref == "" || records == nil { // added by people, or by a solver no longer chosen
		log(contracts.LogLine{Level: contracts.Info, Step: step, Key: i18n.PluginsLogRecordLeft, Params: params})
		return
	}
	log(contracts.LogLine{Level: contracts.Info, Step: step, Key: i18n.PluginsLogRemoveRecord, Params: params})
	if err := records.RemoveRecord(ctx, p.record(), p.Ref); err != nil {
		log(contracts.LogLine{Level: contracts.Warn, Step: step, Key: i18n.PluginsLogRemoveRecordFailed, Params: params, Detail: err.Error()})
	}
}

// putFailed explains a failure to put a file on the website: the solver's
// own reason, if it gave one.
func putFailed(err error, domain string) error {
	var problem *contracts.Problem
	if errors.As(err, &problem) {
		return err
	}
	return &contracts.Problem{Key: i18n.PluginsErrorPutFile, Params: map[string]any{"domain": domain}, Err: err}
}

// addFailed explains a failure to add a record: the solver's own reason,
// if it gave one.
func addFailed(err error, name string) error {
	var problem *contracts.Problem
	if errors.As(err, &problem) {
		return err
	}
	return &contracts.Problem{Key: i18n.PluginsErrorAddRecord, Params: map[string]any{"name": name}, Err: err}
}

// keySource takes in certificates with their keys through a plugin.
type keySource struct{ instance }

// Collect asks the plugin for a certificate newer than the one in use, and
// takes the chain and the key it answers, or waits as it asks.
func (s *keySource) Collect(ctx context.Context, req contracts.CollectRequest) (*contracts.Collected, error) {
	r := s.request(actionCollect)
	r.Collect = &collectOrder{Domains: req.Domains, Current: req.Current, State: string(req.State)}
	if !req.CurrentEnd.IsZero() {
		r.Collect.CurrentEnd = req.CurrentEnd.UTC().Format(time.RFC3339)
	}
	a, err := s.do(ctx, actionTimeout, r, req.Log)
	if err != nil {
		return nil, err
	}
	if w := a.Wait; w != nil {
		if len(w.Files) > 0 || len(w.Records) > 0 {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.PluginsErrorValidation, Params: map[string]any{
				"plugin": s.plugin.id, "problem": "a source with keys of its own validates nothing through JEC",
			}})
		}
		return nil, &contracts.Wait{State: []byte(w.State), After: waitFor(w.After)}
	}
	b, err := cryptox.ParseBundle([]byte(a.Chain), []byte(a.Key))
	if err == nil && !cryptox.Covers(cryptox.Names(b.Leaf), req.Domains) {
		err = errors.New("the certificate does not cover the domains")
	}
	if err != nil {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.PluginsErrorChain, Params: map[string]any{"plugin": s.plugin.id}, Err: err})
	}
	return &contracts.Collected{Chain: append([]*x509.Certificate{b.Leaf}, b.Chain...), Key: b.Key}, nil
}

// maxHeld is how many of the certificates a plugin lists JEC reads.
const maxHeld = 1000

// Held asks the plugin for the certificates it holds. An expiry it does not
// give, or not in RFC 3339, is left unknown.
func (s *keySource) Held(ctx context.Context) ([]contracts.Held, error) {
	a, err := s.do(ctx, testTimeout, s.request(actionHeld), nil)
	if err != nil {
		return nil, err
	}
	held := make([]contracts.Held, 0, min(len(a.Certificates), maxHeld))
	for _, c := range a.Certificates[:min(len(a.Certificates), maxHeld)] {
		end, _ := time.Parse(time.RFC3339, c.NotAfter)
		held = append(held, contracts.Held{Domains: c.Domains, NotAfter: end})
	}
	return held, nil
}

// waitFor is how long to wait as a plugin asked, in seconds: a minute to a
// day.
func waitFor(seconds int) time.Duration {
	return time.Duration(min(max(seconds, 60), 86400)) * time.Second
}
