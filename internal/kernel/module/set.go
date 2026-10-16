package module

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"time"
)

// Errors of Set.SetEnabled.
var (
	ErrUnknown  = errors.New("no such module")
	ErrRequired = errors.New("the module is required and cannot be switched off")
)

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// Set is the modules compiled into the binary and whether each is on.
type Set struct {
	modules   []Module // every module after those it requires
	manifests map[string]Manifest

	mu sync.RWMutex
	db *sql.DB
	on map[string]bool // as switched, before requirements
}

// NewSet checks the manifests of mods and orders the modules so that each
// comes after the modules it requires. All modules count as on until Load.
func NewSet(mods ...Module) (*Set, error) {
	s := &Set{manifests: make(map[string]Manifest, len(mods)), on: make(map[string]bool, len(mods))}
	byID := make(map[string]Module, len(mods))
	var errs []error
	for _, m := range mods {
		mf := m.Manifest()
		switch {
		case !idPattern.MatchString(mf.ID) || mf.ID == "kernel":
			errs = append(errs, fmt.Errorf("module ID %q: want 1 to 32 lowercase letters, digits and underscores, starting with a letter, and not kernel", mf.ID))
			continue
		case byID[mf.ID] != nil:
			errs = append(errs, fmt.Errorf("module %s is registered twice", mf.ID))
			continue
		case !slices.Contains(categories, mf.Category):
			errs = append(errs, fmt.Errorf("module %s: unknown category %q", mf.ID, mf.Category))
		case mf.Name == "" || mf.Description == "":
			errs = append(errs, fmt.Errorf("module %s: name and description are required", mf.ID))
		case mf.Required && mf.Experimental:
			errs = append(errs, fmt.Errorf("module %s cannot be both required and experimental", mf.ID))
		}
		byID[mf.ID] = m
		s.manifests[mf.ID] = mf
		s.on[mf.ID] = true
	}
	for id, mf := range s.manifests {
		for _, req := range mf.Requires {
			if _, ok := s.manifests[req]; !ok || req == id {
				errs = append(errs, fmt.Errorf("module %s requires %q, which is not compiled in", id, req))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	// Depth-first: a module goes in after everything it requires. Visiting
	// IDs in sorted order keeps the result the same from run to run.
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(id string, path []string) error
	visit = func(id string, path []string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("modules require each other in a cycle: %v", append(path, id))
		case 2:
			return nil
		}
		state[id] = 1
		reqs := slices.Sorted(slices.Values(s.manifests[id].Requires))
		for _, req := range reqs {
			if err := visit(req, append(slices.Clip(path), id)); err != nil {
				return err
			}
		}
		state[id] = 2
		s.modules = append(s.modules, byID[id])
		return nil
	}
	for _, id := range ids {
		if err := visit(id, nil); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Modules returns the modules, each after the modules it requires.
func (s *Set) Modules() []Module { return slices.Clone(s.modules) }

// Load reads from db which modules are switched on. A module seen for the
// first time starts on unless it is experimental, and that is recorded.
func (s *Set) Load(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "SELECT id, enabled FROM kernel_modules")
	if err != nil {
		return fmt.Errorf("load module states: %w", err)
	}
	stored := map[string]bool{}
	for rows.Next() {
		var id string
		var on bool
		if err := rows.Scan(&id, &on); err != nil {
			_ = rows.Close()
			return fmt.Errorf("load module states: %w", err)
		}
		stored[id] = on
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("load module states: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.db = db
	for _, m := range s.modules {
		mf := m.Manifest()
		on, ok := stored[mf.ID]
		if !ok {
			on = !mf.Experimental
			if err := s.store(ctx, mf.ID, on); err != nil {
				return err
			}
		}
		s.on[mf.ID] = on
	}
	return nil
}

func (s *Set) store(ctx context.Context, id string, on bool) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kernel_modules (id, enabled, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`,
		id, on, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("store the state of module %s: %w", id, err)
	}
	return nil
}

// Enabled reports whether module id is on: required or switched on, and
// every module it requires on as well.
func (s *Set) Enabled(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled(id)
}

func (s *Set) enabled(id string) bool {
	mf, ok := s.manifests[id]
	if !ok || (!mf.Required && !s.on[id]) {
		return false
	}
	for _, req := range mf.Requires {
		if !s.enabled(req) {
			return false
		}
	}
	return true
}

// SetEnabled switches module id on or off and stores the choice. Call it
// after Load.
func (s *Set) SetEnabled(ctx context.Context, id string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	mf, ok := s.manifests[id]
	switch {
	case !ok:
		return fmt.Errorf("%w: %s", ErrUnknown, id)
	case mf.Required && !on:
		return fmt.Errorf("%w: %s", ErrRequired, id)
	case s.db == nil:
		return errors.New("module states are not loaded")
	}
	if err := s.store(ctx, id, on); err != nil {
		return err
	}
	s.on[id] = on
	return nil
}

// Status is a module's manifest and whether the module is on.
type Status struct {
	Manifest
	// Switched is whether the module is switched on by itself.
	Switched bool
	// Enabled is whether it runs: switched on, and its requirements too.
	Enabled bool
}

// Statuses returns every module's status, ordered by category and ID.
func (s *Set) Statuses() []Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Status, 0, len(s.modules))
	for _, m := range s.modules {
		mf := m.Manifest()
		out = append(out, Status{Manifest: mf, Switched: mf.Required || s.on[mf.ID], Enabled: s.enabled(mf.ID)})
	}
	slices.SortFunc(out, func(a, b Status) int {
		return cmp.Or(
			cmp.Compare(slices.Index(categories, a.Category), slices.Index(categories, b.Category)),
			cmp.Compare(a.ID, b.ID))
	})
	return out
}
