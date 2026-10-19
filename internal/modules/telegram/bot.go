package telegram

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// defaultAPI is Telegram's Bot API server.
const defaultAPI = "https://api.telegram.org"

// botTypeID is the type of the bots.
const botTypeID = "telegram.bot"

func botType() providers.Type {
	return providers.Type{
		ID: botTypeID, Kind: providers.Notifier, Name: i18n.TelegramBotName, Description: i18n.TelegramBotDescription, Untested: true,
		Tags: []string{"popular"},
		Fields: append([]providers.Field{
			{Key: "token", Label: i18n.TelegramFieldToken, Help: i18n.TelegramFieldTokenHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "chat", Label: i18n.TelegramFieldChat, Help: i18n.TelegramFieldChatHelp, Kind: providers.Text, Required: true},
			{Key: "commands", Label: i18n.TelegramFieldCommands, Help: i18n.TelegramFieldCommandsHelp, Kind: providers.Switch, Default: "false"},
			{Key: "actions", Label: i18n.TelegramFieldActions, Help: i18n.TelegramFieldActionsHelp, Kind: providers.Switch, Default: "false",
				When: &providers.When{Key: "commands", Value: "true"}},
			httpx.ProxyField(),
			{Key: "api", Label: i18n.TelegramFieldApi, Help: i18n.TelegramFieldApiHelp, Kind: providers.URL, Default: defaultAPI, Advanced: true},
		}, contracts.NotifierFields()...),
	}
}

// bot sends notes to a chat through a Telegram bot, and answers the
// commands of that chat when its settings ask for it (listen.go).
type bot struct {
	client *http.Client
	api    string
	token  string
	chat   string

	id       int64
	name     string
	locale   string
	commands bool // answers commands with a menu
	actions  bool // and lets the menu renew, deploy and check
	config   map[string]string
	// cat writes the buttons under notes, in the bot's language.
	cat *i18n.Catalog
}

var _ contracts.Notifier = (*bot)(nil)

func factory(_ context.Context, in providers.Instance) (any, error) {
	client, err := httpx.ClientFor(httpx.Options{}, in.Config)
	if err != nil {
		return nil, err
	}
	cat, err := i18n.Default()
	if err != nil {
		return nil, err
	}
	return &bot{
		cat:    cat,
		client: client,
		api:    strings.TrimRight(cmp.Or(in.Config["api"], defaultAPI), "/"),
		token:  strings.TrimSpace(in.Value("token")),
		chat:   strings.TrimSpace(in.Config["chat"]),

		id: in.ID, name: in.Name, locale: in.Config["locale"], config: in.Config,
		commands: in.Config["commands"] == "true", actions: in.Config["actions"] == "true",
	}, nil
}

// Close lets go of idle connections.
func (b *bot) Close() error {
	b.client.CloseIdleConnections()
	return nil
}

// reply is the answer of the Bot API.
type reply struct {
	OK          bool   `json:"ok"`
	Code        int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// Notify sends n as a message: its title in bold, then its text. When the
// bot answers commands, a button under it opens what it is about in the
// menu, in a message of its own.
func (b *bot) Notify(ctx context.Context, n contracts.Note) error {
	text := "<b>" + html.EscapeString(n.Title) + "</b>"
	if n.Text != "" {
		text += "\n\n" + html.EscapeString(n.Text)
	}
	params := map[string]any{
		"chat_id": b.chat, "text": text, "parse_mode": "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true},
	}
	if btn, ok := b.noteButton(n); ok {
		params["reply_markup"] = map[string]any{"inline_keyboard": [][]button{{btn}}}
	}
	body, err := json.Marshal(params)
	if err != nil {
		return retry.Permanent(err)
	}
	// The token is part of the path: no error may carry the URL.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.api+"/bot"+b.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.TelegramErrorApiUrl, Params: map[string]any{"url": b.api}})
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return &contracts.Problem{Key: i18n.TelegramErrorNetwork, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	var r reply
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		// Not the Bot API, or a proxy in the way.
		p := &contracts.Problem{Key: i18n.TelegramErrorStatus, Params: map[string]any{"status": resp.StatusCode}, Err: err}
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return retry.Permanent(p)
		}
		return p
	}
	if r.OK {
		return nil
	}
	return explain(resp.StatusCode, r)
}

// call calls method of the Bot API with params, decoding its result into
// out unless nil, through client, the bot's or one that waits longer.
func (b *bot) call(ctx context.Context, client *http.Client, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return retry.Permanent(err)
	}
	// The token is part of the path: no error may carry the URL.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.api+"/bot"+b.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.TelegramErrorApiUrl, Params: map[string]any{"url": b.api}})
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return &contracts.Problem{Key: i18n.TelegramErrorNetwork, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	var r struct {
		reply
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&r); err != nil {
		return &contracts.Problem{Key: i18n.TelegramErrorStatus, Params: map[string]any{"status": resp.StatusCode}, Err: err}
	}
	if !r.OK {
		return explain(resp.StatusCode, r.reply)
	}
	if out == nil || len(r.Result) == 0 {
		return nil
	}
	return json.Unmarshal(r.Result, out)
}

// explain turns a refusal of the Bot API into a Problem: one that trying
// again cannot fix, such as a revoked token, ends the attempts.
func explain(status int, r reply) error {
	code := cmp.Or(r.Code, status)
	desc := r.Description
	switch {
	case code == http.StatusUnauthorized || code == http.StatusNotFound:
		return retry.Permanent(&contracts.Problem{Key: i18n.TelegramErrorToken})
	case code == http.StatusBadRequest && strings.Contains(strings.ToLower(desc), "chat not found"):
		return retry.Permanent(&contracts.Problem{Key: i18n.TelegramErrorChat})
	case code == http.StatusForbidden:
		return retry.Permanent(&contracts.Problem{Key: i18n.TelegramErrorForbidden, Params: map[string]any{"description": desc}})
	case code == http.StatusTooManyRequests:
		wait := time.Duration(max(r.Parameters.RetryAfter, 1)) * time.Second
		return retry.After(&contracts.Problem{Key: i18n.TelegramErrorThrottled}, wait)
	}
	p := &contracts.Problem{Key: i18n.TelegramErrorApi, Params: map[string]any{"status": code, "description": desc}}
	if code >= 500 {
		return p
	}
	return retry.Permanent(p)
}

// noteButton is the button under note n that opens what it is about in the
// menu: a certificate, the watched sites and domains, or the menu itself.
func (b *bot) noteButton(n contracts.Note) (button, bool) {
	if !b.commands || b.cat == nil {
		return button{}, false
	}
	tag := b.cat.Match(language.Make(b.locale))
	label := func(icon string, key i18n.Key) string {
		s, _ := b.cat.Format(tag, key, nil)
		return icon + " " + s
	}
	switch n.Event {
	case contracts.SiteExpiring, contracts.DomainExpiring:
		return button{Text: label(iconSites, i18n.TelegramButtonSites), Data: viewNew + ":" + viewSites}, true
	case contracts.CertificatesDigest:
		return button{Text: label(iconHome, i18n.TelegramButtonMenu), Data: viewNew + ":" + viewMain}, true
	case contracts.NoteTest:
		return button{}, false
	}
	var d struct {
		CertificateID int64 `json:"certificateId"`
	}
	if json.Unmarshal(n.Data, &d) != nil || d.CertificateID == 0 {
		return button{}, false
	}
	return button{Text: label(iconLock, i18n.TelegramButtonCertificate), Data: viewNew + ":" + viewCert + ":" + strconv.FormatInt(d.CertificateID, 10)}, true
}
