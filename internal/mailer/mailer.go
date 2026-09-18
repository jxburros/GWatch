// Package mailer sends alert and test emails over SMTP using only the
// standard library.
package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Message is one outgoing email.
type Message struct {
	To       []string
	Subject  string
	TextBody string
	HTMLBody string
}

const sendTimeout = 20 * time.Second

// Validate checks the SMTP settings for the fields Send needs.
func Validate(settings model.SMTPSettings) error {
	host := strings.TrimSpace(settings.Host)
	if host == "" {
		return errors.New("SMTP host is required")
	}
	if strings.ContainsAny(host, " /\\:") {
		return fmt.Errorf("SMTP host %q is not a valid hostname", settings.Host)
	}
	if settings.Port <= 0 || settings.Port > 65535 {
		return errors.New("SMTP port must be between 1 and 65535")
	}
	if strings.TrimSpace(settings.From) == "" {
		return errors.New("sender (From) address is required")
	}
	if _, err := mail.ParseAddress(strings.TrimSpace(settings.From)); err != nil {
		return fmt.Errorf("sender address %q is not a valid email address", settings.From)
	}
	switch normalizeSecurity(settings.Security) {
	case "tls", "starttls", "none":
	default:
		return fmt.Errorf("unsupported security mode %q (use starttls, tls or none)", settings.Security)
	}
	if settings.Password != "" && settings.Username == "" {
		return errors.New("a username is required when a password is set")
	}
	return nil
}

func normalizeSecurity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "starttls", "start_tls", "start-tls":
		return "starttls"
	case "tls", "ssl", "ssl/tls", "implicit":
		return "tls"
	case "none", "plain", "off":
		return "none"
	}
	return s
}

// Send delivers msg through the configured SMTP server. The security mode is
// "starttls" (default, port 587), "tls" (implicit TLS, port 465) or "none".
// Authentication uses PLAIN when a username is set, falling back to LOGIN when
// the server only advertises LOGIN.
func Send(ctx context.Context, settings model.SMTPSettings, msg Message) error {
	if err := Validate(settings); err != nil {
		return err
	}
	recipients, err := cleanRecipients(msg.To)
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(strings.TrimSpace(settings.From))
	if err != nil {
		return fmt.Errorf("sender address %q is not valid", settings.From)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	deadline, _ := ctx.Deadline()

	host := strings.TrimSpace(settings.Host)
	addr := net.JoinHostPort(host, strconv.Itoa(settings.Port))
	security := normalizeSecurity(settings.Security)
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}

	dialer := &net.Dialer{Timeout: sendTimeout}
	var conn net.Conn
	if security == "tls" {
		td := &tls.Dialer{NetDialer: dialer, Config: tlsConfig}
		conn, err = td.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("could not connect to %s: %w", addr, describeDial(err))
	}
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("SMTP greeting from %s failed: %w", addr, err)
	}
	defer client.Close()

	if err := client.Hello(localName(from.Address)); err != nil {
		return fmt.Errorf("SMTP HELO failed: %w", err)
	}

	if security == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("server %s does not support STARTTLS; choose security \"tls\" (usually port 465) or \"none\"", host)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("STARTTLS with %s failed: %w", host, err)
		}
	}

	if settings.Username != "" {
		auth, err := chooseAuth(client, host, settings.Username, settings.Password)
		if err != nil {
			return err
		}
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP authentication failed for %q: %w", settings.Username, err)
		}
	}

	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("server rejected sender %s: %w", from.Address, err)
	}
	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("server rejected recipient %s: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA failed: %w", err)
	}
	body := Build(from, recipients, msg, time.Now())
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("sending message body failed: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("server did not accept the message: %w", err)
	}
	if err := client.Quit(); err != nil {
		// The message was accepted; a failed QUIT is not worth reporting.
		return nil
	}
	return nil
}

func describeDial(err error) error {
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return fmt.Errorf("timed out after %s", sendTimeout)
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Errorf("DNS lookup failed: %s", dnsErr.Error())
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && strings.HasPrefix(msg, "dial ") {
		return errors.New(msg[i+2:])
	}
	return err
}

// chooseAuth picks PLAIN when the server advertises it (or advertises nothing
// specific) and LOGIN when that is the only supported mechanism.
func chooseAuth(client *smtp.Client, host, username, password string) (smtp.Auth, error) {
	ok, mechs := client.Extension("AUTH")
	if !ok {
		return nil, fmt.Errorf("server %s does not offer authentication; leave the username empty", host)
	}
	upper := strings.ToUpper(mechs)
	hasPlain := strings.Contains(upper, "PLAIN")
	hasLogin := strings.Contains(upper, "LOGIN")
	switch {
	case hasPlain || (!hasLogin && strings.TrimSpace(upper) == ""):
		return smtp.PlainAuth("", username, password, host), nil
	case hasLogin:
		return LoginAuth(username, password), nil
	default:
		// Try PLAIN anyway; the server will refuse if it truly cannot.
		return smtp.PlainAuth("", username, password, host), nil
	}
}

// loginAuth implements the (non-standard but widespread) LOGIN mechanism.
type loginAuth struct {
	username, password string
	step               int
}

// LoginAuth returns an smtp.Auth for the LOGIN mechanism.
func LoginAuth(username, password string) smtp.Auth {
	return &loginAuth{username: username, password: password}
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLocalhost(server.Name) {
		return "", nil, errors.New("refusing to send the password over an unencrypted connection")
	}
	a.step = 0
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(strings.TrimSpace(string(fromServer)))
	switch {
	case strings.HasPrefix(prompt, "username"):
		a.step = 1
		return []byte(a.username), nil
	case strings.HasPrefix(prompt, "password"):
		a.step = 2
		return []byte(a.password), nil
	}
	// Servers that send non-standard prompts: answer in order.
	a.step++
	switch a.step {
	case 1:
		return []byte(a.username), nil
	case 2:
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("unexpected server challenge %q", string(fromServer))
}

func isLocalhost(name string) bool {
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}

func localName(fromAddr string) string {
	if i := strings.LastIndex(fromAddr, "@"); i >= 0 && i < len(fromAddr)-1 {
		return fromAddr[i+1:]
	}
	return "localhost"
}

func cleanRecipients(to []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range to {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' }) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			a, err := mail.ParseAddress(part)
			if err != nil {
				return nil, fmt.Errorf("recipient %q is not a valid email address", part)
			}
			key := strings.ToLower(a.Address)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, a.Address)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("at least one recipient is required")
	}
	return out, nil
}

// Build renders the RFC 5322 message (headers and body) that Send writes to
// the DATA command. It is exported so tests and previews can inspect it.
func Build(from *mail.Address, recipients []string, msg Message, now time.Time) []byte {
	var b bytes.Buffer
	writeHeader := func(k, v string) {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\r\n")
	}
	writeHeader("From", from.String())
	writeHeader("To", strings.Join(recipients, ", "))
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	writeHeader("Date", now.Format(time.RFC1123Z))
	writeHeader("Message-ID", messageID(from.Address, now))
	writeHeader("MIME-Version", "1.0")
	writeHeader("X-Mailer", "GWatch")
	writeHeader("Auto-Submitted", "auto-generated")

	text := msg.TextBody
	if text == "" && msg.HTMLBody == "" {
		text = msg.Subject
	}
	if msg.HTMLBody == "" {
		writeHeader("Content-Type", "text/plain; charset=utf-8")
		writeHeader("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		b.Write(qp(text))
		return b.Bytes()
	}
	boundary := "gwatch-" + randomHex(12)
	writeHeader("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary))
	b.WriteString("\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	b.Write(qp(text))
	b.WriteString("\r\n--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	b.Write(qp(msg.HTMLBody))
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return b.Bytes()
}

func qp(s string) []byte {
	var buf bytes.Buffer
	w := quotedprintable.NewWriter(&buf)
	_, _ = w.Write([]byte(strings.ReplaceAll(s, "\r\n", "\n")))
	_ = w.Close()
	return buf.Bytes()
}

func messageID(fromAddr string, now time.Time) string {
	return fmt.Sprintf("<%d.%s@%s>", now.UnixNano(), randomHex(8), localName(fromAddr))
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}
