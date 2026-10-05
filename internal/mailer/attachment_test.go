package mailer

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestReportAttachmentMIME(t *testing.T) {
	html := "<!doctype html><html><body>Availability 99.99% &amp; a long line " + strings.Repeat("x", 200) + "</body></html>"
	encoded := Build(&mail.Address{Address: "gwatch@example.com"}, []string{"reader@example.com"}, Message{Subject: "Weekly report", TextBody: "Attached report", HTMLBody: html, HTMLAttachment: html}, time.Now())
	msg, err := mail.ReadMessage(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	typ, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || typ != "multipart/mixed" {
		t.Fatalf("outer MIME: %s %v", typ, err)
	}
	mixed := multipart.NewReader(msg.Body, params["boundary"])
	body, err := mixed.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	typ, params, err = mime.ParseMediaType(body.Header.Get("Content-Type"))
	if err != nil || typ != "multipart/alternative" {
		t.Fatalf("body MIME: %s %v", typ, err)
	}
	alternatives := multipart.NewReader(body, params["boundary"])
	for i, want := range []string{"Attached report", html} {
		part, err := alternatives.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(part)
		if err != nil || string(data) != want {
			t.Fatalf("alternative %d: %q %v", i, data, err)
		}
	}
	if _, err = alternatives.NextPart(); err != io.EOF {
		t.Fatalf("extra body part: %v", err)
	}
	attachment, err := mixed.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	disposition, params, err := mime.ParseMediaType(attachment.Header.Get("Content-Disposition"))
	if err != nil || disposition != "attachment" || params["filename"] != "gwatch-report.html" {
		t.Fatalf("attachment disposition: %v %s", err, attachment.Header)
	}
	data, err := io.ReadAll(attachment)
	if err != nil || string(data) != html {
		t.Fatal("attachment content corrupted")
	}
	if _, err = mixed.NextPart(); err != io.EOF {
		t.Fatalf("extra attachment: %v", err)
	}
}
