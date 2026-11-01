// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package channel

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
)

// recordingSMTP answers like a mail server, refusing RCPT for `refuse`, and returns every command
// the client sent once the connection ends.
func recordingSMTP(t *testing.T, refuse string) (string, int, <-chan []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan []string, 1)
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		say := func(s string) { io.WriteString(conn, s+"\r\n") }
		var cmds []string
		defer func() { got <- cmds }()
		say("220 fake ready")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.TrimSpace(line)
			cmds = append(cmds, cmd)
			upper := strings.ToUpper(cmd)
			switch {
			case strings.HasPrefix(upper, "EHLO"):
				say("250 fake")
			case strings.HasPrefix(upper, "RCPT") && refuse != "" && strings.Contains(cmd, refuse):
				say("550 no such user")
			case strings.HasPrefix(upper, "DATA"):
				say("354 go")
				for {
					if l, _ := r.ReadString('\n'); l == ".\r\n" {
						break
					}
				}
				say("250 queued")
			case strings.HasPrefix(upper, "QUIT"):
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return host, port, got
}

func TestSMTPCheckGoesUpToTheRecipientsAndSendsNothing(t *testing.T) {
	host, port, got := recordingSMTP(t, "")
	s := newSMTP(config.Channel{Host: host, Port: port, TLS: "none", From: "pulse@x.io",
		To: []string{"ops@x.io", "lead@x.io"}}, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(<-got, "\n")
	for _, want := range []string{"MAIL FROM:<pulse@x.io>", "RCPT TO:<ops@x.io>", "RCPT TO:<lead@x.io>", "RSET", "QUIT"} {
		if !strings.Contains(cmds, want) {
			t.Fatalf("missing %q in:\n%s", want, cmds)
		}
	}
	if strings.Contains(strings.ToUpper(cmds), "DATA") {
		t.Fatalf("a check must never send a message:\n%s", cmds)
	}
}

func TestSMTPCheckNamesARefusedRecipient(t *testing.T) {
	host, port, got := recordingSMTP(t, "typo@x.io")
	s := newSMTP(config.Channel{Host: host, Port: port, TLS: "none", From: "pulse@x.io",
		To: []string{"ops@x.io", "typo@x.io"}}, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := s.Check(ctx)
	if err == nil || !strings.Contains(err.Error(), "recipient typo@x.io refused") {
		t.Fatalf("err = %v", err)
	}
	<-got
}

func TestSMTPSendStillDelivers(t *testing.T) {
	host, port, got := recordingSMTP(t, "")
	s := newSMTP(config.Channel{Host: host, Port: port, TLS: "none", From: "pulse@x.io", To: []string{"ops@x.io"}}, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Send(ctx, sample); err != nil {
		t.Fatal(err)
	}
	if cmds := strings.Join(<-got, "\n"); !strings.Contains(cmds, "DATA") || strings.Contains(cmds, "RSET") {
		t.Fatalf("send must deliver the message:\n%s", cmds)
	}
}
