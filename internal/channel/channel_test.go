// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package channel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/config"
)

var sample = alert.Alert{Server: "web-1", Monitor: "disk", MonitorName: "disk", MonitorType: "disk",
	Kind: alert.KindFiring, Severity: "critical", Detail: "used_percent is 91", At: time.Now()}

func TestWebhookSignature(t *testing.T) {
	var gotBody []byte
	var gotSig, gotTS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig, gotTS = r.Header.Get("X-Pulse-Signature"), r.Header.Get("X-Pulse-Timestamp")
	}))
	defer srv.Close()
	t.Setenv("HOOK_SECRET", "s3cret")
	s, err := New(config.Channel{Type: "webhook", URL: srv.URL, Secret: "${HOOK_SECRET}"}, nil, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), sample); err != nil {
		t.Fatal(err)
	}
	if gotSig != "sha256="+Sign("s3cret", gotTS, gotBody) {
		t.Fatal("signature does not verify")
	}
	var a alert.Alert
	if json.Unmarshal(gotBody, &a) != nil || a.Monitor != "disk" {
		t.Fatalf("body = %s", gotBody)
	}
}

func TestTelegramErrorsHideToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/botTOKEN123/sendMessage") {
			t.Errorf("path = %s", r.URL.Path)
		}
		http.Error(w, `{"ok":false,"description":"chat not found"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	tg := &telegram{client: srv.Client(), token: "TOKEN123", chatID: "1", api: srv.URL}
	err := tg.Send(context.Background(), sample)
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("err = %v", err)
	}
	tg.api = "http://127.0.0.1:1"
	err = tg.Send(context.Background(), sample)
	if err == nil || strings.Contains(err.Error(), "TOKEN123") {
		t.Fatalf("the error must not reveal the token: %v", err)
	}
}

func TestSlack(t *testing.T) {
	var text string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		json.NewDecoder(r.Body).Decode(&b)
		text = b["text"]
	}))
	defer srv.Close()
	if err := (&slack{client: srv.Client(), url: srv.URL}).Send(context.Background(), sample); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "CRITICAL on web-1") {
		t.Fatalf("text = %q", text)
	}
	if err := (&slack{client: srv.Client()}).Send(context.Background(), sample); err == nil {
		t.Fatal("empty URL must fail")
	}
}

func TestNotAvailableTypes(t *testing.T) {
	for _, typ := range []string{"brynko"} {
		if _, err := New(config.Channel{Type: typ}, nil, nil); !errors.Is(err, ErrNotAvailable) {
			t.Errorf("%s: %v", typ, err)
		}
	}
}

// fakeSMTP accepts one message and returns what was received.
func fakeSMTP(t *testing.T, starttls bool) (string, <-chan string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r, w := bufio.NewReader(conn), conn
		say := func(s string) { io.WriteString(w, s+"\r\n") }
		say("220 fake ready")
		var data strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				if starttls {
					say("250-fake")
					say("250 STARTTLS")
				} else {
					say("250 fake")
				}
			case strings.HasPrefix(cmd, "DATA"):
				say("354 go")
				for {
					l, _ := r.ReadString('\n')
					if l == ".\r\n" {
						break
					}
					data.WriteString(l)
				}
				say("250 queued")
			case strings.HasPrefix(cmd, "QUIT"):
				say("221 bye")
				got <- data.String()
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSMTPPlain(t *testing.T) {
	addr, got := fakeSMTP(t, false)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	json.Unmarshal([]byte(portStr), &port)
	s := newSMTP(config.Channel{Host: host, Port: port, TLS: "none", From: "a@x.io", To: []string{"b@x.io"}}, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Send(ctx, sample); err != nil {
		t.Fatal(err)
	}
	msg := <-got
	for _, want := range []string{"Subject: [Critical] web-1: disk: used_percent is 91", "multipart/alternative",
		"Content-Type: text/plain; charset=utf-8", "Content-Type: text/html; charset=utf-8", "CRITICAL on web-1"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message misses %q:\n%s", want, msg)
		}
	}
}

func TestSMTPRequiresStartTLS(t *testing.T) {
	addr, _ := fakeSMTP(t, false)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	json.Unmarshal([]byte(portStr), &port)
	s := newSMTP(config.Channel{Host: host, Port: port, From: "a@x.io", To: []string{"b@x.io"}}, "")
	if err := s.Send(context.Background(), sample); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("plain server with default starttls must fail, got %v", err)
	}
}

func TestSMTPHeaderInjection(t *testing.T) {
	s := &smtpSender{from: "a@x.io\r\nBcc: evil@x.io", to: []string{"b@x.io"}}
	if strings.Contains(string(s.message(sample)), "\r\nBcc:") {
		t.Fatal("header injection through from")
	}
}

func TestWhatsApp(t *testing.T) {
	var got []map[string]any
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		got = append(got, m)
	}))
	defer srv.Close()
	t.Setenv("PULSE_WA", "tok")
	s, err := New(config.Channel{Type: "whatsapp", PhoneNumberID: "123", AccessToken: "${PULSE_WA}",
		Template: "pulse_alert", To: []string{"+919876543210", "+15550001111"}}, nil, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	s.(*whatsapp).api = srv.URL
	a := sample
	a.Detail = "line one\n\tline     two"
	if err := s.Send(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || auth != "Bearer tok" || path != "/123/messages" || got[0]["to"] != "919876543210" {
		t.Fatalf("auth %q path %q got %v", auth, path, got)
	}
	b, _ := json.Marshal(got[0])
	if !strings.Contains(string(b), `"text":"line one line two"`) || !strings.Contains(string(b), `"code":"en"`) {
		t.Fatalf("template body: %s", b)
	}
}

func TestTemplateText(t *testing.T) {
	if got := templateText(strings.Repeat("é", 50), 10); got != strings.Repeat("é", 7)+"..." {
		t.Fatalf("got %q", got)
	}
	if templateText("  ", 10) != "-" {
		t.Fatal("empty text must not be empty, WhatsApp refuses it")
	}
}
