package mailer

import (
	"bufio"
	"context"
	"encoding/base64"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

func TestBuildAlertEmail(t *testing.T) {
	when := time.Date(2026, 9, 18, 10, 30, 0, 0, time.UTC)
	msg := BuildAlertEmail(KindDown, "GWatch", "Plex", "HTTP", model.StatusDown, "Unexpected HTTP 503 (expected 200-399)", map[string]string{"Final URL": "https://plex.local/", "Status code": "503"}, when)
	if msg.Subject != "[GWatch] DOWN: Plex — HTTP" {
		t.Errorf("subject = %q", msg.Subject)
	}
	for _, want := range []string{"DOWN", "Node:    Plex", "Check:   HTTP", "Unexpected HTTP 503", "Final URL: https://plex.local/", "Status code: 503"} {
		if !strings.Contains(msg.TextBody, want) {
			t.Errorf("text body missing %q:\n%s", want, msg.TextBody)
		}
	}
	for _, want := range []string{"<!DOCTYPE html>", "Plex", "HTTP", "Unexpected HTTP 503", "https://plex.local/", "#0f1419"} {
		if !strings.Contains(msg.HTMLBody, want) {
			t.Errorf("html body missing %q", want)
		}
	}
	// HTML escaping.
	esc := BuildAlertEmail(KindWarning, "", "N<b>", "C", model.StatusDegraded, "a < b & c", nil, when)
	if !strings.Contains(esc.HTMLBody, "N&lt;b&gt;") || !strings.Contains(esc.HTMLBody, "a &lt; b &amp; c") {
		t.Errorf("html not escaped: %s", esc.HTMLBody)
	}
	if esc.Subject != "[GWatch] WARNING: N<b> — C" {
		t.Errorf("warning subject = %q", esc.Subject)
	}

	rec := BuildAlertEmail(KindRecovered, "Home", "Router", "Ping", model.StatusUp, "4/4 replies", nil, when)
	if rec.Subject != "[Home] RECOVERED: Router — Ping" {
		t.Errorf("recovered subject = %q", rec.Subject)
	}
	cleared := BuildAlertEmail(KindWarningCleared, "GWatch", "Router", "Ping", model.StatusUp, "", nil, when)
	if cleared.Subject != "[GWatch] WARNING CLEARED: Router — Ping" {
		t.Errorf("cleared subject = %q", cleared.Subject)
	}
	test := BuildAlertEmail(KindTest, "GWatch", "", "", "", "", nil, time.Time{})
	if test.Subject != "[GWatch] Test email" || !strings.Contains(test.TextBody, "test email") {
		t.Errorf("test email = %+v", test)
	}
}

func TestBuildMessageFormat(t *testing.T) {
	from := &mail.Address{Name: "GWatch", Address: "gwatch@example.com"}
	raw := Build(from, []string{"a@example.com", "b@example.com"}, Message{Subject: "Hello — wörld", TextBody: "Plain text\nline 2", HTMLBody: "<p>HTML</p>"}, time.Now())
	m, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("message does not parse: %v\n%s", err, raw)
	}
	if got := m.Header.Get("From"); got != `"GWatch" <gwatch@example.com>` {
		t.Errorf("From = %q", got)
	}
	if got := m.Header.Get("To"); got != "a@example.com, b@example.com" {
		t.Errorf("To = %q", got)
	}
	dec := new(mime.WordDecoder)
	subj, err := dec.DecodeHeader(m.Header.Get("Subject"))
	if err != nil || subj != "Hello — wörld" {
		t.Errorf("Subject = %q (%v)", subj, err)
	}
	for _, h := range []string{"Date", "Message-ID", "MIME-Version"} {
		if m.Header.Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}
	ct := m.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/alternative; boundary=") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := string(raw)
	if !strings.Contains(body, "text/plain; charset=utf-8") || !strings.Contains(body, "text/html; charset=utf-8") || !strings.Contains(body, "<p>HTML</p>") {
		t.Errorf("multipart body incomplete:\n%s", body)
	}
	if !strings.Contains(body, "\r\n") || strings.Contains(strings.ReplaceAll(body, "\r\n", ""), "\n") {
		t.Errorf("body must use CRLF line endings only")
	}

	plain := Build(from, []string{"a@example.com"}, Message{Subject: "s", TextBody: "just text"}, time.Now())
	pm, err := mail.ReadMessage(strings.NewReader(string(plain)))
	if err != nil {
		t.Fatal(err)
	}
	if pm.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("plain Content-Type = %q", pm.Header.Get("Content-Type"))
	}
	r := quotedprintable.NewReader(pm.Body)
	buf := make([]byte, 64)
	n, _ := r.Read(buf)
	if string(buf[:n]) != "just text" {
		t.Errorf("plain body = %q", buf[:n])
	}
}

func TestValidate(t *testing.T) {
	good := model.SMTPSettings{Host: "smtp.example.com", Port: 587, From: "GWatch <gwatch@example.com>", Security: "starttls"}
	if err := Validate(good); err != nil {
		t.Errorf("good settings rejected: %v", err)
	}
	cases := []struct {
		name string
		mod  func(s *model.SMTPSettings)
		want string
	}{
		{"no host", func(s *model.SMTPSettings) { s.Host = "" }, "host"},
		{"bad host", func(s *model.SMTPSettings) { s.Host = "smtp.example.com:587" }, "host"},
		{"bad port", func(s *model.SMTPSettings) { s.Port = 0 }, "port"},
		{"no from", func(s *model.SMTPSettings) { s.From = "" }, "From"},
		{"bad from", func(s *model.SMTPSettings) { s.From = "not an address" }, "sender"},
		{"bad security", func(s *model.SMTPSettings) { s.Security = "magic" }, "security"},
		{"password without user", func(s *model.SMTPSettings) { s.Password = "x" }, "username"},
	}
	for _, tc := range cases {
		s := good
		tc.mod(&s)
		err := Validate(s)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
			t.Errorf("%s: err = %v, want containing %q", tc.name, err, tc.want)
		}
	}
}

func TestLoginAuth(t *testing.T) {
	a := LoginAuth("user", "secret")
	proto, initial, err := a.Start(&smtp.ServerInfo{Name: "localhost", TLS: false})
	if err != nil || proto != "LOGIN" || initial != nil {
		t.Fatalf("Start = %q %v %v", proto, initial, err)
	}
	resp, err := a.Next([]byte("Username:"), true)
	if err != nil || string(resp) != "user" {
		t.Errorf("username step = %q %v", resp, err)
	}
	resp, err = a.Next([]byte("Password:"), true)
	if err != nil || string(resp) != "secret" {
		t.Errorf("password step = %q %v", resp, err)
	}
	if resp, err := a.Next(nil, false); err != nil || resp != nil {
		t.Errorf("done step = %q %v", resp, err)
	}
	// Refuses plaintext to a remote host.
	if _, _, err := LoginAuth("u", "p").Start(&smtp.ServerInfo{Name: "smtp.example.com", TLS: false}); err == nil {
		t.Errorf("expected refusal over plaintext")
	}
	if _, _, err := LoginAuth("u", "p").Start(&smtp.ServerInfo{Name: "smtp.example.com", TLS: true}); err != nil {
		t.Errorf("TLS should be accepted: %v", err)
	}
	// Non-standard prompts are answered in order.
	b := LoginAuth("u2", "p2")
	_, _, _ = b.Start(&smtp.ServerInfo{Name: "localhost"})
	r1, _ := b.Next([]byte("?"), true)
	r2, _ := b.Next([]byte("?"), true)
	if string(r1) != "u2" || string(r2) != "p2" {
		t.Errorf("ordered prompts = %q %q", r1, r2)
	}
	if _, err := b.Next([]byte("?"), true); err == nil {
		t.Errorf("third challenge should fail")
	}
}

// fakeSMTP is a minimal in-process SMTP server recording one transaction.
type fakeSMTP struct {
	addr      string
	authMechs string // advertised AUTH line, e.g. "PLAIN LOGIN"
	mu        sync.Mutex
	from      string
	rcpts     []string
	data      string
	authLine  string
	loginUser string
	loginPass string
	commands  []string
}

func newFakeSMTP(t *testing.T, authMechs string) *fakeSMTP {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	f := &fakeSMTP{addr: l.Addr().String(), authMechs: authMechs}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	say := func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }
	say("220 fake.local ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.mu.Lock()
		f.commands = append(f.commands, line)
		f.mu.Unlock()
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			_, _ = w.WriteString("250-fake.local\r\n")
			if f.authMechs != "" {
				_, _ = w.WriteString("250-AUTH " + f.authMechs + "\r\n")
			}
			say("250 8BITMIME")
		case strings.HasPrefix(upper, "HELO"):
			say("250 fake.local")
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			f.mu.Lock()
			f.authLine = line
			f.mu.Unlock()
			say("235 ok")
		case strings.HasPrefix(upper, "AUTH LOGIN"):
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			u, _ := r.ReadString('\n')
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			p, _ := r.ReadString('\n')
			ub, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
			pb, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
			f.mu.Lock()
			f.loginUser, f.loginPass = string(ub), string(pb)
			f.mu.Unlock()
			say("235 ok")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			f.mu.Lock()
			f.from = line[len("MAIL FROM:"):]
			f.mu.Unlock()
			say("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			f.mu.Lock()
			f.rcpts = append(f.rcpts, line[len("RCPT TO:"):])
			f.mu.Unlock()
			say("250 ok")
		case upper == "DATA":
			say("354 go ahead")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(l)
			}
			f.mu.Lock()
			f.data = sb.String()
			f.mu.Unlock()
			say("250 queued")
		case upper == "QUIT":
			say("221 bye")
			return
		case upper == "RSET", upper == "NOOP":
			say("250 ok")
		default:
			say("500 unknown")
		}
	}
}

func TestSendAgainstFakeServer(t *testing.T) {
	f := newFakeSMTP(t, "PLAIN LOGIN")
	host, port, _ := net.SplitHostPort(f.addr)
	settings := model.SMTPSettings{Host: host, Port: atoi(port), From: "GWatch <gwatch@example.com>", Security: "none", Username: "user", Password: "secret"}
	msg := Message{To: []string{"alice@example.com", "Bob <bob@example.com>"}, Subject: "[GWatch] DOWN: Plex — HTTP", TextBody: "Plex is down.\n.leading dot line", HTMLBody: "<p>Plex is down.</p>"}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Send(ctx, settings, msg); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(f.from, "<gwatch@example.com>") {
		t.Errorf("MAIL FROM = %q", f.from)
	}
	if len(f.rcpts) != 2 || f.rcpts[0] != "<alice@example.com>" || f.rcpts[1] != "<bob@example.com>" {
		t.Errorf("RCPT TO = %v", f.rcpts)
	}
	if !strings.HasPrefix(f.authLine, "AUTH PLAIN ") {
		t.Errorf("expected PLAIN auth, got %q", f.authLine)
	} else {
		dec, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(f.authLine, "AUTH PLAIN "))
		if string(dec) != "\x00user\x00secret" {
			t.Errorf("PLAIN credentials = %q", dec)
		}
	}
	m, err := mail.ReadMessage(strings.NewReader(f.data))
	if err != nil {
		t.Fatalf("DATA does not parse: %v\n%s", err, f.data)
	}
	dec := new(mime.WordDecoder)
	subj, _ := dec.DecodeHeader(m.Header.Get("Subject"))
	if subj != msg.Subject {
		t.Errorf("Subject = %q", subj)
	}
	if to := m.Header.Get("To"); to != "alice@example.com, bob@example.com" {
		t.Errorf("To = %q", to)
	}
	if !strings.Contains(f.data, "Plex is down.") || !strings.Contains(f.data, "<p>Plex is down.</p>") {
		t.Errorf("DATA missing bodies:\n%s", f.data)
	}
	// Dot-stuffing must have been undone by the server read (we strip the
	// terminator only), so the leading-dot line arrives stuffed.
	if !strings.Contains(f.data, "..leading dot line") {
		t.Errorf("expected dot-stuffed line in DATA:\n%s", f.data)
	}
}

func TestSendUsesLoginWhenOnlyLoginOffered(t *testing.T) {
	f := newFakeSMTP(t, "LOGIN")
	host, port, _ := net.SplitHostPort(f.addr)
	settings := model.SMTPSettings{Host: host, Port: atoi(port), From: "gwatch@example.com", Security: "none", Username: "user", Password: "secret"}
	if err := Send(context.Background(), settings, Message{To: []string{"a@example.com"}, Subject: "hi", TextBody: "x"}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginUser != "user" || f.loginPass != "secret" {
		t.Errorf("LOGIN credentials = %q / %q (commands %v)", f.loginUser, f.loginPass, f.commands)
	}
}

func TestSendErrors(t *testing.T) {
	f := newFakeSMTP(t, "")
	host, port, _ := net.SplitHostPort(f.addr)
	base := model.SMTPSettings{Host: host, Port: atoi(port), From: "gwatch@example.com", Security: "none"}

	if err := Send(context.Background(), base, Message{Subject: "x"}); err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Errorf("no recipients: %v", err)
	}
	if err := Send(context.Background(), base, Message{To: []string{"nope"}}); err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Errorf("bad recipient: %v", err)
	}
	// STARTTLS requested but not offered.
	s := base
	s.Security = "starttls"
	if err := Send(context.Background(), s, Message{To: []string{"a@example.com"}}); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("starttls not offered: %v", err)
	}
	// Auth requested but not offered.
	s = base
	s.Username = "u"
	if err := Send(context.Background(), s, Message{To: []string{"a@example.com"}}); err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Errorf("auth not offered: %v", err)
	}
	// Closed port.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	_, cport, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close()
	s = base
	s.Port = atoi(cport)
	if err := Send(context.Background(), s, Message{To: []string{"a@example.com"}}); err == nil || !strings.Contains(err.Error(), "could not connect") {
		t.Errorf("closed port: %v", err)
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
