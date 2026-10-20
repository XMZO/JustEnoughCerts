package pushplus

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// endpoint is where messages go.
var endpoint = "https://www.pushplus.plus/send"

// Bounds of a message, in characters: PushPlus takes titles of 100 and
// contents of 20000.
const (
	maxTitle   = 100
	maxContent = 8000
)

// codes PushPlus answers with.
const (
	codeOK      = 200
	codeServer  = 999 // its own failure, for later
	codeBadUser = 903 // a token it does not know
)

// tokenPattern is what a token of PushPlus looks like.
var tokenPattern = regexp.MustCompile(`^[0-9A-Za-z]{16,64}$`)

func tokenType() providers.Type {
	return providers.Type{
		ID: "pushplus.token", Kind: providers.Notifier, Name: i18n.PushplusTokenName, Description: i18n.PushplusTokenDescription, Untested: true,
		Fields: append([]providers.Field{
			{Key: "token", Label: i18n.PushplusFieldToken, Help: i18n.PushplusFieldTokenHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "topic", Label: i18n.PushplusFieldTopic, Help: i18n.PushplusFieldTopicHelp, Kind: providers.Text},
			{Key: "channel", Label: i18n.PushplusFieldChannel, Kind: providers.Select, Default: "wechat", Advanced: true, Options: []providers.Option{
				{Value: "wechat", Label: i18n.PushplusChannelWechat},
				{Value: "cp", Label: i18n.PushplusChannelCp},
				{Value: "mail", Label: i18n.PushplusChannelMail},
				{Value: "app", Label: i18n.PushplusChannelApp},
			}},
		}, contracts.NotifierFields()...),
	}
}

// pusher sends notes through PushPlus.
type pusher struct {
	client                *http.Client
	token, topic, channel string
}

var _ contracts.Notifier = (*pusher)(nil)

func factory(_ context.Context, in providers.Instance) (any, error) {
	token := strings.TrimSpace(in.Value("token"))
	if !tokenPattern.MatchString(token) {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.PushplusErrorToken})
	}
	client, err := httpx.ClientFor(httpx.Options{PublicOnly: true}, in.Config)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &pusher{client: client, token: token, topic: strings.TrimSpace(in.Config["topic"]), channel: cmp.Or(in.Config["channel"], "wechat")}, nil
}

// Close lets go of idle connections.
func (p *pusher) Close() error {
	p.client.CloseIdleConnections()
	return nil
}

func cut(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit-1]) + "…"
}

// content is the text of n as HTML, line by line, with the link to the page
// in JEC.
func content(n contracts.Note) string {
	var b strings.Builder
	for i, line := range strings.Split(cut(cmp.Or(n.Text, n.Title), maxContent), "\n") {
		if i > 0 {
			b.WriteString("<br>")
		}
		b.WriteString(html.EscapeString(line))
	}
	if n.Link != "" {
		l := html.EscapeString(n.Link)
		b.WriteString(`<br><br><a href="` + l + `">` + l + `</a>`)
	}
	return b.String()
}

// Notify sends n: its subject as the title.
func (p *pusher) Notify(ctx context.Context, n contracts.Note) error {
	msg := map[string]any{"token": p.token, "title": cut(cmp.Or(n.Subject, n.Title), maxTitle), "content": content(n), "template": "html", "channel": p.channel}
	if p.topic != "" {
		msg["topic"] = p.topic
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return retry.Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return &contracts.Problem{Key: i18n.PushplusErrorNetwork, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	var reply struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if resp.StatusCode/100 != 2 || json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&reply) != nil {
		pr := &contracts.Problem{Key: i18n.PushplusErrorApi, Params: map[string]any{"code": resp.StatusCode, "message": http.StatusText(resp.StatusCode)}}
		if resp.StatusCode >= 500 {
			return pr
		}
		return retry.Permanent(pr)
	}
	switch reply.Code {
	case codeOK:
		return nil
	case codeBadUser:
		return retry.Permanent(&contracts.Problem{Key: i18n.PushplusErrorToken})
	case codeServer:
		return &contracts.Problem{Key: i18n.PushplusErrorApi, Params: map[string]any{"code": reply.Code, "message": reply.Msg}}
	}
	return retry.Permanent(&contracts.Problem{Key: i18n.PushplusErrorApi, Params: map[string]any{"code": reply.Code, "message": reply.Msg}})
}
