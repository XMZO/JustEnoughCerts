package telegram

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// How a bot listens: it polls the Bot API (getUpdates), which answers as
// soon as the chat sends something, or after m.wait; it skips what came
// while JEC was not listening, so that no old command, such as a
// confirmed renewal, runs late. Each update gets handleWithin.
const (
	handleWithin = time.Minute
	maxBackoff   = 5 * time.Minute
)

// serve keeps a listener for each bot whose settings ask it to answer
// commands, until ctx is done, looking at the settings again every
// m.every. Two bots of one token cannot both listen: the first does.
func (m *Module) serve(ctx context.Context) {
	running := map[int64]*listener{}
	defer func() {
		for _, l := range running {
			l.stop()
		}
	}()
	t := time.NewTicker(m.every)
	defer t.Stop()
	for {
		m.reconcile(ctx, running)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// reconcile starts and stops listeners to match the bots' settings.
func (m *Module) reconcile(ctx context.Context, running map[int64]*listener) {
	list, err := m.providers.List(ctx, providers.Notifier)
	if err != nil {
		m.log.Warn("list the Telegram bots", "err", err)
		return
	}
	want := map[int64]providers.Summary{}
	for _, s := range list {
		if s.Type == botTypeID && s.Config["commands"] == "true" {
			want[s.ID] = s
		}
	}
	for id, l := range running {
		if s, ok := want[id]; !ok || !s.UpdatedAt.Equal(l.updated) {
			l.stop()
			delete(running, id)
		}
	}
	tokens := map[string]int64{}
	for id, l := range running {
		tokens[l.bot.token] = id
	}
	for _, id := range slices.Sorted(maps.Keys(want)) {
		if running[id] != nil {
			continue
		}
		p, _, err := m.providers.Open(ctx, id)
		if err != nil {
			m.log.Warn("open a Telegram bot", "bot", id, "err", err)
			continue
		}
		b, ok := p.(*bot)
		if !ok {
			continue
		}
		if first, taken := tokens[b.token]; taken {
			m.log.Warn("two Telegram bots of one token answer commands: only the first listens", "bot", id, "first", first)
			_ = b.Close()
			continue
		}
		tokens[b.token] = id
		running[id] = m.listen(ctx, b, want[id].UpdatedAt)
	}
}

// listener answers the commands of a bot's chat.
type listener struct {
	m       *Module
	bot     *bot
	updated time.Time // of the settings it runs with
	tag     language.Tag
	poll    *http.Client // waits longer than a poll
	cancel  context.CancelFunc
	done    chan struct{}
}

func (m *Module) listen(ctx context.Context, b *bot, updated time.Time) *listener {
	ctx, cancel := context.WithCancel(ctx)
	l := &listener{m: m, bot: b, updated: updated, cancel: cancel, done: make(chan struct{}), tag: m.cat.Match()}
	if t, err := language.Parse(b.locale); err == nil {
		l.tag = m.cat.Match(t)
	}
	go func() {
		defer close(l.done)
		defer func() { _ = b.Close() }()
		l.run(ctx)
	}()
	return l
}

func (l *listener) stop() {
	l.cancel()
	<-l.done
}

// run polls the Bot API and handles what it gets, until ctx is done.
func (l *listener) run(ctx context.Context) {
	log := l.m.log.With("bot", l.bot.id)
	poll, err := httpx.ClientFor(httpx.Options{Timeout: l.m.wait + 15*time.Second}, l.bot.config)
	if err != nil {
		log.Warn("make the client of the Telegram bot", "err", err)
		return
	}
	defer poll.CloseIdleConnections()
	l.poll = poll
	if err := l.commands(ctx); err != nil && ctx.Err() == nil {
		log.Warn("set the commands of the Telegram bot", "err", err)
	}
	offset, err := l.skip(ctx)
	for err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Warn("poll the Telegram bot", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
		offset, err = l.skip(ctx)
	}
	wait := time.Second
	for ctx.Err() == nil {
		var updates []update
		err := l.bot.call(ctx, l.poll, "getUpdates", map[string]any{
			"offset": offset, "timeout": int(l.m.wait / time.Second), "allowed_updates": []string{"message", "callback_query"},
		}, &updates)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn("poll the Telegram bot", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(wait):
			}
			wait = min(wait*2, maxBackoff)
			continue
		}
		wait = time.Second
		for _, u := range updates {
			offset = u.ID + 1
			l.handle(ctx, u)
		}
	}
}

// skip returns the offset after the updates that came while JEC was not
// listening, which it lets go.
func (l *listener) skip(ctx context.Context) (int64, error) {
	var last []update
	if err := l.bot.call(ctx, l.poll, "getUpdates", map[string]any{"offset": -1, "timeout": 0}, &last); err != nil {
		return 0, err
	}
	if len(last) == 0 {
		return 0, nil
	}
	return last[len(last)-1].ID + 1, nil
}

// update is what the Bot API tells of: a message, or a button pressed.
type update struct {
	ID       int64     `json:"update_id"`
	Message  *message  `json:"message"`
	Callback *callback `json:"callback_query"`
}

type message struct {
	ID   int64  `json:"message_id"`
	Chat chat   `json:"chat"`
	Text string `json:"text"`
}

type chat struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type callback struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *message `json:"message"`
}

// commandViews are the views the commands open; any other command gets
// the help.
var commandViews = map[string]string{
	"start": viewMain, "menu": viewMain, "certs": viewList + ":0", "todo": viewTodo,
	"recent": viewRecent, "online": viewOnline, "help": viewHelp,
}

// handle answers u: a command with a new message, a button with the
// message it is on changed in place. Another chat learns only its ID, to
// set up the bot with, and only when it asks for the menu.
func (l *listener) handle(ctx context.Context, u update) {
	ctx, cancel := context.WithTimeout(ctx, handleWithin)
	defer cancel()
	log := l.m.log.With("bot", l.bot.id)
	switch {
	case u.Message != nil && strings.HasPrefix(u.Message.Text, "/"):
		c := u.Message.Chat
		cmd := command(u.Message.Text)
		if !l.allowed(c) {
			if cmd == "start" || cmd == "menu" {
				if err := l.send(ctx, c.ID, screen{text: l.text(i18n.TelegramChatNotAllowed, i18n.Args{"id": strconv.FormatInt(c.ID, 10)})}); err != nil {
					log.Debug("tell another chat its ID", "err", err)
				}
			}
			return
		}
		view, ok := commandViews[cmd]
		if !ok {
			view = viewHelp
		}
		if err := l.send(ctx, c.ID, l.show(ctx, view)); err != nil {
			log.Warn("answer a command", "err", err)
		}
	case u.Callback != nil && u.Callback.Message != nil:
		q := u.Callback
		if !l.allowed(q.Message.Chat) {
			l.answer(ctx, q.ID, l.text(i18n.TelegramChatNotAllowed, i18n.Args{"id": strconv.FormatInt(q.Message.Chat.ID, 10)}))
			return
		}
		s := l.show(ctx, q.Data)
		l.answer(ctx, q.ID, s.toast)
		if err := l.edit(ctx, q.Message.Chat.ID, q.Message.ID, s); err != nil {
			log.Warn("answer a button", "err", err)
		}
	}
}

// command is the name of the command of text: /certs@JecBot all → certs.
func command(text string) string {
	name, _, _ := strings.Cut(strings.TrimPrefix(strings.Fields(text + " ")[0], "/"), "@")
	return strings.ToLower(name)
}

// allowed tells whether c is the bot's chat: by its ID, or by the name of
// a public group.
func (l *listener) allowed(c chat) bool {
	return l.bot.chat == strconv.FormatInt(c.ID, 10) || (c.Username != "" && strings.EqualFold(l.bot.chat, "@"+c.Username))
}

// commands sets the commands Telegram lists in the bot's chat, in its
// language.
func (l *listener) commands(ctx context.Context) error {
	var list []map[string]string
	for _, c := range []struct {
		name string
		desc i18n.Key
	}{
		{"menu", i18n.TelegramCommandMenu}, {"certs", i18n.TelegramCommandCerts}, {"todo", i18n.TelegramCommandTodo},
		{"recent", i18n.TelegramCommandRecent}, {"online", i18n.TelegramCommandOnline}, {"help", i18n.TelegramCommandHelp},
	} {
		list = append(list, map[string]string{"command": c.name, "description": l.plain(c.desc, nil)})
	}
	params := map[string]any{"commands": list}
	if id, err := strconv.ParseInt(l.bot.chat, 10, 64); err == nil {
		params["scope"] = map[string]any{"type": "chat", "chat_id": id}
	}
	return l.bot.call(ctx, l.bot.client, "setMyCommands", params, nil)
}

// screen is what a message shows: its text in Telegram's HTML, its
// buttons by row, and for a button pressed, what to say over the chat.
type screen struct {
	text  string
	rows  [][]button
	toast string
}

type button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

func (s screen) params() map[string]any {
	p := map[string]any{"text": s.text, "parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true}}
	rows := s.rows
	if rows == nil {
		rows = [][]button{}
	}
	p["reply_markup"] = map[string]any{"inline_keyboard": rows}
	return p
}

func (l *listener) send(ctx context.Context, chatID int64, s screen) error {
	p := s.params()
	p["chat_id"] = chatID
	return l.bot.call(ctx, l.bot.client, "sendMessage", p, nil)
}

// edit changes a message to show s; the same once more, as on a refresh
// with nothing new, is no error.
func (l *listener) edit(ctx context.Context, chatID, messageID int64, s screen) error {
	p := s.params()
	p["chat_id"], p["message_id"] = chatID, messageID
	err := l.bot.call(ctx, l.bot.client, "editMessageText", p, nil)
	var problem *contracts.Problem
	if errors.As(err, &problem) {
		if desc, _ := problem.Params["description"].(string); strings.Contains(strings.ToLower(desc), "message is not modified") {
			return nil
		}
	}
	return err
}

// answer stops the spinner of a button pressed, saying text over the chat
// if there is any.
func (l *listener) answer(ctx context.Context, id, text string) {
	p := map[string]any{"callback_query_id": id}
	if text != "" {
		p["text"] = text
	}
	if err := l.bot.call(ctx, l.bot.client, "answerCallbackQuery", p, nil); err != nil {
		l.m.log.Debug("answer a callback", "bot", l.bot.id, "err", err)
	}
}
