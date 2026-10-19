// Package jobs runs the periodic jobs of the kernel and the modules, such
// as looking for certificates to renew (PLAN.md 4.4), and the workers of
// the modules, which run all along, such as listening to a chat bot.
//
// A job runs every Every, counted from the start of its last run. That start
// is stored, so restarting JEC does not shift the schedule; a job that is
// due, or has never run, starts shortly after startup. A job never overlaps
// itself, and a module's jobs run only while the module is on.
//
// A worker runs while its module is on: it starts once the scheduler runs,
// its context ends when the module goes off or JEC stops, and it starts
// again when the module is back on, or a while after it returned.
package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	startDelay  = 10 * time.Second // from startup to the first run of a due job
	recheck     = time.Minute      // how soon a job skipped while its module was off is due again
	tickEvery   = time.Second      // how often due jobs are looked for
	maxErrorLen = 1000             // bytes of an error message stored
	workerPause = 30 * time.Second // from a worker's return to its next start
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Job is work done periodically.
type Job struct {
	// Name tells the module's jobs apart. The last run is stored under it,
	// so it must not change.
	Name string
	// Every is the time from the start of one run to the start of the next.
	Every time.Duration
	// Timeout bounds a run. Zero means Every.
	Timeout time.Duration
	// Run does the work. An error is logged, and the job runs again at its
	// next time; retrying sooner is up to the job.
	Run func(ctx context.Context) error
}

// Worker is work that runs as long as its module is on.
type Worker struct {
	// Name tells the module's workers apart.
	Name string
	// Run works until ctx is done. Returning before that, as on a fatal
	// error, has it run again after a pause.
	Run func(ctx context.Context)
}

// worker is a Worker of a module, with whether it runs.
type worker struct {
	Worker
	id       string // <module>.<name>
	module   string
	cancel   context.CancelFunc // while it runs
	done     chan struct{}      // closed once it returned
	stopping bool               // its module went off: no pause once it returns
	after    time.Time          // not before then, after it returned
}

// entry is a Job of a module, with its schedule.
type entry struct {
	Job
	id      string // <module>.<name>
	module  string
	next    time.Time
	running bool
	again   bool // triggered while running: due once the run ends
}

// Scheduler runs jobs on schedule.
type Scheduler struct {
	db         *sql.DB
	log        *slog.Logger
	enabled    func(module string) bool
	now        func() time.Time
	startDelay time.Duration

	mu      sync.Mutex
	started bool
	jobs    []*entry // sorted by ID
	workers []*worker
	wg      sync.WaitGroup
	kick    chan struct{}
}

// New returns a scheduler that stores the last runs in db, whose kernel
// migrations are applied. enabled reports whether a module is on; "kernel"
// counts as a module.
func New(db *sql.DB, log *slog.Logger, enabled func(module string) bool) *Scheduler {
	return &Scheduler{db: db, log: log, enabled: enabled, now: time.Now, startDelay: startDelay, kick: make(chan struct{}, 1)}
}

// Scope returns the handle through which module adds its jobs.
func (s *Scheduler) Scope(module string) *Scope { return &Scope{sched: s, module: module} }

// Scope is a module's access to the scheduler.
type Scope struct {
	sched  *Scheduler
	module string
}

// Add schedules a job, before the scheduler runs.
func (s *Scope) Add(job Job) error {
	switch {
	case !namePattern.MatchString(job.Name):
		return fmt.Errorf("job name %q: want lowercase letters, digits and underscores, starting with a letter", job.Name)
	case job.Every <= 0:
		return fmt.Errorf("job %s needs a positive interval", job.Name)
	case job.Timeout < 0:
		return fmt.Errorf("job %s: the timeout cannot be negative", job.Name)
	case job.Run == nil:
		return fmt.Errorf("job %s has nothing to run", job.Name)
	}
	if job.Timeout == 0 {
		job.Timeout = job.Every
	}
	id := s.module + "." + job.Name
	sched := s.sched
	sched.mu.Lock()
	defer sched.mu.Unlock()
	if sched.started {
		return fmt.Errorf("job %s: add jobs while the module registers", id)
	}
	i, found := slices.BinarySearchFunc(sched.jobs, id, func(e *entry, id string) int { return strings.Compare(e.id, id) })
	if found {
		return fmt.Errorf("job %s exists already", id)
	}
	sched.jobs = slices.Insert(sched.jobs, i, &entry{Job: job, id: id, module: s.module})
	return nil
}

// Work adds a worker, before the scheduler runs.
func (s *Scope) Work(w Worker) error {
	switch {
	case !namePattern.MatchString(w.Name):
		return fmt.Errorf("worker name %q: want lowercase letters, digits and underscores, starting with a letter", w.Name)
	case w.Run == nil:
		return fmt.Errorf("worker %s has nothing to run", w.Name)
	}
	id := s.module + "." + w.Name
	sched := s.sched
	sched.mu.Lock()
	defer sched.mu.Unlock()
	switch {
	case sched.started:
		return fmt.Errorf("worker %s: add workers while the module registers", id)
	case slices.ContainsFunc(sched.workers, func(o *worker) bool { return o.id == id }):
		return fmt.Errorf("worker %s exists already", id)
	}
	sched.workers = append(sched.workers, &worker{Worker: w, id: id, module: s.module})
	return nil
}

// Trigger makes the module's job name due now, as when there is work for
// it that should not wait for its next time. A job already running runs
// again once it ends, so that it sees the new work.
func (s *Scope) Trigger(name string) {
	sched := s.sched
	id := s.module + "." + name
	sched.mu.Lock()
	for _, e := range sched.jobs {
		if e.id == id {
			e.next = time.Time{}
			e.again = e.running
		}
	}
	sched.mu.Unlock()
	select {
	case sched.kick <- struct{}{}:
	default:
	}
}

// Run runs the jobs on schedule until ctx is done, then waits for the
// running ones, which see ctx done as well.
func (s *Scheduler) Run(ctx context.Context) {
	if err := s.load(ctx); err != nil {
		s.log.Warn("load job schedules", "err", err)
	}
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		s.tick(ctx)
		s.supervise(ctx)
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return
		case <-t.C:
		case <-s.kick:
		}
	}
}

// load fixes the job list and plans each job's next run from its last
// start, no sooner than startDelay from now.
func (s *Scheduler) load(ctx context.Context) error {
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()

	earliest := s.now().Add(s.startDelay)
	last := map[string]time.Time{}
	rows, err := s.db.QueryContext(ctx, "SELECT name, started_at FROM kernel_jobs")
	if err == nil {
		for rows.Next() {
			var name string
			var started int64
			if err = rows.Scan(&name, &started); err != nil {
				break
			}
			last[name] = time.UnixMilli(started)
		}
		err = rows.Err()
		_ = rows.Close()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.jobs {
		e.next = earliest
		if started, ok := last[e.id]; ok && started.Add(e.Every).After(earliest) {
			e.next = started.Add(e.Every)
		}
	}
	return err
}

// tick starts the jobs that are due and idle. It returns how many it
// started.
func (s *Scheduler) tick(ctx context.Context) int {
	if ctx.Err() != nil {
		return 0
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.jobs {
		switch {
		case e.running || now.Before(e.next):
		case !s.enabled(e.module):
			e.next = now.Add(min(recheck, e.Every))
		default:
			e.running = true
			e.next = now.Add(e.Every)
			s.wg.Add(1)
			go s.run(ctx, e, now)
			n++
		}
	}
	return n
}

// supervise starts the workers whose modules are on, and stops those whose
// modules went off; one that returned starts again after a pause.
func (s *Scheduler) supervise(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.workers {
		if w.cancel != nil {
			select {
			case <-w.done:
				w.cancel, w.after = nil, now.Add(workerPause)
				if w.stopping {
					w.after, w.stopping = now, false
				}
			default:
			}
		}
		on := s.enabled(w.module)
		switch {
		case w.cancel != nil && !on && !w.stopping:
			w.cancel()
			w.stopping = true
		case w.cancel == nil && on && !now.Before(w.after):
			wctx, cancel := context.WithCancel(ctx)
			w.cancel, w.done = cancel, make(chan struct{})
			s.wg.Add(1)
			go s.work(wctx, w, w.done)
		}
	}
}

// work runs a worker until it returns, turning a panic into a log line.
func (s *Scheduler) work(ctx context.Context, w *worker, done chan struct{}) {
	defer s.wg.Done()
	defer close(done)
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("worker panicked", "worker", w.id, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	w.Run(ctx)
}

// run runs a job once and stores when it started and how it ended.
func (s *Scheduler) run(ctx context.Context, e *entry, start time.Time) {
	defer func() {
		s.mu.Lock()
		e.running = false
		again := e.again
		e.again = false
		if again {
			e.next = time.Time{}
		}
		s.mu.Unlock()
		if again {
			select {
			case s.kick <- struct{}{}:
			default:
			}
		}
		s.wg.Done()
	}()
	log := s.log.With("job", e.id)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kernel_jobs (name, started_at, finished_at, error) VALUES (?, ?, NULL, '')
		 ON CONFLICT (name) DO UPDATE SET started_at = excluded.started_at, finished_at = NULL, error = ''`,
		e.id, start.UnixMilli())
	if err != nil {
		log.Warn("record job start", "err", err)
	}

	err = s.call(ctx, e)
	if err != nil && ctx.Err() != nil {
		return // shutting down: finished_at stays NULL, marking the run as cut short
	}
	msg := ""
	if err != nil {
		log.Warn("job failed", "err", err)
		msg = err.Error()
		if len(msg) > maxErrorLen {
			msg = msg[:maxErrorLen]
		}
	} else {
		log.Debug("job done", "took", s.now().Sub(start).Round(time.Millisecond))
	}
	_, err = s.db.ExecContext(context.WithoutCancel(ctx),
		"UPDATE kernel_jobs SET finished_at = ?, error = ? WHERE name = ?", s.now().UnixMilli(), msg, e.id)
	if err != nil {
		log.Warn("record job end", "err", err)
	}
}

// call runs the job with its timeout, turning a panic into an error.
func (s *Scheduler) call(ctx context.Context, e *entry) (err error) {
	ctx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("job panicked", "job", e.id, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return e.Run(ctx)
}
