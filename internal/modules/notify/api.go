package notify

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/api"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// SendBody is what was last sent through a notifier, as the API returns it.
type SendBody struct {
	Notifier int64              `json:"notifier" doc:"The notifier provider instance"`
	Event    string             `json:"event" doc:"The event the note was about, or notify.test for a test"`
	State    string             `json:"state" enum:"sent,retrying,failed,held" doc:"retrying: sent again with the next delivery of the event; failed: not sent again; held: waiting for the end of the notifier's quiet hours"`
	Attempts int                `json:"attempts"`
	Error    *contracts.Message `json:"error,omitempty" doc:"Why the last attempt failed"`
	At       time.Time          `json:"at"`
	SendAt   *time.Time         `json:"sendAt,omitempty" doc:"When a held note goes out"`
}

type sendsOutput struct {
	Body struct {
		Sends []SendBody `json:"sends" doc:"One for each notifier that sent something"`
	}
}

type notifierInput struct {
	ID int64 `path:"id"`
}

func registerAPI(a huma.API, s *service) {
	huma.Register(a, huma.Operation{
		OperationID: "notify-sends", Method: http.MethodGet, Path: "/sends",
		Summary: "The last note sent through each notifier",
	}, func(ctx context.Context, _ *struct{}) (*sendsOutput, error) {
		sends, err := s.latest(ctx)
		if err != nil {
			return nil, err
		}
		out := &sendsOutput{}
		out.Body.Sends = make([]SendBody, 0, len(sends))
		for _, x := range sends {
			b := SendBody{Notifier: x.Provider, Event: x.Event, State: x.State, Attempts: x.Attempts, At: x.At}
			if !x.SendAt.IsZero() {
				at := x.SendAt
				b.SendAt = &at
			}
			if x.Error.Key != "" {
				b.Error = &x.Error
			}
			out.Body.Sends = append(out.Body.Sends, b)
		}
		return out, nil
	})

	huma.Register(a, huma.Operation{
		OperationID: "notify-test", Method: http.MethodPost, Path: "/notifiers/{id}/test", DefaultStatus: http.StatusNoContent,
		Summary:     "Send a test note through a notifier",
		Description: "Sends at once, in the notifier's language. A notifier that fails answers 502 with its own message.",
	}, func(ctx context.Context, in *notifierInput) (*struct{}, error) {
		if p, err := s.prov.Get(ctx, in.ID); err == nil {
			api.Subject(ctx, p.Name)
		}
		return nil, s.test(ctx, in.ID)
	})
}

// test sends a test note through notifier id and says how it went, as an
// API error when it failed.
func (s *service) test(ctx context.Context, id int64) error {
	t, err := s.prov.Type(ctx, id)
	switch {
	case errors.Is(err, providers.ErrNotFound), errors.Is(err, providers.ErrUnavailable), err == nil && t.Kind != providers.Notifier:
		return api.NewError(http.StatusNotFound, i18n.KernelErrorNotFound, nil)
	case err != nil:
		return err
	}
	sendErr := s.send(ctx, id, s.testNote)
	if err := s.record(ctx, 0, id, contracts.NoteTest, sendErr); err != nil {
		return err
	}
	if sendErr != nil {
		m := problem(sendErr)
		return api.NewError(http.StatusBadGateway, m.Key, m.Params)
	}
	return nil
}
