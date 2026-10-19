package system

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
)

func TestHealth(t *testing.T) {
	started := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)
	_, api := humatest.New(t)
	Register(api, Options{
		DataDir: t.TempDir(),
		JobStatus: func(context.Context) ([]jobs.Status, error) {
			return []jobs.Status{
				{Name: "certs.renew", Module: "certs", Every: time.Hour, Started: started, Finished: started.Add(time.Second)},
				{Name: "watch.domains", Module: "watch", Every: 10 * time.Minute, Started: started, Finished: started.Add(time.Minute), Error: "rdap: timeout"},
				{Name: "monitor.check", Module: "monitor", Every: time.Minute, Running: true, Started: started},
				{Name: "calendar.never", Module: "calendar", Every: 24 * time.Hour},
			}, nil
		},
	})
	resp := api.Get("/system/health")
	var h HealthBody
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &h) != nil || len(h.Jobs) != 4 {
		t.Fatalf("GET /system/health = %d %s", resp.Code, resp.Body)
	}
	if j := h.Jobs[1]; j.Name != "watch.domains" || j.Module != "watch" || j.EverySeconds != 600 || j.Error != "rdap: timeout" ||
		j.StartedAt == nil || j.FinishedAt == nil || !j.FinishedAt.Equal(started.Add(time.Minute)) {
		t.Errorf("failed job %+v", j)
	}
	if j := h.Jobs[2]; !j.Running || j.FinishedAt != nil {
		t.Errorf("running job %+v", j)
	}
	if j := h.Jobs[3]; j.StartedAt != nil || j.FinishedAt != nil {
		t.Errorf("job that never ran %+v", j)
	}
	if h.Disk == nil || h.Disk.Total <= 0 || h.Disk.Free < 0 || h.Disk.Free > h.Disk.Total {
		t.Errorf("disk %+v", h.Disk)
	}
}
