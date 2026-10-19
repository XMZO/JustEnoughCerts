package audit

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// pageSize is how many entries a page holds unless asked otherwise, and
// maxPage how many it may hold, as for an export.
const (
	pageSize = 50
	maxPage  = 5000
)

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
	Token     string    `json:"token,omitempty" doc:"The name of the API token it came with; none for a session"`
}

type entriesInput struct {
	Days   int    `query:"days" minimum:"0" maximum:"36500" default:"30" doc:"How many days back to list; 0 for all"`
	Result string `query:"result" enum:"ok,failed" doc:"Only the entries that succeeded, or only those that failed (status 400 and up)"`
	Text   string `query:"q" maxLength:"200" doc:"Only those holding this, letter case aside, in the path, the account, the address, the token or the operation's ID"`
	Ops    string `query:"ops" maxLength:"4000" doc:"Operations, by ID and comma separated, that also match the text: those whose names, as the interface words them, hold it"`
	Page   int    `query:"page" minimum:"1" default:"1"`
	Size   int    `query:"size" minimum:"1" maximum:"5000" default:"50" doc:"Entries a page holds"`
}

// CountsBody counts the entries of the time and text asked for, by result.
type CountsBody struct {
	All    int `json:"all"`
	OK     int `json:"ok"`
	Failed int `json:"failed"`
}

type entriesOutput struct {
	Body struct {
		Entries []EntryBody `json:"entries" doc:"The page asked for, the newest first"`
		Total   int         `json:"total" doc:"The entries of every page"`
		Counts  CountsBody  `json:"counts"`
	}
}

func registerAPI(a huma.API, m *Module) {
	huma.Register(a, huma.Operation{
		OperationID: "audit-entries", Method: http.MethodGet, Path: "/entries",
		Summary:     "List what was done",
		Description: "The operations of the last days that may have changed something, the newest first, a page at a time.",
	}, func(ctx context.Context, in *entriesInput) (*entriesOutput, error) {
		q := query{result: in.Result, text: in.Text, limit: min(in.Size, maxPage), offset: (in.Page - 1) * min(in.Size, maxPage)}
		if in.Days > 0 {
			q.since = m.now().Add(-time.Duration(in.Days) * 24 * time.Hour)
		}
		for op := range strings.SplitSeq(in.Ops, ",") {
			if op = strings.TrimSpace(op); op != "" {
				q.ops = append(q.ops, op)
			}
		}
		entries, t, err := m.find(ctx, q)
		if err != nil {
			return nil, err
		}
		out := &entriesOutput{}
		out.Body.Counts = CountsBody{All: t.all, OK: t.ok, Failed: t.failed}
		out.Body.Total = map[string]int{"ok": t.ok, "failed": t.failed}[in.Result]
		if in.Result == "" {
			out.Body.Total = t.all
		}
		out.Body.Entries = make([]EntryBody, 0, len(entries))
		for _, e := range entries {
			out.Body.Entries = append(out.Body.Entries, EntryBody{ID: e.id, At: e.at, Operation: e.operation, Method: e.method,
				Path: e.path, Status: e.status, User: e.user, Client: e.client, UserAgent: e.agent, Token: e.token})
		}
		return out, nil
	})
}
