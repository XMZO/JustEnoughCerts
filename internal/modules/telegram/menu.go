package telegram

import (
	"context"
	"errors"
	"html"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

// The menu: a main view of how the certificates are, and views of them
// all, of those needing attention, of the recent activity, of the online
// checks, of the system and of the help; a certificate's own view, which,
// when the bot allows actions, renews it, deploys it again and checks it
// now, the first two once confirmed. A button's data names the view, and
// an ID for those of one certificate.
const (
	viewMain   = "m"
	viewList   = "l" // l:<page>
	viewTodo   = "t"
	viewRecent = "r"
	viewOnline = "o"
	viewSystem = "s"
	viewHelp   = "h"
	viewSites  = "w"
	viewCert   = "c"  // c:<id>
	askRenew   = "ar" // ar:<id>, and so on
	askDeploy  = "ad"
	doRenew    = "xr"
	doDeploy   = "xd"
	doCheck    = "xc"
	doPause    = "xp"
	doResume   = "xu"
	// viewNew shows the view after it in a message of its own, rather than
	// in place: for the buttons under a note, which stays.
	viewNew = "n" // n:c:<id>, and so on

	pageSize   = 20   // certificates a page of the list holds
	recentRuns = 15   // runs the recent activity shows
	maxText    = 4000 // runes of a message, under Telegram's 4096
	dayLength  = 24 * time.Hour
)

// The icons that go before what the views tell.
const (
	iconLock    = "🔒"
	iconOK      = "✅"
	iconWarn    = "⚠️"
	iconFail    = "❌"
	iconBusy    = "⏳"
	iconInfo    = "ℹ️"
	iconClock   = "⏰"
	iconList    = "📋"
	iconRecent  = "🕑"
	iconOnline  = "🌐"
	iconHelp    = "❓"
	iconRefresh = "🔄"
	iconLink    = "🔗"
	iconHome    = "🏠"
	iconBack    = "⬅️"
	iconRenew   = "🔁"
	iconDeploy  = "📤"
	iconSource  = "📥"
	iconIssuer  = "🏢"
	iconUpload  = "📎"
	iconOff     = "🔌"
	iconPrev    = "◀️"
	iconNext    = "▶️"
	iconCancel  = "✖️"
	iconSites   = "📡"
)

// show builds the view data names, or the main one for data it does not
// know; a failure shows as a view of its own.
func (l *listener) show(ctx context.Context, data string) screen {
	view, arg, _ := strings.Cut(data, ":")
	id, _ := strconv.ParseInt(arg, 10, 64)
	var s screen
	var err error
	switch view {
	case viewList:
		s, err = l.listView(ctx, int(id))
	case viewTodo:
		s, err = l.todoView(ctx)
	case viewRecent:
		s, err = l.recentView(ctx)
	case viewOnline:
		s, err = l.onlineView(ctx)
	case viewSystem:
		s, err = l.systemView(ctx)
	case viewHelp:
		s = l.helpView()
	case viewSites:
		s, err = l.sitesView(ctx)
	case viewCert:
		s, err = l.certView(ctx, id, "")
	case askRenew, askDeploy:
		s, err = l.askView(ctx, view, id)
	case doRenew, doDeploy, doCheck, doPause, doResume:
		s, err = l.act(ctx, view, id)
	default:
		s, err = l.mainView(ctx)
	}
	if err != nil {
		return l.failed(err)
	}
	if utf8.RuneCountInString(s.text) > maxText {
		s.text = string([]rune(s.text)[:maxText]) + "…"
	}
	return s
}

// failed tells why JEC could not show or do what was asked.
func (l *listener) failed(err error) screen {
	var refused *module.CallError
	reason := l.plain(i18n.TelegramAnswerUnknown, nil)
	if errors.As(err, &refused) && refused.Code != "" {
		reason = l.plain(i18n.TelegramAnswerFailed, i18n.Args{"reason": l.plain(refused.Code, i18n.Args(refused.Params))})
	} else {
		l.m.log.Warn("the Telegram menu", "bot", l.bot.id, "err", err)
	}
	return screen{text: iconFail + " " + html.EscapeString(reason), rows: [][]button{{l.home()}}, toast: reason}
}

func (l *listener) mainView(ctx context.Context) (screen, error) {
	certs, err := l.certificates(ctx)
	if err != nil {
		return screen{}, err
	}
	now := l.m.now()
	var ok, expiring, attention int
	var next *certificate
	for i := range certs {
		c := &certs[i]
		switch {
		case needsAttention(c):
			attention++
		case c.State == "expiring":
			expiring++
		default:
			ok++
		}
		if c.Source == "provider" && c.RenewAt != nil && c.RenewAt.After(now) && (next == nil || c.RenewAt.Before(*next.RenewAt)) {
			next = c
		}
	}
	lines := []string{iconLock + " <b>" + l.text(i18n.TelegramMenuTitle, nil) + "</b>", ""}
	if len(certs) == 0 {
		lines = append(lines, l.text(i18n.TelegramMenuNone, nil))
	} else {
		lines = append(lines,
			l.text(i18n.TelegramMenuSummary, i18n.Args{"total": len(certs)}),
			l.text(i18n.TelegramMenuCounts, i18n.Args{"ok": ok, "expiring": expiring, "attention": attention}))
		if next != nil {
			lines = append(lines, iconClock+" "+l.text(i18n.TelegramMenuNextRenewal, i18n.Args{"time": l.clock(*next.RenewAt), "domain": primary(next)}))
		}
	}
	lines = append(lines, "", "<i>"+l.text(i18n.TelegramMenuUpdated, i18n.Args{"time": now.In(l.m.zone()).Format(time.TimeOnly)})+"</i>")
	rows := [][]button{
		{l.button(iconList, i18n.TelegramButtonCerts, i18n.Args{"count": len(certs)}, viewList+":0"),
			l.button(iconWarn, i18n.TelegramButtonTodo, i18n.Args{"count": attention}, viewTodo)},
		{l.button(iconRecent, i18n.TelegramButtonRecent, nil, viewRecent), l.button(iconOnline, i18n.TelegramButtonOnline, nil, viewOnline),
			l.button(iconSites, i18n.TelegramButtonSites, nil, viewSites)},
		{l.button(iconInfo, i18n.TelegramButtonSystem, nil, viewSystem), l.button(iconHelp, i18n.TelegramButtonHelp, nil, viewHelp)},
		append([]button{l.button(iconRefresh, i18n.TelegramButtonRefresh, nil, viewMain)}, l.open("")...),
	}
	return screen{text: strings.Join(lines, "\n"), rows: rows}, nil
}

func (l *listener) listView(ctx context.Context, page int) (screen, error) {
	certs, err := l.certificates(ctx)
	if err != nil {
		return screen{}, err
	}
	pages := max(1, (len(certs)+pageSize-1)/pageSize)
	page = min(max(page, 0), pages-1)
	shown := certs[min(page*pageSize, len(certs)):min((page+1)*pageSize, len(certs))]
	lines := []string{iconList + " <b>" + l.text(i18n.TelegramListTitle, i18n.Args{"count": len(certs)}) + "</b>", ""}
	if len(certs) == 0 {
		lines = append(lines, l.text(i18n.TelegramMenuNone, nil))
	}
	for i := range shown {
		c := &shown[i]
		lines = append(lines, icon(c)+" <b>"+html.EscapeString(primary(c))+"</b>"+more(c)+" · "+l.short(c))
	}
	rows := l.certButtons(shown)
	if pages > 1 {
		var nav []button
		if page > 0 {
			nav = append(nav, l.button(iconPrev, i18n.TelegramButtonPrev, nil, viewList+":"+strconv.Itoa(page-1)))
		}
		if page < pages-1 {
			nav = append(nav, l.button(iconNext, i18n.TelegramButtonNext, nil, viewList+":"+strconv.Itoa(page+1)))
		}
		rows = append(rows, nav)
	}
	rows = append(rows, []button{l.home()})
	return screen{text: strings.Join(lines, "\n"), rows: rows}, nil
}

func (l *listener) todoView(ctx context.Context) (screen, error) {
	certs, err := l.certificates(ctx)
	if err != nil {
		return screen{}, err
	}
	var todo []certificate
	for _, c := range certs {
		if needsAttention(&c) {
			todo = append(todo, c)
		}
	}
	lines := []string{iconWarn + " <b>" + l.text(i18n.TelegramTodoTitle, i18n.Args{"count": len(todo)}) + "</b>", ""}
	if len(todo) == 0 {
		lines = append(lines, iconOK+" "+l.text(i18n.TelegramTodoNone, nil))
	}
	for i := range todo {
		c := &todo[i]
		lines = append(lines, icon(c)+" <b>"+html.EscapeString(primary(c))+"</b> · "+l.text(stateText(c.State), nil))
		if why := l.why(c); why != "" {
			lines = append(lines, "    "+why)
		}
	}
	rows := append(l.certButtons(todo), []button{l.button(iconRefresh, i18n.TelegramButtonRefresh, nil, viewTodo), l.home()})
	return screen{text: strings.Join(lines, "\n"), rows: rows}, nil
}

// why tells why a certificate needs attention.
func (l *listener) why(c *certificate) string {
	switch c.State {
	case "failed":
		if c.Issuance != nil && c.Issuance.Error != nil {
			return l.message(c.Issuance.Error)
		}
	case "deploy_failed":
		for _, d := range c.Deployments {
			if d.Error != nil && (d.State == "failed" || d.State == "pending") {
				return l.message(d.Error)
			}
		}
	case "expired":
		if c.Current != nil {
			return l.text(i18n.TelegramTodoExpired, i18n.Args{"date": l.day(c.Current.NotAfter)})
		}
	case "revoked":
		return l.text(i18n.TelegramStateRevoked, nil)
	case "expiring":
		if c.Current != nil {
			return l.text(i18n.TelegramTodoExpiringManual, i18n.Args{"date": l.day(c.Current.NotAfter)})
		}
	}
	return ""
}

func (l *listener) recentView(ctx context.Context) (screen, error) {
	var out struct {
		Runs []run `json:"runs"`
	}
	if err := l.get(ctx, "/certs/runs?days=7", &out); err != nil {
		return screen{}, err
	}
	names := l.names(ctx)
	lines := []string{iconRecent + " <b>" + l.text(i18n.TelegramRecentTitle, nil) + "</b>", ""}
	if len(out.Runs) == 0 {
		lines = append(lines, l.text(i18n.TelegramRecentNone, nil))
	}
	for _, r := range out.Runs[:min(len(out.Runs), recentRuns)] {
		action := l.text(runActions[r.Action], nil)
		if r.Trigger == "manual" && r.Action != "renew" {
			action += l.text(i18n.TelegramRunManual, nil)
		}
		line := runIcon(r.State) + " " + l.clock(r.CreatedAt) + " " + action + " <b>" + html.EscapeString(r.Domain) + "</b>"
		if r.Action == "deploy" {
			if n, ok := names[r.Target]; ok {
				line += " → " + html.EscapeString(n.Name)
			}
		}
		lines = append(lines, line+" · "+l.text(runStates[r.State], nil))
	}
	rows := [][]button{{l.button(iconRefresh, i18n.TelegramButtonRefresh, nil, viewRecent), l.home()}}
	return screen{text: strings.Join(lines, "\n"), rows: rows}, nil
}

func (l *listener) onlineView(ctx context.Context) (screen, error) {
	var out struct {
		Certificates []tally `json:"certificates"`
	}
	lines := []string{iconOnline + " <b>" + l.text(i18n.TelegramOnlineTitle, nil) + "</b>", ""}
	var refused *module.CallError
	if err := l.get(ctx, "/monitor/states", &out); errors.As(err, &refused) && refused.Status == 404 {
		lines = append(lines, iconOff+" "+l.text(i18n.TelegramOnlineOff, nil))
		return screen{text: strings.Join(lines, "\n"), rows: [][]button{{l.home()}}}, nil
	} else if err != nil {
		return screen{}, err
	}
	certs, err := l.certificates(ctx)
	if err != nil {
		return screen{}, err
	}
	var troubled []certificate
	for _, t := range out.Certificates {
		i := slices.IndexFunc(certs, func(c certificate) bool { return c.ID == t.CertificateID })
		if i < 0 || t.Old+t.Unreachable == 0 {
			continue
		}
		c := certs[i]
		troubled = append(troubled, c)
		name := "<b>" + html.EscapeString(primary(&c)) + "</b>"
		if t.Old > 0 {
			lines = append(lines, iconWarn+" "+name+" · "+l.text(i18n.TelegramOnlineProblemOld, i18n.Args{"count": t.Old}))
		}
		if t.Unreachable > 0 {
			lines = append(lines, iconOff+" "+name+" · "+l.text(i18n.TelegramOnlineProblemUnreachable, i18n.Args{"count": t.Unreachable}))
		}
	}
	if len(troubled) == 0 {
		lines = append(lines, iconOK+" "+l.text(i18n.TelegramOnlineAllOk, nil))
	}
	rows := append(l.certButtons(troubled), []button{l.button(iconRefresh, i18n.TelegramButtonRefresh, nil, viewOnline), l.home()})
	return screen{text: strings.Join(lines, "\n"), rows: rows}, nil
}

// site is a site the watch module watches, as the menu shows it.
type site struct {
	Host     string     `json:"host"`
	Port     int        `json:"port"`
	Name     string     `json:"name"`
	State    string     `json:"state"`
	NotAfter *time.Time `json:"notAfter"`
}

var siteStates = map[string]i18n.Key{
	"pending": i18n.WatchStatePending, "ok": i18n.WatchStateOk, "expiring": i18n.WatchStateExpiring,
	"expired": i18n.WatchStateExpired, "invalid": i18n.WatchStateInvalid, "unreachable": i18n.WatchStateUnreachable,
}

// domain is a domain registration as the watch module tells it.
type domain struct {
	Name      string     `json:"name"`
	State     string     `json:"state"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

var domainStates = map[string]i18n.Key{
	"pending": i18n.WatchDomainStatePending, "ok": i18n.WatchDomainStateOk, "expiring": i18n.WatchDomainStateExpiring,
	"expired": i18n.WatchDomainStateExpired, "unknown": i18n.WatchDomainStateUnknown, "failed": i18n.WatchDomainStateFailed,
}

// maxDomains bounds the domains listed, as a message has a size.
const maxDomains = 20

// sitesView shows the sites watched, those to look at first.
func (l *listener) sitesView(ctx context.Context) (screen, error) {
	var out struct {
		Sites []site `json:"sites"`
	}
	lines := []string{iconSites + " <b>" + l.text(i18n.TelegramSitesTitle, nil) + "</b>", ""}
	var refused *module.CallError
	if err := l.get(ctx, "/watch/sites", &out); errors.As(err, &refused) && refused.Status == 404 {
		lines = append(lines, iconOff+" "+l.text(i18n.TelegramSitesOff, nil))
		return screen{text: strings.Join(lines, "\n"), rows: [][]button{{l.home()}}}, nil
	} else if err != nil {
		return screen{}, err
	}
	var registrations struct {
		Domains []domain `json:"domains"`
	}
	if err := l.get(ctx, "/watch/domains", &registrations); err != nil {
		return screen{}, err
	}
	if len(out.Sites) == 0 && len(registrations.Domains) == 0 {
		lines = append(lines, l.text(i18n.TelegramSitesNone, nil))
	}
	if len(out.Sites) > 0 {
		lines = append(lines, "<b>"+l.text(i18n.WatchPageSites, nil)+"</b>")
	}
	rank := map[string]int{"expired": 0, "invalid": 1, "unreachable": 2, "expiring": 3, "pending": 4, "ok": 5}
	slices.SortStableFunc(out.Sites, func(a, b site) int { return rank[a.State] - rank[b.State] })
	now := l.m.now()
	left := func(t time.Time) string {
		days := int(math.Floor(t.Sub(now).Hours() / 24))
		if days < 0 {
			return l.text(i18n.WatchPageDaysAgo, i18n.Args{"days": -days})
		}
		return l.text(i18n.WatchPageDaysLeft, i18n.Args{"days": days})
	}
	for _, s := range out.Sites {
		icon := iconOK
		switch s.State {
		case "expired", "invalid":
			icon = iconFail
		case "unreachable":
			icon = iconOff
		case "expiring":
			icon = iconWarn
		case "pending":
			icon = iconBusy
		}
		name := s.Host
		if s.Port != 443 {
			name += ":" + strconv.Itoa(s.Port)
		}
		if s.Name != "" {
			name = s.Name + " (" + name + ")"
		}
		line := icon + " <b>" + html.EscapeString(name) + "</b> · " + l.text(siteStates[s.State], nil)
		if s.NotAfter != nil {
			line += " · " + l.day(*s.NotAfter) + " · " + left(*s.NotAfter)
		}
		lines = append(lines, line)
	}
	// The domain registrations: those to look at first, then by their end.
	if len(registrations.Domains) > 0 {
		if len(out.Sites) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "<b>"+l.text(i18n.WatchDomainTitle, nil)+"</b>")
	}
	order := map[string]int{"expired": 0, "expiring": 1, "failed": 2, "pending": 3, "ok": 4, "unknown": 5}
	slices.SortStableFunc(registrations.Domains, func(a, b domain) int {
		if c := order[a.State] - order[b.State]; c != 0 {
			return c
		}
		if a.ExpiresAt == nil || b.ExpiresAt == nil {
			return 0
		}
		return a.ExpiresAt.Compare(*b.ExpiresAt)
	})
	for i, d := range registrations.Domains {
		if i == maxDomains {
			lines = append(lines, l.text(i18n.TelegramSitesMore, i18n.Args{"count": len(registrations.Domains) - maxDomains}))
			break
		}
		icon := iconOK
		switch d.State {
		case "expired":
			icon = iconFail
		case "expiring":
			icon = iconWarn
		case "failed", "unknown":
			icon = iconOff
		case "pending":
			icon = iconBusy
		}
		line := icon + " <b>" + html.EscapeString(d.Name) + "</b> · " + l.text(domainStates[d.State], nil)
		if d.ExpiresAt != nil {
			line += " · " + l.day(*d.ExpiresAt) + " · " + left(*d.ExpiresAt)
		}
		lines = append(lines, line)
	}
	// The certificates found in transparency logs that someone should look at.
	var ct struct {
		Certificates []struct {
			Names  []string `json:"names"`
			Issuer string   `json:"issuer"`
			State  string   `json:"state"`
		} `json:"certificates"`
	}
	if err := l.get(ctx, "/watch/transparency", &ct); err != nil {
		return screen{}, err
	}
	header := false
	for _, c := range ct.Certificates {
		if c.State != "unknown" {
			continue
		}
		if !header {
			lines = append(lines, "", "<b>"+l.text(i18n.WatchCtTitle, nil)+"</b>")
			header = true
		}
		lines = append(lines, iconWarn+" <b>"+html.EscapeString(strings.Join(c.Names, ", "))+"</b> · "+html.EscapeString(c.Issuer))
	}
	rows := [][]button{append([]button{l.button(iconRefresh, i18n.TelegramButtonRefresh, nil, viewSites), l.home()}, l.open("/sites")...)}
	return screen{text: strings.Join(lines, "\n"), rows: rows}, nil
}

func (l *listener) systemView(ctx context.Context) (screen, error) {
	var m manifest
	if err := l.get(ctx, "/manifest", &m); err != nil {
		return screen{}, err
	}
	on := 0
	for _, mod := range m.Modules {
		if mod.Enabled {
			on++
		}
	}
	kinds := map[string]int{}
	for _, p := range l.names(ctx) {
		kinds[p.Kind]++
	}
	zone := m.TimeZone
	if zone == "" {
		zone = l.m.zone().String()
	}
	lines := []string{
		iconInfo + " <b>" + l.text(i18n.TelegramSystemTitle, nil) + "</b>", "",
		l.text(i18n.TelegramSystemVersion, i18n.Args{"version": m.Version}),
		l.text(i18n.TelegramSystemPlatform, i18n.Args{"platform": m.Platform}),
		l.text(i18n.TelegramSystemZone, i18n.Args{"zone": zone}),
		l.text(i18n.TelegramSystemModules, i18n.Args{"count": on}),
		l.text(i18n.TelegramSystemProviders, i18n.Args{
			"sources": kinds["source"], "solvers": kinds["solver"], "targets": kinds["target"], "notifiers": kinds["notifier"],
		}),
	}
	if l.m.baseURL != "" {
		lines = append(lines, l.text(i18n.TelegramSystemPanel, i18n.Args{"url": l.m.baseURL}))
	}
	return screen{text: strings.Join(lines, "\n"), rows: [][]button{append([]button{l.home()}, l.open("")...)}}, nil
}

func (l *listener) helpView() screen {
	lines := []string{iconHelp + " <b>" + l.text(i18n.TelegramHelpTitle, nil) + "</b>", "", l.text(i18n.TelegramHelpIntro, nil)}
	for _, c := range []struct {
		name string
		desc i18n.Key
	}{
		{"menu", i18n.TelegramCommandMenu}, {"certs", i18n.TelegramCommandCerts}, {"todo", i18n.TelegramCommandTodo},
		{"recent", i18n.TelegramCommandRecent}, {"online", i18n.TelegramCommandOnline}, {"sites", i18n.TelegramCommandSites},
		{"help", i18n.TelegramCommandHelp},
	} {
		lines = append(lines, "/"+c.name+" · "+l.text(c.desc, nil))
	}
	if l.bot.actions {
		lines = append(lines, "", l.text(i18n.TelegramHelpActionsOn, nil))
	} else {
		lines = append(lines, "", l.text(i18n.TelegramHelpActionsOff, nil))
	}
	return screen{text: strings.Join(lines, "\n"), rows: [][]button{{l.home()}}}
}

// certView shows a certificate: how it is, where it comes from, where it
// goes and what the domains serve, with what can be done to it. notice,
// if any, goes first, as what was just done.
func (l *listener) certView(ctx context.Context, id int64, notice string) (screen, error) {
	c, err := l.certificate(ctx, id)
	if err != nil {
		return screen{}, err
	}
	names := l.names(ctx)
	var lines []string
	if notice != "" {
		lines = append(lines, iconOK+" "+notice, "")
	}
	lines = append(lines, iconLock+" <b>"+html.EscapeString(primary(&c))+"</b>")
	if len(c.Domains) > 1 {
		lines = append(lines, l.text(i18n.TelegramCertAlso, i18n.Args{"names": strings.Join(c.Domains[1:], ", ")}))
	}
	lines = append(lines, icon(&c)+" "+l.text(stateText(c.State), nil)+" · "+l.life(&c))
	switch {
	case c.State == "issuing" && c.Issuance != nil && c.Issuance.NextAt != nil && c.Issuance.State == "waiting":
		lines = append(lines, iconBusy+" "+l.text(i18n.TelegramCertNextLook, i18n.Args{"time": l.clock(*c.Issuance.NextAt)}))
	case c.State == "issuing" && c.Issuance != nil && c.Issuance.NextAt != nil && c.Issuance.Error != nil:
		lines = append(lines, iconBusy+" "+l.text(i18n.TelegramCertNextTry, i18n.Args{"time": l.clock(*c.Issuance.NextAt)}))
	case c.Source == "provider" && c.Paused:
		lines = append(lines, iconOff+" "+l.text(i18n.TelegramCertPaused, nil))
	case c.Source == "provider" && c.RenewAt != nil:
		lines = append(lines, iconClock+" "+l.text(i18n.TelegramCertRenewAt, i18n.Args{"time": l.clock(*c.RenewAt)}))
	case c.Source != "provider":
		lines = append(lines, iconUpload+" "+l.text(i18n.TelegramCertManual, nil))
	}
	if c.Source == "provider" {
		from := l.text(i18n.TelegramCertSource, i18n.Args{"name": names[c.SourceProvider].Name})
		if p, ok := names[c.SolverProvider]; ok {
			from += " · " + l.text(i18n.TelegramCertSolver, i18n.Args{"name": p.Name})
		}
		lines = append(lines, iconSource+" "+from)
	} else {
		lines = append(lines, iconSource+" "+l.text(i18n.TelegramCertUpload, nil))
	}
	if c.Current != nil {
		lines = append(lines, iconIssuer+" "+l.text(i18n.TelegramCertIssuer, i18n.Args{"issuer": c.Current.Issuer}))
	}

	lines = append(lines, "", "<b>"+l.text(i18n.TelegramCertDeploys, nil)+"</b>")
	if len(c.Targets) == 0 {
		lines = append(lines, l.text(i18n.TelegramCertNoTargets, nil))
	}
	for _, t := range c.Targets {
		state, at := "none", ""
		if i := slices.IndexFunc(c.Deployments, func(d deployment) bool { return d.Target == t }); i >= 0 {
			d := c.Deployments[i]
			state = d.State
			if d.FinishedAt != nil {
				at = " · " + l.clock(*d.FinishedAt)
			}
		}
		lines = append(lines, deployIcon(state)+" "+html.EscapeString(names[t].Name)+" · "+l.text(deployStates[state], nil)+at)
	}

	monitored := false
	var online checks
	if c.Current != nil && l.get(ctx, "/monitor/certificates/"+strconv.FormatInt(id, 10), &online) == nil {
		monitored = online.Tracked
		lines = append(lines, "", "<b>"+l.text(i18n.TelegramCertOnline, nil)+"</b>")
		if len(online.Results) == 0 {
			lines = append(lines, l.text(i18n.TelegramCertOnlinePending, nil))
		}
		lines = append(lines, l.checks(online.Results)...)
	}

	if is := c.Issuance; is != nil {
		lines = append(lines, "", "<b>"+l.text(i18n.TelegramCertLastRun, nil)+"</b>")
		action := runActions[map[string]string{"renewal": "renew"}[is.Reason]]
		if action == "" {
			action = i18n.TelegramRunIssue
		}
		line := runIcon(is.State) + " " + l.clock(is.CreatedAt) + " " + l.text(action, nil) + " · " + l.text(runStates[is.State], nil)
		lines = append(lines, line)
		if is.Error != nil && is.State != "done" {
			lines = append(lines, "    "+l.message(is.Error))
		}
	}

	var rows [][]button
	if l.bot.actions && c.Current != nil {
		var row []button
		if c.Source == "provider" && c.State != "issuing" {
			row = append(row, l.button(iconRenew, i18n.TelegramButtonRenew, nil, askRenew+":"+strconv.FormatInt(id, 10)))
		}
		if len(c.Targets) > 0 && c.State != "deploying" {
			row = append(row, l.button(iconDeploy, i18n.TelegramButtonDeploy, nil, askDeploy+":"+strconv.FormatInt(id, 10)))
		}
		if monitored {
			row = append(row, l.button(iconOnline, i18n.TelegramButtonCheck, nil, doCheck+":"+strconv.FormatInt(id, 10)))
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
		switch {
		case c.Source == "provider" && c.Paused:
			rows = append(rows, []button{l.button(iconRenew, i18n.TelegramButtonResume, nil, doResume+":"+strconv.FormatInt(id, 10))})
		case c.Source == "provider":
			rows = append(rows, []button{l.button(iconOff, i18n.TelegramButtonPause, nil, doPause+":"+strconv.FormatInt(id, 10))})
		}
	}
	rows = append(rows,
		[]button{l.button(iconRefresh, i18n.TelegramButtonRefresh, nil, viewCert+":"+strconv.FormatInt(id, 10))},
		append([]button{l.button(iconBack, i18n.TelegramButtonBack, nil, viewList+":0"), l.home()}, l.open("/certificates/"+strconv.FormatInt(id, 10))...))
	return screen{text: strings.Join(lines, "\n"), rows: rows}, nil
}

// checks tells what the domains served, one line for each outcome, as the
// page of the certificate does.
func (l *listener) checks(results []check) []string {
	type outcome struct {
		key     string
		domains []string
		result  check
	}
	var out []outcome
	for _, r := range results {
		key := strings.Join([]string{r.View, r.State, r.Issuer, l.day(ptrTime(r.NotAfter))}, "|")
		if i := slices.IndexFunc(out, func(o outcome) bool { return o.key == key }); i >= 0 {
			out[i].domains = append(out[i].domains, r.Domain)
			continue
		}
		out = append(out, outcome{key: key, domains: []string{r.Domain}, result: r})
	}
	var lines []string
	for _, o := range out {
		r := o.result
		what := ""
		switch r.State {
		case "ok":
			what = l.text(i18n.TelegramOnlineOk, nil)
		case "old":
			what = l.text(i18n.TelegramOnlineOld, i18n.Args{"date": l.day(ptrTime(r.NotAfter))})
		case "other":
			what = l.text(i18n.TelegramOnlineOther, i18n.Args{"issuer": r.Issuer})
		default:
			what = l.text(i18n.TelegramOnlineUnreachable, nil)
		}
		where := html.EscapeString(strings.Join(o.domains, ", "))
		switch r.View {
		case "cn":
			where += " (" + l.text(i18n.TelegramOnlineCn, nil) + ")"
		case "global":
			where += " (" + l.text(i18n.TelegramOnlineGlobal, nil) + ")"
		}
		lines = append(lines, checkIcon(r.State)+" "+where+" · "+what)
	}
	return lines
}

// askView asks before renewing or deploying again, saying what it does.
func (l *listener) askView(ctx context.Context, view string, id int64) (screen, error) {
	if !l.bot.actions {
		return l.notAllowed(id), nil
	}
	c, err := l.certificate(ctx, id)
	if err != nil {
		return screen{}, err
	}
	names := l.names(ctx)
	ref := ":" + strconv.FormatInt(id, 10)
	var lines []string
	var confirm button
	if view == askRenew {
		lines = []string{
			iconHelp + " <b>" + l.text(i18n.TelegramActionRenewAsk, i18n.Args{"domain": primary(&c)}) + "</b>", "",
			l.text(i18n.TelegramActionRenewDetail, i18n.Args{"source": names[c.SourceProvider].Name}),
		}
		confirm = l.button(iconOK, i18n.TelegramButtonConfirmRenew, nil, doRenew+ref)
	} else {
		var targets []string
		for _, t := range c.Targets {
			targets = append(targets, names[t].Name)
		}
		lines = []string{
			iconHelp + " <b>" + l.text(i18n.TelegramActionDeployAsk, i18n.Args{"domain": primary(&c)}) + "</b>", "",
			l.text(i18n.TelegramActionDeployDetail, i18n.Args{"targets": strings.Join(targets, ", ")}),
		}
		confirm = l.button(iconOK, i18n.TelegramButtonConfirmDeploy, nil, doDeploy+ref)
	}
	rows := [][]button{{confirm, l.button(iconCancel, i18n.TelegramButtonCancel, nil, viewCert+ref)}}
	return screen{text: strings.Join(lines, "\n"), rows: rows}, nil
}

// act renews, deploys again, checks now, or pauses or resumes the renewal,
// then shows the certificate.
func (l *listener) act(ctx context.Context, view string, id int64) (screen, error) {
	if !l.bot.actions {
		return l.notAllowed(id), nil
	}
	ref := strconv.FormatInt(id, 10)
	var done i18n.Key
	var err error
	switch view {
	case doRenew:
		done, err = i18n.TelegramActionRenewStarted, l.post(ctx, "/certs/certificates/"+ref+"/issue", nil)
	case doDeploy:
		done, err = i18n.TelegramActionDeployStarted, l.post(ctx, "/certs/certificates/"+ref+"/deploy", map[string]any{})
	case doPause, doResume:
		done = i18n.TelegramActionPaused
		if view == doResume {
			done = i18n.TelegramActionResumed
		}
		err = l.patch(ctx, "/certs/certificates/"+ref, map[string]any{"paused": view == doPause})
	default:
		done, err = i18n.TelegramActionCheckStarted, l.post(ctx, "/monitor/certificates/"+ref+"/check", nil)
	}
	if err != nil {
		return screen{}, err
	}
	s, err := l.certView(ctx, id, l.text(done, nil))
	s.toast = l.plain(done, nil)
	return s, err
}

func (l *listener) notAllowed(id int64) screen {
	text := l.plain(i18n.TelegramActionNotAllowed, nil)
	return screen{
		text:  iconOff + " " + html.EscapeString(text),
		rows:  [][]button{{l.button(iconBack, i18n.TelegramButtonBack, nil, viewCert+":"+strconv.FormatInt(id, 10)), l.home()}},
		toast: text,
	}
}

// certButtons lists certificates as buttons, two to a row.
func (l *listener) certButtons(certs []certificate) [][]button {
	var rows [][]button
	for i := range certs {
		c := &certs[i]
		b := button{Text: icon(c) + " " + primary(c), Data: viewCert + ":" + strconv.FormatInt(c.ID, 10)}
		if i%2 == 0 {
			rows = append(rows, []button{b})
		} else {
			rows[len(rows)-1] = append(rows[len(rows)-1], b)
		}
	}
	return rows
}

func (l *listener) button(icon string, key i18n.Key, args i18n.Args, data string) button {
	return button{Text: icon + " " + l.plain(key, args), Data: data}
}

func (l *listener) home() button { return l.button(iconHome, i18n.TelegramButtonMenu, nil, viewMain) }

// open is the button to the page path of JEC, where Telegram can open it:
// none without an https address.
func (l *listener) open(path string) []button {
	if !strings.HasPrefix(l.m.baseURL, "https://") {
		return nil
	}
	return []button{{Text: iconLink + " " + l.plain(i18n.TelegramButtonOpen, nil), URL: strings.TrimRight(l.m.baseURL, "/") + path}}
}

// plain is message key in the bot's language, as plain text.
func (l *listener) plain(key i18n.Key, args i18n.Args) string {
	s, err := l.m.cat.Format(l.tag, key, args)
	if err != nil {
		l.m.log.Warn("format a Telegram message", "key", key, "err", err)
	}
	return s
}

// text is plain made safe for Telegram's HTML.
func (l *listener) text(key i18n.Key, args i18n.Args) string {
	return html.EscapeString(l.plain(key, args))
}

// message is a message of the API, made safe for Telegram's HTML.
func (l *listener) message(m *apiMessage) string {
	return l.text(m.Key, m.Params)
}

func (l *listener) clock(t time.Time) string { return t.In(l.m.zone()).Format("2006-01-02 15:04") }

func (l *listener) day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(l.m.zone()).Format(time.DateOnly)
}

// life tells how long a certificate has left, or that it has none.
func (l *listener) life(c *certificate) string {
	if c.Current == nil {
		return l.text(i18n.TelegramCertPending, nil)
	}
	left := c.Current.NotAfter.Sub(l.m.now())
	if left <= 0 {
		return l.text(i18n.TelegramCertExpired, i18n.Args{"date": l.day(c.Current.NotAfter)})
	}
	return l.text(i18n.TelegramCertLeft, i18n.Args{"days": int(math.Floor(left.Hours() / 24)), "date": l.day(c.Current.NotAfter)})
}

// short tells how a certificate is in a few words: the days it has left,
// or its state when that says more.
func (l *listener) short(c *certificate) string {
	switch {
	case c.Current == nil || c.State != "ok" && c.State != "expiring":
		return l.text(stateText(c.State), nil)
	case !c.Current.NotAfter.After(l.m.now()):
		return l.text(i18n.TelegramStateExpired, nil)
	}
	return l.text(i18n.TelegramListLeft, i18n.Args{"days": int(c.Current.NotAfter.Sub(l.m.now()) / dayLength)})
}

// needsAttention tells a certificate someone has to see to, as the list
// of the web interface does: an issuance or a deployment that failed, one
// expired, or an upload about to.
func needsAttention(c *certificate) bool {
	switch c.State {
	case "failed", "deploy_failed", "expired", "revoked":
		return true
	case "expiring":
		return c.Source != "provider"
	}
	return false
}

func primary(c *certificate) string {
	if len(c.Domains) == 0 {
		return ""
	}
	return c.Domains[0]
}

// more counts the other domains of a certificate: +1.
func more(c *certificate) string {
	if len(c.Domains) < 2 {
		return ""
	}
	return " +" + strconv.Itoa(len(c.Domains)-1)
}

func icon(c *certificate) string {
	switch c.State {
	case "ok":
		return iconOK
	case "expiring":
		return iconWarn
	case "issuing", "deploying":
		return iconBusy
	}
	return iconFail
}

func stateText(state string) i18n.Key {
	if k, ok := states[state]; ok {
		return k
	}
	return i18n.TelegramStateOk
}

var states = map[string]i18n.Key{
	"ok": i18n.TelegramStateOk, "expiring": i18n.TelegramStateExpiring, "expired": i18n.TelegramStateExpired,
	"issuing": i18n.TelegramStateIssuing, "deploying": i18n.TelegramStateDeploying,
	"failed": i18n.TelegramStateFailed, "deploy_failed": i18n.TelegramStateDeployFailed, "revoked": i18n.TelegramStateRevoked,
}

var deployStates = map[string]i18n.Key{
	"done": i18n.TelegramDeployDone, "pending": i18n.TelegramDeployPending, "running": i18n.TelegramDeployRunning,
	"failed": i18n.TelegramDeployFailed, "canceled": i18n.TelegramDeployCanceled, "none": i18n.TelegramDeployNone,
}

func deployIcon(state string) string {
	switch state {
	case "done":
		return iconOK
	case "pending", "running":
		return iconBusy
	case "failed":
		return iconFail
	}
	return iconInfo
}

var runActions = map[string]i18n.Key{"issue": i18n.TelegramRunIssue, "renew": i18n.TelegramRunRenew, "deploy": i18n.TelegramRunDeploy}

var runStates = map[string]i18n.Key{
	"done": i18n.TelegramRunDone, "failed": i18n.TelegramRunFailed, "running": i18n.TelegramRunRunning,
	"pending": i18n.TelegramRunPending, "waiting": i18n.TelegramRunWaiting, "canceled": i18n.TelegramRunCanceled,
}

func runIcon(state string) string {
	switch state {
	case "done":
		return iconOK
	case "failed":
		return iconFail
	case "canceled":
		return iconInfo
	}
	return iconBusy
}

func checkIcon(state string) string {
	switch state {
	case "ok":
		return iconOK
	case "old":
		return iconWarn
	case "unreachable":
		return iconOff
	}
	return iconInfo
}

func ptrTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
