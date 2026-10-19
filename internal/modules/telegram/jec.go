package telegram

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

// What the menu reads of JEC, through its own API (module.Registry.Call):
// only the fields it shows, so that the module depends on the shape of the
// API alone, not on the modules behind it.

type certificate struct {
	ID             int64        `json:"id"`
	Domains        []string     `json:"domains"`
	State          string       `json:"state"`
	Source         string       `json:"source"`
	SourceProvider int64        `json:"sourceProvider"`
	SolverProvider int64        `json:"solverProvider"`
	RenewAt        *time.Time   `json:"renewAt"`
	Current        *version     `json:"current"`
	Targets        []int64      `json:"targets"`
	Deployments    []deployment `json:"deployments"`
	Issuance       *issuance    `json:"issuance"`
}

type version struct {
	ID       int64     `json:"id"`
	NotAfter time.Time `json:"notAfter"`
	Issuer   string    `json:"issuer"`
}

type deployment struct {
	Target     int64       `json:"target"`
	VersionID  int64       `json:"versionId"`
	State      string      `json:"state"`
	NextAt     *time.Time  `json:"nextAt"`
	FinishedAt *time.Time  `json:"finishedAt"`
	Error      *apiMessage `json:"error"`
}

type issuance struct {
	ID         int64       `json:"id"`
	State      string      `json:"state"`
	Reason     string      `json:"reason"`
	CreatedAt  time.Time   `json:"createdAt"`
	FinishedAt *time.Time  `json:"finishedAt"`
	NextAt     *time.Time  `json:"nextAt"`
	Error      *apiMessage `json:"error"`
}

// apiMessage is a message of the API, by key: why something failed.
type apiMessage struct {
	Key    i18n.Key  `json:"key"`
	Params i18n.Args `json:"params"`
}

type instance struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type checks struct {
	Tracked bool       `json:"tracked"`
	CheckAt *time.Time `json:"checkAt"`
	Results []check    `json:"results"`
}

type check struct {
	Domain    string     `json:"domain"`
	View      string     `json:"view"`
	State     string     `json:"state"`
	Issuer    string     `json:"issuer"`
	NotAfter  *time.Time `json:"notAfter"`
	CheckedAt time.Time  `json:"checkedAt"`
}

type tally struct {
	CertificateID int64 `json:"certificateId"`
	Old           int   `json:"old"`
	Unreachable   int   `json:"unreachable"`
}

type run struct {
	Certificate int64       `json:"certificate"`
	Domain      string      `json:"domain"`
	Action      string      `json:"action"`
	Trigger     string      `json:"trigger"`
	State       string      `json:"state"`
	Target      int64       `json:"target"`
	Error       *apiMessage `json:"error"`
	CreatedAt   time.Time   `json:"createdAt"`
}

type manifest struct {
	Version  string `json:"version"`
	Platform string `json:"platform"`
	TimeZone string `json:"timeZone"`
	Modules  []struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	} `json:"modules"`
}

// get reads path of the API into out, as the bot.
func (l *listener) get(ctx context.Context, path string, out any) error {
	return l.m.call(ctx, l.bot.name, http.MethodGet, path, nil, out)
}

// post asks the API to do path, as the bot.
func (l *listener) post(ctx context.Context, path string, body any) error {
	return l.m.call(ctx, l.bot.name, http.MethodPost, path, body, nil)
}

func (l *listener) certificates(ctx context.Context) ([]certificate, error) {
	var out struct {
		Certificates []certificate `json:"certificates"`
	}
	return out.Certificates, l.get(ctx, "/certs/certificates", &out)
}

func (l *listener) certificate(ctx context.Context, id int64) (certificate, error) {
	var out struct {
		Certificate certificate `json:"certificate"`
	}
	return out.Certificate, l.get(ctx, "/certs/certificates/"+strconv.FormatInt(id, 10), &out)
}

// names returns the names of the providers, by ID.
func (l *listener) names(ctx context.Context) map[int64]instance {
	var out struct {
		Providers []instance `json:"providers"`
	}
	byID := map[int64]instance{}
	if err := l.get(ctx, "/providers", &out); err != nil {
		return byID
	}
	for _, p := range out.Providers {
		byID[p.ID] = p
	}
	return byID
}
