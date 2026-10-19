package serverchan

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Where the APIs are: Turbo's, and ServerChan3's for the uid of a key.
const (
	turboBase = "https://sctapi.ftqq.com/"
	sc3Base   = "https://%s.push.ft07.com/send/"
)

// What ServerChan takes at most, in characters: a title, and the short
// text of the card that WeChat shows.
const (
	maxTitle = 32
	maxShort = 64
)

var (
	keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	// A key of ServerChan3 starts with sctp, the uid and t.
	sc3Pattern = regexp.MustCompile(`^sctp([0-9]+)t`)
)

func sendKeyType() providers.Type {
	return providers.Type{
		ID: "serverchan.sendkey", Kind: providers.Notifier, Name: i18n.ServerchanSendkeyName, Description: i18n.ServerchanSendkeyDescription, Untested: true,
		Tags: []string{"china"},
		Fields: append([]providers.Field{
			{Key: "key", Label: i18n.ServerchanFieldKey, Help: i18n.ServerchanFieldKeyHelp, Kind: providers.Text, Secret: true, Required: true},
		}, contracts.NotifierFields()...),
	}
}

// sendKey sends notes through a SendKey.
type sendKey struct {
	m   *Module
	key string
}

var _ contracts.Notifier = (*sendKey)(nil)

func (m *Module) factory(_ context.Context, in providers.Instance) (any, error) {
	return &sendKey{m: m, key: strings.TrimSpace(in.Value("key"))}, nil
}

// Close has nothing to release: the client is the module's.
func (*sendKey) Close() error { return nil }

// Notify sends n: its subject as the title, the first line of its text as
// what the card shows, and the whole text as the page behind the card.
func (k *sendKey) Notify(ctx context.Context, n contracts.Note) error {
	if !keyPattern.MatchString(k.key) {
		return retry.Permanent(&contracts.Problem{Key: i18n.ServerchanErrorKey})
	}
	title := cmp.Or(n.Subject, n.Title)
	first, _, _ := strings.Cut(n.Text, "\n")
	msg := map[string]any{"title": cut(title, maxTitle), "desp": page(title, n.Text), "short": cut(first, maxShort)}
	target := k.m.turbo + k.key + ".send"
	if uid := sc3Pattern.FindStringSubmatch(k.key); uid != nil {
		target = fmt.Sprintf(k.m.sc3, uid[1]) + k.key + ".send"
		msg["tags"] = "JEC"
	} else {
		// Turbo shows the address a message came from, unless told not to.
		msg["noip"] = 1
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return retry.Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.ServerchanErrorKey})
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := k.m.client.Do(req)
	if err != nil {
		// The key is in the URL: no error may carry it.
		return &contracts.Problem{Key: i18n.ServerchanErrorNetwork, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	status := resp.StatusCode
	if status == http.StatusTooManyRequests {
		// Too many calls from this address in a day.
		return &contracts.Problem{Key: i18n.ServerchanErrorThrottled}
	}
	var reply struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply)
	if err == nil && reply.Code == nil {
		err = errNoCode
	}
	if err != nil {
		// Not ServerChan, or a proxy in the way.
		p := &contracts.Problem{Key: i18n.ServerchanErrorStatus, Params: map[string]any{"status": status}, Err: err}
		if status >= 500 {
			return p
		}
		return retry.Permanent(p)
	}
	return explain(status, *reply.Code, reply.Message)
}

var errNoCode = errors.New("the answer has no code")

// explain turns ServerChan's answer into an error: one that trying again
// cannot fix, such as a wrong key, ends the attempts.
func explain(status, code int, message string) error {
	lower := strings.ToLower(message)
	switch {
	case code == 0:
		return nil
	case strings.Contains(lower, "key"), strings.Contains(lower, "token"):
		return retry.Permanent(&contracts.Problem{Key: i18n.ServerchanErrorKey})
	}
	p := &contracts.Problem{Key: i18n.ServerchanErrorApi, Params: map[string]any{"code": code, "message": message}}
	if status >= 500 {
		return p
	}
	return retry.Permanent(p)
}

// page is the Markdown of the page behind the card: each line of text as
// a paragraph, after title when the card has to cut it.
func page(title, text string) string {
	var paras []string
	if utf8.RuneCountInString(title) > maxTitle {
		paras = append(paras, markdown(title))
	}
	for line := range strings.SplitSeq(text, "\n") {
		if line != "" {
			paras = append(paras, markdown(line))
		}
	}
	return strings.Join(paras, "\n\n")
}

// urlPattern finds the links in a line, which escapes would break.
var urlPattern = regexp.MustCompile(`https?://[^\s<>]+`)

// markdown is line with what Markdown would read as formatting escaped,
// but for its links.
func markdown(line string) string {
	var b strings.Builder
	last := 0
	for _, loc := range urlPattern.FindAllStringIndex(line, -1) {
		b.WriteString(escape(line[last:loc[0]]))
		b.WriteString(line[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(escape(line[last:]))
	return b.String()
}

// special are the characters escaped in text: emphasis, code, links,
// HTML, strikethrough, tables and headings.
var special = string(rune(92)) + "`*_[]<>~|#"

func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(special, r) {
			b.WriteRune(92)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ellipsis ends what cut shortens.
var ellipsis = string(rune(0x2026))

// cut shortens s to n characters at most, the last an ellipsis.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + ellipsis
}
