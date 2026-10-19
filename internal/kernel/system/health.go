package system

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// HealthBody is how the work JEC does by itself goes, for people to see a
// failure that nothing else shows.
type HealthBody struct {
	Jobs []JobBody `json:"jobs" doc:"The jobs of the modules that are on, by name"`
	Disk *DiskBody `json:"disk,omitempty" doc:"The file system of the data directory, where the database, backups and key file are; missing where the server cannot tell"`
}

// JobBody is a job and its last run.
type JobBody struct {
	Name         string     `json:"name" doc:"<module>.<job>"`
	Module       string     `json:"module"`
	EverySeconds int64      `json:"everySeconds" doc:"How often it runs"`
	Running      bool       `json:"running"`
	StartedAt    *time.Time `json:"startedAt,omitempty" doc:"When its last run started; missing when it never ran"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty" doc:"When its last run ended; missing while it runs, or when the run was cut short"`
	Error        string     `json:"error,omitempty" doc:"Why its last run failed, as the job said it; empty when it succeeded"`
}

// DiskBody is the space of a file system, in bytes.
type DiskBody struct {
	Free  int64 `json:"free" doc:"Free to JEC"`
	Total int64 `json:"total"`
}

func registerHealth(a huma.API, o Options, tags []string) {
	huma.Register(a, huma.Operation{
		OperationID: "system-health", Method: http.MethodGet, Path: "/system/health", Tags: tags,
		Summary: "Tell how the background jobs went last, and the space left in the data directory",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body HealthBody }, error) {
		out := &struct{ Body HealthBody }{Body: HealthBody{Jobs: []JobBody{}}}
		if o.JobStatus != nil {
			list, err := o.JobStatus(ctx)
			if err != nil {
				return nil, err
			}
			for _, j := range list {
				b := JobBody{Name: j.Name, Module: j.Module, EverySeconds: int64(j.Every / time.Second), Running: j.Running, Error: j.Error}
				if !j.Started.IsZero() {
					b.StartedAt = &j.Started
				}
				if !j.Finished.IsZero() {
					b.FinishedAt = &j.Finished
				}
				out.Body.Jobs = append(out.Body.Jobs, b)
			}
		}
		if free, total, ok := diskSpace(o.DataDir); ok && o.DataDir != "" {
			out.Body.Disk = &DiskBody{Free: clamp(free), Total: clamp(total)}
		}
		return out, nil
	})
}

func clamp(n uint64) int64 {
	if n > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(n)
}
