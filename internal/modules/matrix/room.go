package matrix

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// maxText bounds the text of a message, in characters; homeservers take
// events of 64 KiB.
const maxText = 8000

const defaultServer = "https://matrix-client.matrix.org"

func roomType() providers.Type {
	return providers.Type{
		ID: "matrix.room", Kind: providers.Notifier, Name: i18n.MatrixRoomName, Description: i18n.MatrixRoomDescription,
		Tags: []string{"self_hosted"},
		Fields: append([]providers.Field{
			{Key: "server", Label: i18n.MatrixFieldServer, Help: i18n.MatrixFieldServerHelp, Kind: providers.URL, Default: defaultServer, Required: true},
			{Key: "token", Label: i18n.MatrixFieldToken, Help: i18n.MatrixFieldTokenHelp, Kind: providers.Text, Secret: true, Required: true},
			{Key: "room", Label: i18n.MatrixFieldRoom, Help: i18n.MatrixFieldRoomHelp, Kind: providers.Text, Required: true},
			{Key: "allow_private", Label: i18n.MatrixFieldAllowPrivate, Help: i18n.MatrixFieldAllowPrivateHelp, Kind: providers.Switch, Default: "false", Advanced: true},
			httpx.ProxyField(),
		}, contracts.NotifierFields()...),
	}
}

// room sends notes to a room through the client-server API.
type room struct {
	client *http.Client
	server *url.URL
	token  string
	room   string // an ID, !…:server, or an alias, #…:server

	mu sync.Mutex
	id string // the room's ID, once an alias is resolved
}

var _ contracts.Notifier = (*room)(nil)

// factory reads the settings of in. Only public addresses are reached
// unless the instance allows private ones.
func factory(_ context.Context, in providers.Instance) (any, error) {
	raw := cmp.Or(strings.TrimSpace(in.Config["server"]), defaultServer)
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.MatrixErrorServer, Params: map[string]any{"server": raw}})
	}
	r := strings.TrimSpace(in.Config["room"])
	if len(r) < 4 || r[0] != '!' && r[0] != '#' || !strings.Contains(r, ":") {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.MatrixErrorRoom, Params: map[string]any{"room": r}})
	}
	client, err := httpx.ClientFor(httpx.Options{PublicOnly: in.Config["allow_private"] != "true"}, in.Config)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &room{client: client, server: u, token: strings.TrimSpace(in.Value("token")), room: r}, nil
}

// Close lets go of idle connections.
func (r *room) Close() error {
	r.client.CloseIdleConnections()
	return nil
}

// apiError is the error body of the client-server API.
type apiError struct {
	Errcode      string `json:"errcode"`
	Error        string `json:"error"`
	RetryAfterMS int64  `json:"retry_after_ms"`
}

// call sends a request to the API and decodes the answer into out.
func (r *room) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return retry.Permanent(err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.server.String()+path, body)
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.MatrixErrorServer, Params: map[string]any{"server": r.server.Host}})
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.client.Do(req)
	if err != nil {
		if errors.Is(err, httpx.ErrNotPublic) {
			return retry.Permanent(&contracts.Problem{Key: i18n.MatrixErrorPrivate, Params: map[string]any{"host": r.server.Hostname()}})
		}
		return &contracts.Problem{Key: i18n.MatrixErrorNetwork, Params: map[string]any{"host": r.server.Host}, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 == 2 {
		if out != nil && json.Unmarshal(raw, out) != nil {
			return retry.Permanent(&contracts.Problem{Key: i18n.MatrixErrorApi, Params: map[string]any{"status": resp.StatusCode, "message": "not JSON"}})
		}
		return nil
	}
	var e apiError
	_ = json.Unmarshal(raw, &e)
	msg := cmp.Or(e.Error, e.Errcode, http.StatusText(resp.StatusCode))
	switch {
	case e.Errcode == "M_UNKNOWN_TOKEN" || e.Errcode == "M_MISSING_TOKEN" || resp.StatusCode == http.StatusUnauthorized:
		return retry.Permanent(&contracts.Problem{Key: i18n.MatrixErrorToken})
	case e.Errcode == "M_FORBIDDEN":
		return retry.Permanent(&contracts.Problem{Key: i18n.MatrixErrorForbidden, Params: map[string]any{"room": r.room, "message": msg}})
	case e.Errcode == "M_NOT_FOUND" && strings.HasPrefix(path, "/_matrix/client/v3/directory/"):
		return retry.Permanent(&contracts.Problem{Key: i18n.MatrixErrorRoom, Params: map[string]any{"room": r.room}})
	case resp.StatusCode == http.StatusTooManyRequests || e.Errcode == "M_LIMIT_EXCEEDED":
		return retry.After(&contracts.Problem{Key: i18n.MatrixErrorApi, Params: map[string]any{"status": resp.StatusCode, "message": msg}}, time.Duration(e.RetryAfterMS)*time.Millisecond)
	case resp.StatusCode >= 500:
		return &contracts.Problem{Key: i18n.MatrixErrorApi, Params: map[string]any{"status": resp.StatusCode, "message": msg}}
	}
	return retry.Permanent(&contracts.Problem{Key: i18n.MatrixErrorApi, Params: map[string]any{"status": resp.StatusCode, "message": msg}})
}

// roomID returns the ID of the room, resolving an alias once.
func (r *room) roomID(ctx context.Context) (string, error) {
	if r.room[0] == '!' {
		return r.room, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.id != "" {
		return r.id, nil
	}
	var out struct {
		RoomID string `json:"room_id"`
	}
	if err := r.call(ctx, http.MethodGet, "/_matrix/client/v3/directory/room/"+url.PathEscape(r.room), nil, &out); err != nil {
		return "", err
	}
	if out.RoomID == "" {
		return "", retry.Permanent(&contracts.Problem{Key: i18n.MatrixErrorRoom, Params: map[string]any{"room": r.room}})
	}
	r.id = out.RoomID
	return r.id, nil
}

// content is the event of n: a notice, as bots send, in plain text and in
// HTML with the subject in bold and a link to the page in JEC.
func content(n contracts.Note) map[string]any {
	text := n.Text
	if utf8.RuneCountInString(text) > maxText {
		text = string([]rune(text)[:maxText-1]) + "…"
	}
	subject := cmp.Or(n.Subject, n.Title)
	plain := []string{subject}
	rich := []string{"<strong>" + html.EscapeString(subject) + "</strong>"}
	if text != "" {
		plain = append(plain, text)
		rich = append(rich, strings.ReplaceAll(html.EscapeString(text), "\n", "<br>"))
	}
	if n.Link != "" {
		plain = append(plain, n.Link)
		l := html.EscapeString(n.Link)
		rich = append(rich, `<a href="`+l+`">`+l+`</a>`)
	}
	return map[string]any{"msgtype": "m.notice", "body": strings.Join(plain, "\n"), "format": "org.matrix.custom.html", "formatted_body": strings.Join(rich, "<br>")}
}

// Notify sends n to the room.
func (r *room) Notify(ctx context.Context, n contracts.Note) error {
	id, err := r.roomID(ctx)
	if err != nil {
		return err
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	path := "/_matrix/client/v3/rooms/" + url.PathEscape(id) + "/send/m.room.message/jec" + hex.EncodeToString(b)
	return r.call(ctx, http.MethodPut, path, content(n), nil)
}
