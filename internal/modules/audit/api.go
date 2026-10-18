package audit

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// maxEntries bounds the entries an answer holds.
const maxEntries = 5000

// EntryBody is an entry of the log, as the API returns it.
type EntryBody struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Operation string    `json:"operation" doc:"The ID of the operation, such as certs-delete"`
	Method    string    `json:"method"`
	Path      string    `json:"path" doc:"The path below /api/v1"`
	Status    int       `json:"status" doc:"The status of the answer"`
	User      string    `json:"user,omitempty" doc:"The account signed in; none for a sign-in"`
	Client    string    `json:"client" doc:"The address of the client"`
	UserAgent string    `json:"userAgent,omitempty"`
}

type entriesInput struct {
	Days int `query:"days" minimum:"0" maximum:"36500" default:"30" doc:"How many days back to list; 0 for all"`
}

type entriesOutput struct {
	Body struct {
		Entries []EntryBody `json:"entries" doc:"The newest first"`
		More    bool        `json:"more" doc:"There are older ones in the time asked for than one answer holds"`
	}
}

func registerAPI(a huma.API, m *Module) {
	huma.Register(a, huma.Operation{
		OperationID: "audit-entries", Method: http.MethodGet, Path: "/entries",
		Summary:     "List what was done",
		Description: fmt.Sprintf("The operations of the last days that may have changed something, the newest first, at most %d.", maxEntries),
	}, func(ctx context.Context, in *entriesInput) (*entriesOutput, error) {
		var since time.Time
		if in.Days > 0 {
			since = m.now().Add(-time.Duration(in.Days) * 24 * time.Hour)
		}
		entries, more, err := m.entries(ctx, since, maxEntries)
		if err != nil {
			return nil, err
		}
		out := &entriesOutput{}
		out.Body.More = more
		out.Body.Entries = make([]EntryBody, 0, len(entries))
		for _, e := range entries {
			out.Body.Entries = append(out.Body.Entries, EntryBody{ID: e.id, At: e.at, Operation: e.operation, Method: e.method,
				Path: e.path, Status: e.status, User: e.user, Client: e.client, UserAgent: e.agent})
		}
		return out, nil
	})
}
