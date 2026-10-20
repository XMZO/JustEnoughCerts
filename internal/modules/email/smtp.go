package email

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// How a connection is encrypted.
const (
	implicitTLS = "tls"      // TLS from the start, usually on port 465
	startTLS    = "starttls" // plain, then STARTTLS, usually on port 587
	noTLS       = "none"     // never, for a relay on a trusted network
)

func smtpType() providers.Type {
	return providers.Type{
		ID: "email.smtp", Kind: providers.Notifier, Name: i18n.EmailSmtpName, Description: i18n.EmailSmtpDescription,
		Tags: []string{"popular"},
		Fields: append([]providers.Field{
			{Key: "host", Label: i18n.EmailFieldHost, Help: i18n.EmailFieldHostHelp, Kind: providers.Text, Required: true},
			{Key: "security", Label: i18n.EmailFieldSecurity, Kind: providers.Select, Required: true, Default: implicitTLS, Options: []providers.Option{
				{Value: implicitTLS, Label: i18n.EmailSecurityTls},
				{Value: startTLS, Label: i18n.EmailSecurityStarttls},
				{Value: noTLS, Label: i18n.EmailSecurityNone},
			}},
			{Key: "port", Label: i18n.EmailFieldPort, Help: i18n.EmailFieldPortHelp, Kind: providers.Number},
			{Key: "username", Label: i18n.EmailFieldUsername, Help: i18n.EmailFieldUsernameHelp, Kind: providers.Text},
			{Key: "password", Label: i18n.EmailFieldPassword, Help: i18n.EmailFieldPasswordHelp, Kind: providers.Text, Secret: true},
			{Key: "from", Label: i18n.EmailFieldFrom, Help: i18n.EmailFieldFromHelp, Kind: providers.Email, Required: true},
			{Key: "to", Label: i18n.EmailFieldTo, Help: i18n.EmailFieldToHelp, Kind: providers.Text, Required: true},
		}, contracts.NotifierFields()...),
	}
}

// mailer sends notes as mail through an SMTP server.
type mailer struct {
	host, port, security string
	username, password   string
	from, to             string
	// roots verify the server; nil for the system's.
	roots *x509.CertPool
	now   func() time.Time
}

var _ contracts.Notifier = (*mailer)(nil)

func factory(_ context.Context, in providers.Instance) (any, error) {
	m := &mailer{
		host: strings.TrimSpace(in.Config["host"]), port: strings.TrimSpace(in.Config["port"]), security: in.Config["security"],
		username: strings.TrimSpace(in.Config["username"]), password: in.Value("password"),
		from: strings.TrimSpace(in.Config["from"]), to: in.Config["to"], now: time.Now,
	}
	if m.port == "" {
		m.port = map[string]string{implicitTLS: "465", startTLS: "587", noTLS: "25"}[m.security]
	}
	return m, nil
}

// Close has nothing to release: each note has a connection of its own.
func (*mailer) Close() error { return nil }

// Notify mails n to the recipients: the title as the subject, the text as
// the body.
func (m *mailer) Notify(ctx context.Context, n contracts.Note) error {
	to, err := mail.ParseAddressList(m.to)
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.EmailErrorRecipients, Params: map[string]any{"to": m.to}, Err: err})
	}
	from, err := mail.ParseAddress(m.from)
	if err != nil {
		return retry.Permanent(&contracts.Problem{Key: i18n.EmailErrorSender, Params: map[string]any{"from": m.from}, Err: err})
	}
	msg, err := m.message(n, from, to)
	if err != nil {
		return retry.Permanent(err)
	}
	return m.send(ctx, from, to, msg)
}

// message is the mail of n: UTF-8 text, and the same as HTML for mail
// clients that show it, with its links clickable.
func (m *mailer) message(n contracts.Note, from *mail.Address, to []*mail.Address) ([]byte, error) {
	var b bytes.Buffer
	header := func(name, value string) { b.WriteString(name + ": " + value + "\r\n") }
	header("From", from.String())
	list := make([]string, 0, len(to))
	for _, a := range to {
		list = append(list, a.String())
	}
	header("To", strings.Join(list, ", "))
	header("Subject", mime.QEncoding.Encode("utf-8", cmp.Or(n.Subject, n.Title)))
	header("Date", m.now().Format(time.RFC1123Z))
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := from.Address[strings.LastIndex(from.Address, "@")+1:]
	header("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	header("MIME-Version", "1.0")
	boundary := "jec-" + hex.EncodeToString(id)
	header("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	header("Auto-Submitted", "auto-generated")
	header("X-JEC-Event", n.Event)
	for _, part := range []struct{ typ, body string }{
		{"text/plain", n.Text + "\n"},
		{"text/html", noteHTML(n)},
	} {
		b.WriteString("\r\n--" + boundary + "\r\n")
		header("Content-Type", part.typ+"; charset=utf-8")
		header("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		qp := quotedprintable.NewWriter(&b)
		if _, err := qp.Write([]byte(part.body)); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
	}
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return b.Bytes(), nil
}

// links are the web addresses in the lines of a note.
var links = regexp.MustCompile(`https?://[^\s<>"]+`)

// noteHTML is n as a small HTML page: its title, then its lines, each
// address in them a link. Styles are inline, as mail clients want.
func noteHTML(n contracts.Note) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><body style="margin:0;background:#f6f7f6">`)
	b.WriteString(`<div style="max-width:560px;margin:0 auto;padding:20px 16px;font-family:-apple-system,'Segoe UI',Roboto,'PingFang SC','Microsoft YaHei',sans-serif;font-size:14px;line-height:1.6;color:#1b1f1d">`)
	b.WriteString(`<p style="margin:0 0 8px;font-size:12px;color:#5c6660">Just Enough Certs</p>`)
	b.WriteString(`<h1 style="margin:0 0 12px;font-size:18px;line-height:1.4">` + html.EscapeString(n.Title) + `</h1>`)
	for line := range strings.SplitSeq(n.Text, "\n") {
		escaped := html.EscapeString(line)
		escaped = links.ReplaceAllStringFunc(escaped, func(u string) string {
			return `<a href="` + u + `" style="color:#1d6b46">` + u + `</a>`
		})
		b.WriteString(`<p style="margin:0 0 4px">` + escaped + `</p>`)
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}

// send delivers msg in one SMTP session.
func (m *mailer) send(ctx context.Context, from *mail.Address, to []*mail.Address, msg []byte) error {
	addr := net.JoinHostPort(m.host, m.port)
	tlsConfig := &tls.Config{ServerName: m.host, RootCAs: m.roots, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if m.security == implicitTLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return m.explain(err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(time.Minute)
	}
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		_ = conn.Close()
		return m.explain(err)
	}
	defer func() { _ = c.Close() }()
	if m.security == startTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return retry.Permanent(&contracts.Problem{Key: i18n.EmailErrorStarttls, Params: map[string]any{"host": m.host}})
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return m.explain(err)
		}
	}
	if m.username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return retry.Permanent(&contracts.Problem{Key: i18n.EmailErrorNoAuth, Params: map[string]any{"host": m.host}})
		}
		if err := c.Auth(m.auth(c)); err != nil {
			return m.explain(err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return m.explain(err)
	}
	for _, a := range to {
		if err := c.Rcpt(a.Address); err != nil {
			return m.explain(err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return m.explain(err)
	}
	if _, err := w.Write(msg); err != nil {
		return m.explain(err)
	}
	if err := w.Close(); err != nil {
		return m.explain(err)
	}
	_ = c.Quit()
	return nil
}

// auth signs in the way the server offers: PLAIN, or LOGIN, which some
// servers offer alone. Neither sends the password over a connection that
// is not encrypted, except to this machine.
func (m *mailer) auth(c *smtp.Client) smtp.Auth {
	_, mechs := c.Extension("AUTH")
	offered := strings.Fields(strings.ToUpper(mechs))
	if slices.Contains(offered, "LOGIN") && !slices.Contains(offered, "PLAIN") {
		return &loginAuth{username: m.username, password: m.password, host: m.host}
	}
	return smtp.PlainAuth("", m.username, m.password, m.host)
}

// errUnencrypted: a password would have gone out in the clear.
var errUnencrypted = errors.New("unencrypted connection")

// loginAuth is the LOGIN mechanism, which net/smtp lacks.
type loginAuth struct{ username, password, host string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLocal(server.Name) {
		return "", nil, errUnencrypted
	}
	if server.Name != a.host {
		return "", nil, errors.New("wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(challenge))) {
	case "username:":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("unexpected LOGIN challenge %q", challenge)
}

func isLocal(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// explain turns a failure into a Problem: one that trying again cannot
// fix, such as a refused password, ends the attempts.
func (m *mailer) explain(err error) error {
	host := map[string]any{"host": m.host}
	var tp *textproto.Error
	if errors.As(err, &tp) {
		params := map[string]any{"code": tp.Code, "message": tp.Msg}
		switch {
		case tp.Code == 535 || tp.Code == 534 || tp.Code == 530:
			return retry.Permanent(&contracts.Problem{Key: i18n.EmailErrorAuth, Params: params, Err: err})
		case tp.Code >= 500:
			return retry.Permanent(&contracts.Problem{Key: i18n.EmailErrorRejected, Params: params, Err: err})
		}
		return &contracts.Problem{Key: i18n.EmailErrorServer, Params: params, Err: err}
	}
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	switch {
	case errors.Is(err, errUnencrypted) || strings.Contains(err.Error(), "unencrypted connection"):
		return retry.Permanent(&contracts.Problem{Key: i18n.EmailErrorUnencrypted, Err: err})
	case errors.As(err, &unknown), errors.As(err, &hostname), errors.As(err, &invalid):
		return retry.Permanent(&contracts.Problem{Key: i18n.EmailErrorCertificate, Params: host, Err: err})
	case errors.As(err, &record):
		return retry.Permanent(&contracts.Problem{Key: i18n.EmailErrorTls, Params: host, Err: err})
	}
	return &contracts.Problem{Key: i18n.EmailErrorConnect, Params: map[string]any{"host": m.host, "port": m.port}, Err: err}
}
