package system

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/jobs"
)

// Checking for releases by itself, once a day, only once someone turned it
// on: JEC calls home on its own never otherwise. A release newer than the
// running version is told of once (contracts.ReleaseAvailable).
const (
	updateCheckMeta = "update_check"
	updateToldMeta  = "update_told"
)

// UpdateCheckBody is the choice of checking for releases by itself.
type UpdateCheckBody struct {
	Enabled bool `json:"enabled" doc:"JEC asks GitHub once a day for a newer release, and tells of one through the notification channels"`
}

type updateCheckInput struct {
	Body UpdateCheckBody
}

// registerUpdates adds PUT /system/update-check, and the daily job.
func registerUpdates(a huma.API, o Options, client *http.Client, tags []string) {
	huma.Register(a, huma.Operation{
		OperationID: "set-update-check", Method: http.MethodPut, Path: "/system/update-check", Tags: tags,
		Summary: "Choose whether JEC checks for releases by itself, once a day",
	}, func(ctx context.Context, in *updateCheckInput) (*struct{ Body RuntimeBody }, error) {
		value := "0"
		if in.Body.Enabled {
			value = "1"
		}
		if err := o.setMeta(ctx, updateCheckMeta, value); err != nil {
			return nil, err
		}
		if in.Body.Enabled && o.Jobs != nil {
			o.Jobs.Trigger("release_check")
		}
		return &struct{ Body RuntimeBody }{Body: o.runtime(ctx)}, nil
	})
	if o.Jobs != nil {
		_ = o.Jobs.Add(jobs.Job{Name: "release_check", Every: 24 * time.Hour, Timeout: time.Minute, Run: func(ctx context.Context) error {
			return o.checkUpdates(ctx, client)
		}})
	}
}

// checkUpdates is the job release_check: when on, it asks for the releases
// and tells of a newer one, once.
func (o Options) checkUpdates(ctx context.Context, client *http.Client) error {
	if on, err := o.meta(ctx, updateCheckMeta); err != nil || on != "1" || o.Events == nil {
		return err
	}
	body, err := checkRelease(ctx, client, o.Version)
	if err != nil || !body.Newer {
		return err
	}
	told, err := o.meta(ctx, updateToldMeta)
	if err != nil || told == body.Latest {
		return err
	}
	ev := contracts.ReleaseEvent{Version: body.Latest, Current: body.Current, URL: body.URL}
	if err := o.Events.Publish(ctx, contracts.ReleaseAvailable, body.Latest, ev); err != nil {
		return err
	}
	return o.setMeta(ctx, updateToldMeta, body.Latest)
}

// meta reads a setting of the kernel, "" when there is none.
func (o Options) meta(ctx context.Context, name string) (string, error) {
	if o.DB == nil {
		return "", nil
	}
	var v []byte
	err := o.DB.QueryRowContext(ctx, "SELECT value FROM kernel_meta WHERE name = ?", name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return string(v), err
}

func (o Options) setMeta(ctx context.Context, name, value string) error {
	_, err := o.DB.ExecContext(ctx,
		`INSERT INTO kernel_meta (name, value) VALUES (?, ?) ON CONFLICT (name) DO UPDATE SET value = excluded.value`, name, []byte(value))
	return err
}
