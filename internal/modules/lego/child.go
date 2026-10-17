package lego

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"time"

	"github.com/go-acme/lego/v5/challenge"
	legolog "github.com/go-acme/lego/v5/log"
	"github.com/go-acme/lego/v5/providers/dns"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/children"
)

// The parent and its child exchange one JSON object per line: the child
// says it is ready, then answers each request in turn until its stdin
// closes.

const childName = "lego-dns"

// ready is the first line of a child: its timing, or why it cannot work.
type ready struct {
	Timeout  time.Duration `json:"timeout,omitempty"`
	Interval time.Duration `json:"interval,omitempty"`
	Error    string        `json:"error,omitempty"`
}

// request asks the child to present or clean up a challenge.
type request struct {
	Op      string `json:"op"` // "present" or "cleanup"
	Domain  string `json:"domain"`
	Token   string `json:"token"`
	KeyAuth string `json:"keyAuth"`
}

type reply struct {
	Error string `json:"error,omitempty"`
}

// lego's defaults for a provider without a Timeout method.
const (
	defaultTimeout  = 60 * time.Second
	defaultInterval = 2 * time.Second
)

func init() { children.Register(childName, runChild) }

// runChild is the child: it makes the provider named by args[0] from its
// environment, then serves requests.
func runChild(ctx context.Context, args []string, in io.Reader, out io.Writer, log io.Writer) int {
	legolog.SetDefault(slog.New(slog.NewTextHandler(log, nil)))
	enc := json.NewEncoder(out)
	fail := func(err error) int {
		_ = enc.Encode(ready{Error: err.Error()})
		return 1
	}
	if len(args) != 1 {
		return fail(errors.New("want the code of one DNS provider"))
	}
	if code := args[0]; slices.Contains(excluded, code) {
		return fail(fmt.Errorf("the %s provider is not offered", code))
	}
	p, err := dns.NewDNSChallengeProviderByName(args[0])
	if err != nil {
		return fail(err)
	}
	r := ready{Timeout: defaultTimeout, Interval: defaultInterval}
	if t, ok := p.(challenge.ProviderTimeout); ok {
		r.Timeout, r.Interval = t.Timeout()
	}
	if err := enc.Encode(r); err != nil {
		return 1
	}

	dec := json.NewDecoder(in)
	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return 0
			}
			return 1
		}
		var err error
		switch req.Op {
		case "present":
			err = p.Present(ctx, req.Domain, req.Token, req.KeyAuth)
		case "cleanup":
			err = p.CleanUp(ctx, req.Domain, req.Token, req.KeyAuth)
		default:
			err = fmt.Errorf("unknown request %q", req.Op)
		}
		var rep reply
		if err != nil {
			rep.Error = err.Error()
		}
		if err := enc.Encode(rep); err != nil {
			return 1
		}
	}
}

// excluded are the providers JEC does not offer, as tools/legocatalog
// leaves them out of the catalog.
var excluded = []string{"manual", "exec"}
