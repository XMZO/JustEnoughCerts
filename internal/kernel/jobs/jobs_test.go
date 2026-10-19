package jobs

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/migrations"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/store"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type fixture struct {
	t     *testing.T
	db    *sql.DB
	clock *clock
	mu    sync.Mutex
	off   map[string]bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context(), "kernel", migrations.FS); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, db: st.DB(), clock: &clock{t: time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)}, off: map[string]bool{}}
}

// scheduler returns a scheduler with the given jobs of module "certs",
// loaded as at startup.
func (f *fixture) scheduler(jobs ...Job) *Scheduler {
	f.t.Helper()
	s := New(f.db, slog.New(slog.DiscardHandler), func(m string) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return !f.off[m]
	})
	s.now = f.clock.now
	for _, j := range jobs {
		if err := s.Scope("certs").Add(j); err != nil {
			f.t.Fatal(err)
		}
	}
	if err := s.load(f.t.Context()); err != nil {
		f.t.Fatal(err)
	}
	return s
}

// tick starts the due jobs and waits for them to end.
func (f *fixture) tick(s *Scheduler) int {
	n := s.tick(f.t.Context())
	s.wg.Wait()
	return n
}

type jobRow struct {
	started  time.Time
	finished bool
	err      string
}

func (f *fixture) row(name string) jobRow {
	f.t.Helper()
	var r jobRow
	var started int64
	var finished sql.NullInt64
	err := f.db.QueryRowContext(f.t.Context(), "SELECT started_at, finished_at, error FROM kernel_jobs WHERE name = ?", name).
		Scan(&started, &finished, &r.err)
	if err != nil {
		f.t.Fatalf("job %s: %v", name, err)
	}
	r.started, r.finished = time.UnixMilli(started).UTC(), finished.Valid
	return r
}

func counting(n *atomic.Int32) func(context.Context) error {
	return func(context.Context) error {
		n.Add(1)
		return nil
	}
}

func TestSchedule(t *testing.T) {
	f := newFixture(t)
	var runs atomic.Int32
	s := f.scheduler(Job{Name: "renew", Every: time.Hour, Run: counting(&runs)})
	start := f.clock.now()

	if f.tick(s) != 0 {
		t.Error("a job ran before the start delay")
	}
	f.clock.add(startDelay)
	if f.tick(s) != 1 || runs.Load() != 1 {
		t.Fatalf("the first run did not happen: %d runs", runs.Load())
	}
	if r := f.row("certs.renew"); !r.started.Equal(start.Add(startDelay)) || !r.finished || r.err != "" {
		t.Errorf("stored run: %+v", r)
	}
	f.clock.add(59 * time.Minute)
	if f.tick(s) != 0 {
		t.Error("the job ran again before its interval")
	}
	f.clock.add(time.Minute)
	if f.tick(s) != 1 || runs.Load() != 2 {
		t.Errorf("the second run did not happen: %d runs", runs.Load())
	}
}

func TestRestartKeepsSchedule(t *testing.T) {
	f := newFixture(t)
	var runs atomic.Int32
	job := Job{Name: "renew", Every: time.Hour, Run: counting(&runs)}
	s := f.scheduler(job)
	f.clock.add(startDelay)
	f.tick(s)
	ranAt := f.clock.now()

	// A restart 20 minutes later keeps the hourly schedule.
	f.clock.add(20 * time.Minute)
	s = f.scheduler(job)
	f.clock.add(startDelay)
	if f.tick(s) != 0 {
		t.Error("a restart made the job run early")
	}
	f.clock.t = ranAt.Add(time.Hour)
	if f.tick(s) != 1 {
		t.Error("the job did not run an hour after its last start")
	}

	// A restart after a long stop runs the overdue job soon.
	f.clock.add(5 * time.Hour)
	s = f.scheduler(job)
	if f.tick(s) != 0 {
		t.Error("an overdue job ran before the start delay")
	}
	f.clock.add(startDelay)
	if f.tick(s) != 1 || runs.Load() != 3 {
		t.Errorf("the overdue job did not run: %d runs", runs.Load())
	}
}

func TestModuleOff(t *testing.T) {
	f := newFixture(t)
	var runs atomic.Int32
	s := f.scheduler(Job{Name: "renew", Every: time.Hour, Run: counting(&runs)})
	f.off["certs"] = true
	f.clock.add(startDelay)
	if f.tick(s) != 0 {
		t.Fatal("a job of a module that is off ran")
	}
	f.mu.Lock()
	f.off["certs"] = false
	f.mu.Unlock()
	f.clock.add(recheck)
	if f.tick(s) != 1 || runs.Load() != 1 {
		t.Error("the job did not run once its module was back on")
	}
}

func TestNoOverlap(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	var runs atomic.Int32
	s := f.scheduler(Job{Name: "slow", Every: time.Minute, Timeout: time.Hour, Run: func(context.Context) error {
		runs.Add(1)
		<-release
		return nil
	}})
	f.clock.add(startDelay)
	if n := s.tick(t.Context()); n != 1 {
		t.Fatalf("started %d runs", n)
	}
	f.clock.add(2 * time.Minute)
	if n := s.tick(t.Context()); n != 0 {
		t.Error("a job overlapped itself")
	}
	close(release)
	s.wg.Wait()
	if f.tick(s) != 1 || runs.Load() != 2 {
		t.Errorf("the due job did not run after the slow run: %d runs", runs.Load())
	}
}

func TestFailures(t *testing.T) {
	f := newFixture(t)
	s := f.scheduler(
		Job{Name: "broken", Every: time.Hour, Run: func(context.Context) error { return errors.New("dns timeout") }},
		Job{Name: "panics", Every: time.Hour, Run: func(context.Context) error { panic("boom") }},
		Job{Name: "slow", Every: time.Hour, Timeout: 10 * time.Millisecond, Run: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}},
	)
	f.clock.add(startDelay)
	if n := f.tick(s); n != 3 {
		t.Fatalf("started %d runs", n)
	}
	for name, want := range map[string]string{
		"certs.broken": "dns timeout",
		"certs.panics": "panic: boom",
		"certs.slow":   context.DeadlineExceeded.Error(),
	} {
		if r := f.row(name); !r.finished || r.err != want {
			t.Errorf("%s: %+v, want error %q", name, r, want)
		}
	}
	f.clock.add(time.Hour)
	if n := f.tick(s); n != 3 {
		t.Errorf("failed jobs were not scheduled again: %d runs", n)
	}
}

func TestStatus(t *testing.T) {
	f := newFixture(t)
	var n atomic.Int32
	s := f.scheduler(
		Job{Name: "fine", Every: time.Hour, Run: counting(&n)},
		Job{Name: "broken", Every: 2 * time.Hour, Run: func(context.Context) error { return errors.New("dns timeout") }},
	)
	list, err := s.Status(t.Context())
	if err != nil || len(list) != 2 || !list[0].Started.IsZero() || list[0].Error != "" {
		t.Fatalf("before any run: %+v %v", list, err)
	}
	f.clock.add(startDelay)
	f.tick(s)
	list, err = s.Status(t.Context())
	if err != nil || len(list) != 2 {
		t.Fatalf("status %+v %v", list, err)
	}
	broken, fine := list[0], list[1]
	if broken.Name != "certs.broken" || broken.Module != "certs" || broken.Every != 2*time.Hour || broken.Error != "dns timeout" ||
		broken.Started.IsZero() || broken.Finished.IsZero() || broken.Running {
		t.Errorf("broken %+v", broken)
	}
	if fine.Name != "certs.fine" || fine.Error != "" || fine.Finished.IsZero() {
		t.Errorf("fine %+v", fine)
	}
	// The jobs of a module that is off are left out.
	f.mu.Lock()
	f.off["certs"] = true
	f.mu.Unlock()
	if list, err := s.Status(t.Context()); err != nil || len(list) != 0 {
		t.Errorf("with the module off: %+v %v", list, err)
	}
}

func TestShutdown(t *testing.T) {
	f := newFixture(t)
	started := make(chan struct{})
	s := f.scheduler(Job{Name: "long", Every: time.Hour, Run: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}})
	f.clock.add(startDelay)
	ctx, cancel := context.WithCancel(t.Context())
	s.tick(ctx)
	<-started
	cancel()
	s.wg.Wait()
	if r := f.row("certs.long"); r.finished || r.err != "" {
		t.Errorf("a run cut short by shutdown looks finished: %+v", r)
	}
}

func TestAddChecks(t *testing.T) {
	f := newFixture(t)
	run := func(context.Context) error { return nil }
	s := New(f.db, slog.New(slog.DiscardHandler), func(string) bool { return true })
	if err := s.Scope("certs").Add(Job{Name: "renew", Every: time.Hour, Run: run}); err != nil {
		t.Fatal(err)
	}
	for name, job := range map[string]Job{
		"duplicate":   {Name: "renew", Every: time.Hour, Run: run},
		"bad name":    {Name: "Renew", Every: time.Hour, Run: run},
		"no interval": {Name: "x", Run: run},
		"negative":    {Name: "x", Every: time.Hour, Timeout: -time.Second, Run: run},
		"no run":      {Name: "x", Every: time.Hour},
	} {
		if err := s.Scope("certs").Add(job); err == nil {
			t.Errorf("%s: added", name)
		}
	}
	if err := s.Scope("other").Add(Job{Name: "renew", Every: time.Hour, Run: run}); err != nil {
		t.Errorf("the same name in another module: %v", err)
	}
	if err := s.load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Scope("certs").Add(Job{Name: "late", Every: time.Hour, Run: run}); err == nil {
		t.Error("added a job after the scheduler started")
	}
}

func TestRun(t *testing.T) {
	f := newFixture(t)
	ran := make(chan struct{}, 1)
	s := New(f.db, slog.New(slog.DiscardHandler), func(string) bool { return true })
	s.startDelay = 0
	if err := s.Scope("certs").Add(Job{Name: "renew", Every: time.Hour, Run: func(context.Context) error {
		ran <- struct{}{}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not run the job")
	}
	cancel()
	<-done
}

func TestTrigger(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	var runs atomic.Int32
	s := f.scheduler(Job{Name: "issue", Every: time.Hour, Run: func(context.Context) error {
		if runs.Add(1) == 1 {
			<-release
		}
		return nil
	}})
	scope := s.Scope("certs")

	// Before its time, a trigger makes the job due.
	scope.Trigger("issue")
	if n := s.tick(t.Context()); n != 1 {
		t.Fatalf("a triggered job did not start: %d", n)
	}
	// A trigger while it runs makes it run again once it ends.
	scope.Trigger("issue")
	if n := s.tick(t.Context()); n != 0 {
		t.Error("a triggered job overlapped itself")
	}
	// Forget the wake-ups of the triggers, to see the one the run's end sends.
	for len(s.kick) > 0 {
		<-s.kick
	}
	close(release)
	s.wg.Wait()
	select {
	case <-s.kick:
	case <-time.After(5 * time.Second):
		t.Fatal("the end of the run did not wake the scheduler")
	}
	if f.tick(s) != 1 || runs.Load() != 2 {
		t.Errorf("the job triggered while running did not run again: %d runs", runs.Load())
	}
	if f.tick(s) != 0 {
		t.Error("the job ran a third time")
	}
	scope.Trigger("nonexistent") // no job of that name: nothing happens
}

// TestWorkers: a worker runs while its module is on, stops when it goes
// off and starts again when it is back; one that returns on its own starts
// again after a pause.
func TestWorkers(t *testing.T) {
	f := newFixture(t)
	s := f.scheduler()
	started := make(chan struct{}, 4)
	var quit atomic.Bool
	if err := s.Scope("certs").Work(Worker{Name: "listen", Run: func(ctx context.Context) {
		started <- struct{}{}
		if quit.Load() {
			return
		}
		<-ctx.Done()
	}}); err == nil {
		t.Fatal("a worker added once the scheduler ran")
	}
	s = New(f.db, slog.New(slog.DiscardHandler), func(m string) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return !f.off[m]
	})
	s.now = f.clock.now
	if err := s.Scope("certs").Work(Worker{Name: "listen", Run: func(ctx context.Context) {
		started <- struct{}{}
		if quit.Load() {
			return
		}
		<-ctx.Done()
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Scope("certs").Work(Worker{Name: "listen", Run: func(context.Context) {}}); err == nil {
		t.Error("a worker added twice")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wait := func(what string) {
		t.Helper()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatalf("the worker did not start %s", what)
		}
	}
	settle := func() {
		// Until the worker's goroutine is seen to have returned, if it did.
		for range 100 {
			s.supervise(ctx)
			time.Sleep(10 * time.Millisecond)
		}
	}

	s.supervise(ctx)
	wait("at first")
	f.mu.Lock()
	f.off["certs"] = true
	f.mu.Unlock()
	settle()
	f.mu.Lock()
	f.off["certs"] = false
	f.mu.Unlock()
	s.supervise(ctx)
	wait("once its module was back on")

	quit.Store(true)
	f.mu.Lock()
	f.off["certs"] = true
	f.mu.Unlock()
	settle()
	f.mu.Lock()
	f.off["certs"] = false
	f.mu.Unlock()
	s.supervise(ctx)
	wait("after it was stopped")
	settle()
	if len(started) != 0 {
		t.Fatal("the worker started again at once after it returned")
	}
	f.clock.add(workerPause)
	s.supervise(ctx)
	wait("after the pause")
	cancel()
	s.wg.Wait()
}
