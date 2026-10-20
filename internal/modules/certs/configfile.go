package certs

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/configfile"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// The module's sections of the configuration file (PLAN.md 11): the
// renewal defaults, and the certificates, which name their providers by
// name. An import adds the certificates JEC lacks, to be issued by their
// sources, and changes the solver, the key type, the renewal window and
// the targets of those it has, found by their domains; it deletes none.

// renewalEntry is the renewal defaults in a file. A value left out stays
// as it is.
type renewalEntry struct {
	RenewBeforeDays int    `yaml:"renew_before_days,omitempty"`
	CheckTime       string `yaml:"check_time,omitempty"`
	KeyType         string `yaml:"key_type,omitempty"`
	// Retries is a pointer, as 0 is one of its values.
	Retries *int `yaml:"retries,omitempty"`
}

// certificateEntry is a certificate in a file. Left out, the key type and
// the renewal window are those of the settings for a new certificate, and
// stay as they are for another, as do the targets.
type certificateEntry struct {
	Domains []string `yaml:"domains"`
	// Source and Solver are the names of the providers; no Source is a
	// certificate that was uploaded.
	Source          string   `yaml:"source,omitempty"`
	Solver          string   `yaml:"solver,omitempty"`
	KeyType         string   `yaml:"key_type,omitempty"`
	RenewBeforeDays int      `yaml:"renew_before_days,omitempty"`
	Targets         []string `yaml:"targets"`
	// Note, left out, stays as it is.
	Note *string `yaml:"note,omitempty"`
	// ReuseKey keeps the private key for renewals; left out, it stays as
	// it is.
	ReuseKey *bool `yaml:"reuse_key,omitempty"`
	// NotifyTo names the notifiers its notes go to, all when empty; left
	// out, they stay as they are.
	NotifyTo []string `yaml:"notify_to,omitempty"`
}

// configSections are the module's sections: the renewal defaults first,
// which new certificates take.
func (s *service) configSections() []configfile.Section {
	return []configfile.Section{
		{
			Key: "renewal", Title: i18n.CertsConfigSectionRenewal, New: func() any { return new(renewalEntry) },
			Export: s.exportRenewal, Import: s.importRenewal,
		},
		{
			Key: "certificates", Title: i18n.CertsConfigSectionCertificates, New: func() any { return new([]certificateEntry) },
			Export: s.exportCertificates, Import: s.importCertificates,
		},
	}
}

func (s *service) exportRenewal(ctx context.Context, _ configfile.Export) (any, error) {
	st, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	return &renewalEntry{RenewBeforeDays: st.RenewBeforeDays, CheckTime: formatClock(st.CheckTime), KeyType: st.KeyType, Retries: &st.Retries}, nil
}

func (s *service) importRenewal(ctx context.Context, tx *sql.Tx, v any, in *configfile.Import) (func(), error) {
	e := v.(*renewalEntry)
	had, err := settingsFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	st := had
	it := configfile.Item{Action: configfile.Skip}
	invalid := func(field string) {
		it.Problems = append(it.Problems, configfile.Problem{Field: field, Key: i18n.KernelErrorInvalidField})
	}
	if e.RenewBeforeDays != 0 {
		st.RenewBeforeDays = e.RenewBeforeDays
		if e.RenewBeforeDays < 1 || e.RenewBeforeDays > 365 {
			invalid("renew_before_days")
		}
	}
	if e.CheckTime != "" {
		m, ok := parseClock(e.CheckTime)
		if !ok {
			invalid("check_time")
		}
		st.CheckTime = m
	}
	if e.KeyType != "" {
		st.KeyType = e.KeyType
		if !slices.Contains(cryptox.KeyTypes, e.KeyType) {
			invalid("key_type")
		}
	}
	if e.Retries != nil {
		st.Retries = *e.Retries
		if *e.Retries < 0 || *e.Retries > maxRetries {
			invalid("retries")
		}
	}
	switch {
	case len(it.Problems) > 0:
	case st == had:
		it.Action = configfile.Keep
	default:
		if err := saveSettingsTx(ctx, tx, st); err != nil {
			return nil, err
		}
		it.Action = configfile.Change
	}
	in.Report(it)
	return nil, nil
}

func (s *service) exportCertificates(ctx context.Context, _ configfile.Export) (any, error) {
	list, err := s.list(ctx)
	if err != nil {
		return nil, err
	}
	// In the order they were added, which keeps the file's lines in place
	// from one export to the next.
	slices.SortFunc(list, func(a, b *Certificate) int { return cmp.Compare(a.ID, b.ID) })
	out := make([]certificateEntry, 0, len(list))
	for _, c := range list {
		e := certificateEntry{Domains: c.Domains, RenewBeforeDays: c.RenewBeforeDays, Targets: []string{}}
		if c.Note != "" {
			e.Note = &c.Note
		}
		if c.ReuseKey {
			e.ReuseKey = &c.ReuseKey
		}
		if c.Source == sourceProvider {
			if e.Source, err = s.providerName(ctx, c.SourceProvider); err != nil {
				return nil, err
			}
			if c.SolverProvider != 0 {
				if e.Solver, err = s.providerName(ctx, c.SolverProvider); err != nil {
					return nil, err
				}
			}
			e.KeyType = c.KeyType
		}
		for _, t := range c.Targets {
			name, err := s.providerName(ctx, t)
			if err != nil {
				return nil, err
			}
			e.Targets = append(e.Targets, name)
		}
		for _, n := range c.NotifyTo {
			name, err := s.providerName(ctx, n)
			if err != nil {
				return nil, err
			}
			e.NotifyTo = append(e.NotifyTo, name)
		}
		out = append(out, e)
	}
	return &out, nil
}

// providerName is the name of provider instance id.
func (s *service) providerName(ctx context.Context, id int64) (string, error) {
	p, err := s.providers.Get(ctx, id)
	if err != nil {
		return "", fmt.Errorf("provider %d: %w", id, err)
	}
	return p.Name, nil
}

// certImport is the state of an import of certificates.
type certImport struct {
	in       *configfile.Import
	tx       *sql.Tx
	settings Settings
	// have are the certificates JEC had, by domainKey; seen are those of
	// the file so far.
	have map[string][]*Certificate
	seen map[string]bool
	// added and deployed tell the runners to wake.
	added, deployed bool
}

// domainKey is a set of normalized domains, whatever their order.
func domainKey(domains []string) string {
	return strings.Join(slices.Sorted(slices.Values(domains)), " ")
}

func (s *service) importCertificates(ctx context.Context, tx *sql.Tx, v any, in *configfile.Import) (func(), error) {
	list, err := s.query(ctx, tx, selectCertificates)
	if err != nil {
		return nil, err
	}
	// The defaults as the file leaves them, its renewal section coming
	// first.
	st, err := settingsFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	ci := &certImport{in: in, tx: tx, settings: st, have: map[string][]*Certificate{}, seen: map[string]bool{}}
	for _, c := range list {
		k := domainKey(c.Domains)
		ci.have[k] = append(ci.have[k], c)
	}
	for _, e := range *v.(*[]certificateEntry) {
		it, err := s.importCertificate(ctx, ci, e)
		if err != nil {
			return nil, err
		}
		in.Report(it)
	}
	return func() {
		if ci.added {
			s.issuing.kick()
		}
		if ci.deployed {
			s.deploying.kick()
		}
	}, nil
}

// importCertificate adds or changes the certificate of e, and says what
// it did.
func (s *service) importCertificate(ctx context.Context, ci *certImport, e certificateEntry) (configfile.Item, error) {
	// Spaces part the domains: the UI lists entries with commas.
	domains, bad := normalizeDomains(e.Domains)
	it := configfile.Item{Name: strings.Join(domains, " "), Action: configfile.Skip}
	problem := func(field string, key i18n.Key, params map[string]any) {
		it.Problems = append(it.Problems, configfile.Problem{Field: field, Key: key, Params: params})
	}
	for _, i := range bad {
		problem(fmt.Sprintf("domains[%d]", i), i18n.CertsErrorBadDomain, nil)
	}
	if len(domains) == 0 && len(bad) == 0 {
		problem("domains", i18n.KernelErrorFieldRequired, nil)
	}
	if len(it.Problems) > 0 {
		it.Name = strings.Join(e.Domains, " ")
		return it, nil
	}
	key := domainKey(domains)
	if ci.seen[key] {
		problem("domains", i18n.KernelConfigErrorDuplicate, map[string]any{"name": it.Name})
		return it, nil
	}
	ci.seen[key] = true
	// The targets' problems come after the others, as in the file.
	var targets []int64
	var targetProblems []configfile.Problem
	if e.Targets != nil {
		targets = []int64{}
		for i, name := range e.Targets {
			p, pr := ci.in.Provider(providers.Target, name)
			if pr != nil {
				pr.Field = fmt.Sprintf("targets[%d]", i)
				targetProblems = append(targetProblems, *pr)
			} else if !slices.Contains(targets, p.ID) {
				targets = append(targets, p.ID)
			}
		}
	}
	var notifyTo []int64
	if e.NotifyTo != nil {
		notifyTo = []int64{}
		for i, name := range e.NotifyTo {
			p, pr := ci.in.Provider(providers.Notifier, name)
			if pr != nil {
				pr.Field = fmt.Sprintf("notify_to[%d]", i)
				targetProblems = append(targetProblems, *pr)
			} else if !slices.Contains(notifyTo, p.ID) {
				notifyTo = append(notifyTo, p.ID)
			}
		}
	}
	switch found := ci.have[key]; len(found) {
	case 0:
		return s.addCertificate(ctx, ci, e, domains, targets, notifyTo, targetProblems, it)
	case 1:
		return s.changeCertificate(ctx, ci, e, found[0], targets, notifyTo, targetProblems, it)
	}
	problem("domains", i18n.CertsConfigErrorAmbiguous, nil)
	return it, nil
}

// addCertificate adds the certificate of e, to be issued by its source,
// unless it has problems.
func (s *service) addCertificate(ctx context.Context, ci *certImport, e certificateEntry, domains []string, targets, notifyTo []int64,
	targetProblems []configfile.Problem, it configfile.Item,
) (configfile.Item, error) {
	if e.Source == "" {
		// As for one that was uploaded: nothing to issue it with.
		it.Problems = append(it.Problems, configfile.Problem{Key: i18n.CertsConfigErrorNoSource, Warning: true})
		it.Problems = append(it.Problems, targetProblems...)
		return it, nil
	}
	c := Certificate{Domains: domains, KeyType: e.KeyType, RenewBeforeDays: e.RenewBeforeDays, Targets: targets, NotifyTo: notifyTo}
	if e.Note != nil {
		c.Note = strings.TrimSpace(*e.Note)
	}
	if e.ReuseKey != nil {
		c.ReuseKey = *e.ReuseKey
	}
	source, pr := ci.in.Provider(providers.Source, e.Source)
	if pr != nil {
		pr.Field = "source"
		it.Problems = append(it.Problems, *pr)
	}
	c.SourceProvider = source.ID
	if e.Solver != "" {
		solver, pr := ci.in.Provider(providers.Solver, e.Solver)
		if pr != nil {
			pr.Field = "solver"
			it.Problems = append(it.Problems, *pr)
		}
		c.SolverProvider = solver.ID
		if pr == nil && source.ID != 0 {
			it.Problems = append(it.Problems, solverProblems(source.Type, solver.Type, domains)...)
		}
	} else if source.Type.NeedsSolver {
		it.Problems = append(it.Problems, configfile.Problem{Field: "solver", Key: i18n.KernelErrorFieldRequired})
	}
	switch {
	case source.Type.OwnKey:
		c.KeyType = "" // the source's own
	case c.KeyType == "":
		c.KeyType = ci.settings.KeyType
	case !slices.Contains(cryptox.KeyTypes, c.KeyType):
		it.Problems = append(it.Problems, configfile.Problem{Field: "key_type", Key: i18n.KernelErrorInvalidField})
	}
	if c.RenewBeforeDays == 0 {
		c.RenewBeforeDays = ci.settings.RenewBeforeDays
	} else if c.RenewBeforeDays < 1 || c.RenewBeforeDays > 365 {
		it.Problems = append(it.Problems, configfile.Problem{Field: "renew_before_days", Key: i18n.KernelErrorInvalidField})
	}
	if c.Targets == nil {
		c.Targets = []int64{}
	}
	it.Problems = append(it.Problems, targetProblems...)
	if len(it.Problems) > 0 {
		return it, nil
	}
	if _, err := s.insertIssued(ctx, ci.tx, c); err != nil {
		return it, err
	}
	ci.added = true
	it.Action = configfile.Add
	return it, nil
}

// solverProblems tell why solver cannot prove control of domains for
// source.
func solverProblems(source, solver providers.Type, domains []string) []configfile.Problem {
	switch {
	case source.NeedsSolver && !source.Takes(solver):
		return []configfile.Problem{{Field: "solver", Key: i18n.CertsErrorSolverChallenge}}
	case solver.Solves() == providers.ChallengeHTTP && slices.ContainsFunc(domains, isWildcard):
		return []configfile.Problem{{Field: "solver", Key: i18n.CertsErrorWildcardHttp}}
	}
	return nil
}

// changeCertificate gives c what e says, unless it has problems. Its
// source stays, whatever e says.
func (s *service) changeCertificate(ctx context.Context, ci *certImport, e certificateEntry, c *Certificate, targets, notifyTo []int64,
	targetProblems []configfile.Problem, it configfile.Item,
) (configfile.Item, error) {
	solver, keyType, days := c.SolverProvider, c.KeyType, c.RenewBeforeDays
	current, err := s.providers.Type(ctx, c.SourceProvider)
	switch {
	case c.Source != sourceProvider:
		if e.Source != "" {
			it.Problems = append(it.Problems, configfile.Problem{Field: "source", Key: i18n.CertsConfigErrorUploadedKept, Warning: true})
		}
	case errors.Is(err, providers.ErrUnavailable):
		// Its module is off: the solver and the key type stay too.
		it.Problems = append(it.Problems, configfile.Problem{Field: "source", Key: i18n.KernelErrorProviderTypeUnknown, Warning: true})
	case err != nil:
		return it, fmt.Errorf("provider %d: %w", c.SourceProvider, err)
	default:
		if p, pr := ci.in.Provider(providers.Source, e.Source); pr != nil || p.ID != c.SourceProvider {
			name, err := s.providerName(ctx, c.SourceProvider)
			if err != nil {
				return it, err
			}
			it.Problems = append(it.Problems, configfile.Problem{Field: "source", Key: i18n.CertsConfigErrorSourceKept, Params: map[string]any{"name": name}, Warning: true})
		}
		switch {
		case e.Solver != "":
			p, pr := ci.in.Provider(providers.Solver, e.Solver)
			if pr != nil {
				pr.Field = "solver"
				it.Problems = append(it.Problems, *pr)
			} else {
				it.Problems = append(it.Problems, solverProblems(current, p.Type, c.Domains)...)
				solver = p.ID
			}
		case current.NeedsSolver:
			it.Problems = append(it.Problems, configfile.Problem{Field: "solver", Key: i18n.KernelErrorFieldRequired})
		default:
			solver = 0
		}
		if e.KeyType != "" && !current.OwnKey {
			keyType = e.KeyType
			if !slices.Contains(cryptox.KeyTypes, e.KeyType) {
				it.Problems = append(it.Problems, configfile.Problem{Field: "key_type", Key: i18n.KernelErrorInvalidField})
			}
		}
	}
	if e.RenewBeforeDays != 0 {
		days = e.RenewBeforeDays
		if days < 1 || days > 365 {
			it.Problems = append(it.Problems, configfile.Problem{Field: "renew_before_days", Key: i18n.KernelErrorInvalidField})
		}
	}
	it.Problems = append(it.Problems, targetProblems...)
	if slices.ContainsFunc(it.Problems, func(p configfile.Problem) bool { return !p.Warning }) {
		return it, nil
	}
	changed := false
	if solver != c.SolverProvider || keyType != c.KeyType || days != c.RenewBeforeDays {
		changed = true
		_, err := ci.tx.ExecContext(ctx,
			"UPDATE certs_certificates SET solver_provider = ?, key_type = ?, renew_before_days = ?, updated_at = ? WHERE id = ?",
			sql.NullInt64{Int64: solver, Valid: solver != 0}, keyType, days, s.now().UnixMilli(), c.ID)
		if err != nil {
			return it, err
		}
	}
	if targets != nil && !slices.Equal(targets, c.Targets) {
		changed = true
		if err := s.setTargets(ctx, ci.tx, c, targets); err != nil {
			return it, err
		}
		ci.deployed = true
	}
	if e.Note != nil && strings.TrimSpace(*e.Note) != c.Note {
		changed = true
		if _, err := ci.tx.ExecContext(ctx, "UPDATE certs_certificates SET note = ?, updated_at = ? WHERE id = ?",
			strings.TrimSpace(*e.Note), s.now().UnixMilli(), c.ID); err != nil {
			return it, err
		}
	}
	if notifyTo != nil && !slices.Equal(notifyTo, c.NotifyTo) {
		changed = true
		list, _ := json.Marshal(notifyTo)
		if _, err := ci.tx.ExecContext(ctx, "UPDATE certs_certificates SET notify_to = ?, updated_at = ? WHERE id = ?",
			string(list), s.now().UnixMilli(), c.ID); err != nil {
			return it, err
		}
	}
	if e.ReuseKey != nil && *e.ReuseKey != c.ReuseKey {
		changed = true
		reuse := 0
		if *e.ReuseKey {
			reuse = 1
		}
		if _, err := ci.tx.ExecContext(ctx, "UPDATE certs_certificates SET reuse_key = ?, updated_at = ? WHERE id = ?",
			reuse, s.now().UnixMilli(), c.ID); err != nil {
			return it, err
		}
	}
	it.Action = configfile.Keep
	if changed {
		it.Action = configfile.Change
	}
	return it, nil
}
