package system

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
)

// The heartbeat: an address JEC asks for every few minutes while it runs,
// which a monitoring service, such as healthchecks.io or a push monitor of
// Uptime Kuma, waits for, to tell when JEC stopped.
const (
	heartbeatMeta  = "heartbeat"
	heartbeatEvery = 5 * time.Minute
)

// HeartbeatBody is the address of the heartbeat and how it went last.
type HeartbeatBody struct {
	URL   string     `json:"url,omitempty" doc:"Asked for with GET every five minutes while JEC runs; empty for none"`
	Last  *time.Time `json:"last,omitempty" doc:"When it was last asked for"`
	Error string     `json:"error,omitempty" doc:"What went wrong the last time, as the HTTP client said; empty when it answered"`
}

// beats is how the last heartbeat went, kept in memory.
type beats struct {
	mu   sync.Mutex
	last time.Time
	err  string
}

type heartbeatInput struct {
	Body struct {
		URL string `json:"url" maxLength:"2000" doc:"An http or https address; empty for none"`
	}
}

// registerHeartbeat adds PUT /system/heartbeat and the job that asks for
// the address.
func registerHeartbeat(a huma.API, o Options, client *http.Client, tags []string) {
	huma.Register(a, huma.Operation{
		OperationID: "set-heartbeat", Method: http.MethodPut, Path: "/system/heartbeat", Tags: tags,
		Summary: "Choose the address JEC asks for every five minutes while it runs, for a monitoring service to tell when it stopped",
	}, func(ctx context.Context, in *heartbeatInput) (*struct{ Body RuntimeBody }, error) {
		u := strings.TrimSpace(in.Body.URL)
		if p, err := url.Parse(u); u != "" && (err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "") {
			return nil, api.NewError(http.StatusUnprocessableEntity, i18n.KernelErrorInvalidRequest, nil).WithField("body.url", i18n.KernelErrorHeartbeatUrl)
		}
		_, err := o.DB.ExecContext(ctx,
			`INSERT INTO kernel_meta (name, value) VALUES (?, ?) ON CONFLICT (name) DO UPDATE SET value = excluded.value`,
			heartbeatMeta, []byte(u))
		if err != nil {
			return nil, err
		}
		o.beats.mu.Lock()
		o.beats.last, o.beats.err = time.Time{}, ""
		o.beats.mu.Unlock()
		if u != "" && o.Jobs != nil {
			o.Jobs.Trigger("heartbeat")
		}
		return &struct{ Body RuntimeBody }{Body: o.runtime(ctx)}, nil
	})
	if o.Jobs != nil {
		_ = o.Jobs.Add(jobs.Job{Name: "heartbeat", Every: heartbeatEvery, Timeout: time.Minute, Run: func(ctx context.Context) error {
			return o.beat(ctx, client)
		}})
	}
}

// heartbeatURL is the address of the heartbeat, "" for none.
func (o Options) heartbeatURL(ctx context.Context) (string, error) {
	if o.DB == nil {
		return "", nil
	}
	var v []byte
	err := o.DB.QueryRowContext(ctx, "SELECT value FROM kernel_meta WHERE name = ?", heartbeatMeta).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return string(v), err
}

// beat asks for the address of the heartbeat, if any. It fails, which the
// health of the jobs shows, when the address does not answer with 2xx.
func (o Options) beat(ctx context.Context, client *http.Client) error {
	u, err := o.heartbeatURL(ctx)
	if err != nil || u == "" {
		return err
	}
	err = get(ctx, client, u)
	o.beats.mu.Lock()
	o.beats.last, o.beats.err = o.Now(), ""
	if err != nil {
		o.beats.err = err.Error()
	}
	o.beats.mu.Unlock()
	return err
}

func get(ctx context.Context, client *http.Client, u string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
	_ = res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}

// heartbeatState is the heartbeat as the API tells it.
func (o Options) heartbeatState(ctx context.Context) HeartbeatBody {
	u, _ := o.heartbeatURL(ctx)
	b := HeartbeatBody{URL: u}
	if o.beats == nil {
		return b
	}
	o.beats.mu.Lock()
	defer o.beats.mu.Unlock()
	if !o.beats.last.IsZero() {
		last := o.beats.last.UTC()
		b.Last, b.Error = &last, o.beats.err
	}
	return b
}
