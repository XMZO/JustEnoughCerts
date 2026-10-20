package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox/cryptoxtest"
)

const password = "s3cret"

// server speaks enough SMTP for the tests: it takes PLAIN or LOGIN with
// password, and keeps the mail it gets.
type server struct {
	t        *testing.T
	ln       net.Listener
	tls      *tls.Config // the certificate, for implicit TLS or STARTTLS
	implicit bool
	starttls bool
	auth     string // the mechanisms offered, "" for none
	greeting string // "220 fake" unless set
	rcpt     string // the reply to RCPT, "250 ok" unless set

	mu    sync.Mutex
	mails []string
	users []string
}

func newServer(t *testing.T, s *server) (*server, *x509.CertPool) {
	t.Helper()
	ca := cryptoxtest.NewCA(t)
	key := cryptoxtest.NewKey(t)
	leaf := ca.Leaf(t, key, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "localhost")
	s.t = t
	s.tls = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leaf.Raw, ca.Intermediate.Raw}, PrivateKey: key}}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.ln = ln
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	roots := x509.NewCertPool()
	roots.AddCert(ca.Root)
	return s, roots
}

func (s *server) port() string {
	_, port, _ := net.SplitHostPort(s.ln.Addr().String())
	return port
}

func (s *server) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if s.implicit {
		conn = tls.Server(conn, s.tls)
	}
	tp := textproto.NewConn(conn)
	reply := func(lines ...string) {
		for _, l := range lines {
			if err := tp.PrintfLine("%s", l); err != nil {
				return
			}
		}
	}
	greeting := s.greeting
	if greeting == "" {
		greeting = "220 fake ESMTP"
	}
	reply(greeting)
	if !strings.HasPrefix(greeting, "220") {
		return
	}
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			ext := []string{"250-fake"}
			if s.starttls {
				ext = append(ext, "250-STARTTLS")
			}
			if s.auth != "" {
				ext = append(ext, "250-AUTH "+s.auth)
			}
			ext = append(ext, "250 8BITMIME")
			reply(ext...)
		case "STARTTLS":
			reply("220 go ahead")
			conn = tls.Server(conn, s.tls)
			tp = textproto.NewConn(conn)
		case "AUTH":
			mech, initial, _ := strings.Cut(arg, " ")
			var user, pass string
			switch strings.ToUpper(mech) {
			case "PLAIN":
				b, _ := base64.StdEncoding.DecodeString(initial)
				parts := strings.Split(string(b), "\x00")
				if len(parts) == 3 {
					user, pass = parts[1], parts[2]
				}
			case "LOGIN":
				reply("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
				l, _ := tp.ReadLine()
				u, _ := base64.StdEncoding.DecodeString(l)
				reply("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
				l, _ = tp.ReadLine()
				p, _ := base64.StdEncoding.DecodeString(l)
				user, pass = string(u), string(p)
			}
			if pass != password {
				reply("535 5.7.8 authentication failed")
				continue
			}
			s.mu.Lock()
			s.users = append(s.users, mech+" "+user)
			s.mu.Unlock()
			reply("235 ok")
		case "MAIL":
			reply("250 ok")
		case "RCPT":
			if s.rcpt != "" {
				reply(s.rcpt)
			} else {
				reply("250 ok")
			}
		case "DATA":
			reply("354 go on")
			b, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.mails = append(s.mails, string(b))
			s.mu.Unlock()
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func (s *server) got() (mails, users []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.mails...), append([]string{}, s.users...)
}

func open(t *testing.T, s *server, roots *x509.CertPool, config map[string]string) *mailer {
	t.Helper()
	cfg := map[string]string{"host": "localhost", "port": s.port(), "from": "jec@example.com", "to": "a@example.com, Ops <b@example.com>"}
	for k, v := range config {
		cfg[k] = v
	}
	p, err := factory(t.Context(), providers.Instance{Config: cfg, Secrets: map[string]string{"password": password}})
	if err != nil {
		t.Fatal(err)
	}
	m := p.(*mailer)
	m.roots = roots
	m.now = func() time.Time { return time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC) }
	return m
}

var note = contracts.Note{Event: contracts.CertificateIssueFailed, Title: "Could not issue example.com, über", Subject: "[JEC] Could not issue example.com, über", Text: "Line one.\nDetails: https://jec.example.com/certificates/3"}

func TestSend(t *testing.T) {
	for _, c := range []struct {
		name   string
		srv    *server
		config map[string]string
		user   string
	}{
		{"implicit TLS with PLAIN", &server{implicit: true, auth: "PLAIN LOGIN"}, map[string]string{"security": "tls", "username": "jec@example.com"}, "PLAIN jec@example.com"},
		{"STARTTLS with LOGIN", &server{starttls: true, auth: "LOGIN"}, map[string]string{"security": "starttls", "username": "jec@example.com"}, "LOGIN jec@example.com"},
		{"a relay without sign-in", &server{}, map[string]string{"security": "none"}, ""},
	} {
		s, roots := newServer(t, c.srv)
		if err := open(t, s, roots, c.config).Notify(t.Context(), note); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		mails, users := s.got()
		if len(mails) != 1 || (c.user == "") != (len(users) == 0) || (c.user != "" && users[0] != c.user) {
			t.Fatalf("%s: mails %d, users %v", c.name, len(mails), users)
		}
		msg, err := mail.ReadMessage(strings.NewReader(mails[0]))
		if err != nil {
			t.Fatal(err)
		}
		subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
		to, _ := msg.Header.AddressList("To")
		body, _ := io.ReadAll(msg.Body)
		if subject != note.Subject || len(to) != 2 || to[1].Name != "Ops" || msg.Header.Get("X-JEC-Event") != note.Event ||
			msg.Header.Get("Date") != "Sat, 17 Oct 2026 08:00:00 +0000" || !strings.Contains(string(body), "Details: https://jec.example.com/certificates/3") {
			t.Errorf("%s: %v\n%s", c.name, msg.Header, body)
		}
	}
}

func TestSendErrors(t *testing.T) {
	for _, c := range []struct {
		name      string
		srv       *server
		config    map[string]string
		key       i18n.Key
		permanent bool
	}{
		{"a wrong password", &server{implicit: true, auth: "PLAIN"}, map[string]string{"security": "tls", "username": "jec"}, i18n.EmailErrorAuth, true},
		{"no STARTTLS", &server{}, map[string]string{"security": "starttls"}, i18n.EmailErrorStarttls, true},
		{"no AUTH", &server{implicit: true}, map[string]string{"security": "tls", "username": "jec"}, i18n.EmailErrorNoAuth, true},
		{"a refused recipient", &server{rcpt: "550 5.1.1 no such user"}, map[string]string{"security": "none"}, i18n.EmailErrorRejected, true},
		{"a busy server", &server{greeting: "421 4.3.2 try later"}, map[string]string{"security": "none"}, i18n.EmailErrorServer, false},
		{"TLS on a plain port", &server{}, map[string]string{"security": "tls"}, i18n.EmailErrorTls, true},
		{"bad recipients", &server{}, map[string]string{"security": "none", "to": "a@, b"}, i18n.EmailErrorRecipients, true},
	} {
		s, roots := newServer(t, c.srv)
		m := open(t, s, roots, c.config)
		if c.name == "a wrong password" {
			m.password = "wrong"
		}
		err := m.Notify(t.Context(), note)
		var p *contracts.Problem
		if !errors.As(err, &p) || p.Key != c.key || retry.IsPermanent(err) != c.permanent {
			t.Errorf("%s: %v", c.name, err)
		}
	}

	// An untrusted certificate, and nobody listening.
	s, _ := newServer(t, &server{implicit: true})
	err := open(t, s, nil, map[string]string{"security": "tls"}).Notify(t.Context(), note)
	var p *contracts.Problem
	if !errors.As(err, &p) || p.Key != i18n.EmailErrorCertificate || !retry.IsPermanent(err) {
		t.Errorf("an untrusted certificate: %v", err)
	}
	port := s.port()
	_ = s.ln.Close()
	m := open(t, s, nil, map[string]string{"security": "none", "port": port})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := m.Notify(ctx, note); !errors.As(err, &p) || p.Key != i18n.EmailErrorConnect || retry.IsPermanent(err) {
		t.Errorf("nobody listening: %v", err)
	}
}

func TestPasswordInTheClear(t *testing.T) {
	// LOGIN refuses to send the password unencrypted to another machine.
	a := &loginAuth{username: "u", password: "p", host: "mail.example.com"}
	if _, _, err := a.Start(&smtp.ServerInfo{Name: "mail.example.com"}); !errors.Is(err, errUnencrypted) {
		t.Errorf("unencrypted: %v", err)
	}
}

func TestDefaultPort(t *testing.T) {
	for security, want := range map[string]string{"tls": "465", "starttls": "587", "none": "25"} {
		p, _ := factory(t.Context(), providers.Instance{Config: map[string]string{"security": security}})
		if got := p.(*mailer).port; got != want {
			t.Errorf("%s: port %s", security, got)
		}
	}
}

func TestMessageParts(t *testing.T) {
	m := &mailer{now: func() time.Time { return time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC) }}
	note := contracts.Note{Event: "certificate.issued", Title: "New certificate for <a.example.com>", Subject: "[JEC] New certificate",
		Text: "Ends on 2027-01-15.\nDetails: https://jec.example.com/certificates/3?x=1&y=2"}
	raw, err := m.message(note, &mail.Address{Address: "jec@example.com"}, []*mail.Address{{Address: "ops@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("content type %q: %v", msg.Header.Get("Content-Type"), err)
	}
	r := multipart.NewReader(msg.Body, params["boundary"])
	var parts []string
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(p) // the reader decodes quoted-printable
		parts = append(parts, p.Header.Get("Content-Type")+"\n"+strings.ReplaceAll(string(body), "\r\n", "\n"))
	}
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "text/plain; charset=utf-8\n"+note.Text) {
		t.Fatalf("parts %q", parts)
	}
	html := parts[1]
	for _, want := range []string{"text/html; charset=utf-8", "<h1", "New certificate for &lt;a.example.com&gt;",
		`<a href="https://jec.example.com/certificates/3?x=1&amp;y=2"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the HTML lacks %q:\n%s", want, html)
		}
	}
}
