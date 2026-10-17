package certs

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
)

// queue runs one kind of background work on certificates, issuances or
// deployments, as a job: each run starts the due tasks, at most limit at
// once, takes on those that become due meanwhile, and returns once none is
// due or under way. The job never overlaps itself, so a task still marked
// running when a run starts was cut short by a stop of JEC: resume makes
// it due again.
type queue[T any] struct {
	limit   int
	timeout time.Duration // bounds an attempt
	wake    chan struct{} // new work while a run is under way
	// trigger starts the job when it is idle.
	trigger func()
	resume  func(ctx context.Context) error
	// claim marks the task due the longest as running and returns it; nil
	// when none is due.
	claim func(ctx context.Context) (*T, error)
	// key tells the attempts under way apart, so that deleting a
	// certificate stops its own.
	key func(*T) runKey
	// attempt tries a task once within attemptCtx. ctx ending means that
	// JEC stops: the task then stays running, to start over after the
	// restart.
	attempt func(ctx, attemptCtx context.Context, t *T)
}

// runKey names an attempt under way: its certificate, and what sets it
// apart from the certificate's other work.
type runKey struct {
	certificate int64
	part        string
}

func newQueue[T any](limit int, timeout time.Duration) *queue[T] {
	return &queue[T]{limit: limit, timeout: timeout, wake: make(chan struct{}, 1), trigger: func() {}}
}

// kick tells the queue that there is work: a run under way picks it up at
// once, and an idle queue starts.
func (q *queue[T]) kick() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
	q.trigger()
}

// run is a run of the queue's job.
func (q *queue[T]) run(ctx context.Context, s *service) error {
	if err := q.resume(ctx); err != nil {
		return err
	}
	ended := make(chan struct{}, q.limit)
	var wg sync.WaitGroup
	defer wg.Wait()
	running := 0
	for {
		for running < q.limit {
			t, err := q.claim(ctx)
			if err != nil {
				return err
			}
			if t == nil {
				break
			}
			running++
			key := q.key(t)
			attemptCtx, cancel := context.WithTimeout(ctx, q.timeout)
			s.mu.Lock()
			s.running[key] = cancel
			s.mu.Unlock()
			wg.Go(func() {
				defer func() {
					s.mu.Lock()
					delete(s.running, key)
					s.mu.Unlock()
					cancel()
					ended <- struct{}{}
				}()
				q.attempt(ctx, attemptCtx, t)
			})
		}
		if running == 0 {
			return nil
		}
		select {
		case <-ended:
			running--
		case <-q.wake:
		case <-ctx.Done():
			return nil
		}
	}
}

// stopAttempts cancels the attempts under way for certificate id.
func (s *service) stopAttempts(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, cancel := range s.running {
		if key.certificate == id {
			cancel()
		}
	}
}

// logTable is where the log of a kind of task is kept: the statements
// that add a line and read the lines of a task.
type logTable struct{ insert, lines string }

var (
	issuanceLogTable = logTable{
		insert: `INSERT INTO certs_issuance_log (issuance_id, seq, at, attempt, level, step, key, params, detail)
		 VALUES (?, (SELECT coalesce(max(seq), 0) + 1 FROM certs_issuance_log WHERE issuance_id = ?), ?, ?, ?, ?, ?, ?, ?)`,
		lines: `SELECT seq, at, attempt, level, step, key, params, detail FROM certs_issuance_log WHERE issuance_id = ? ORDER BY seq`,
	}
	deploymentLogTable = logTable{
		insert: `INSERT INTO certs_deployment_log (deployment_id, seq, at, attempt, level, step, key, params, detail)
		 VALUES (?, (SELECT coalesce(max(seq), 0) + 1 FROM certs_deployment_log WHERE deployment_id = ?), ?, ?, ?, ?, ?, ?, ?)`,
		lines: `SELECT seq, at, attempt, level, step, key, params, detail FROM certs_deployment_log WHERE deployment_id = ? ORDER BY seq`,
	}
)

// taskLog appends the lines of an attempt to the log of a task. It is
// safe for concurrent use.
type taskLog struct {
	s       *service
	t       logTable
	id      int64
	attempt int

	mu   sync.Mutex
	step contracts.Step // of the last line that had one
}

func (s *service) taskLogger(t logTable, id int64, attempt int) *taskLog {
	return &taskLog{s: s, t: t, id: id, attempt: attempt}
}

// current returns the step of the last line that had one.
func (l *taskLog) current() contracts.Step {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.step
}

// add stores line. A line that cannot be stored is logged instead: the log
// is for people, and the task goes on without it.
func (l *taskLog) add(line contracts.LogLine) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if line.Step != "" {
		l.step = line.Step
	}
	params := ""
	if len(line.Params) > 0 {
		b, err := json.Marshal(line.Params)
		if err != nil {
			l.s.log.Warn("task log params", "key", line.Key, "err", err)
		} else {
			params = string(b)
		}
	}
	detail := line.Detail
	if len(detail) > maxDetailLen {
		detail = detail[:maxDetailLen]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := l.s.db.ExecContext(ctx, l.t.insert,
		l.id, l.id, l.s.now().UnixMilli(), l.attempt, string(line.Level), string(line.Step), string(line.Key), params, detail)
	if err != nil {
		l.s.log.Warn("task log", "id", l.id, "key", line.Key, "err", err)
	}
}

// LogEntry is a stored line of a task's log.
type LogEntry struct {
	Seq     int
	At      time.Time
	Attempt int
	contracts.LogLine
}

// logLines returns the log of task id, in order.
func (s *service) logLines(ctx context.Context, t logTable, id int64) ([]LogEntry, error) {
	rows, err := s.db.QueryContext(ctx, t.lines, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		var at int64
		var level, step, params string
		// The key is scanned as it is: it was stored from a constant.
		if err := rows.Scan(&e.Seq, &at, &e.Attempt, &level, &step, &e.Key, &params, &e.Detail); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(at).UTC()
		e.Level, e.Step = contracts.Level(level), contracts.Step(step)
		if params != "" {
			if err := json.Unmarshal([]byte(params), &e.Params); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
