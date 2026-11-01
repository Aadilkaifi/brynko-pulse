// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package channel

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/config"
)

type smtpSender struct {
	host, mode         string
	port               int
	username, password string
	from               string
	to                 []string
	tlsConfig          *tls.Config // nil means verify against the system roots
}

func newSMTP(c config.Channel, password string) *smtpSender {
	port := c.Port
	if port == 0 {
		port = 587
	}
	mode := c.TLS
	if mode == "" {
		mode = "starttls"
	}
	return &smtpSender{host: c.Host, port: port, mode: mode, username: c.Username,
		password: password, from: c.From, to: c.To}
}

func (s *smtpSender) tlsConf() *tls.Config {
	if s.tlsConfig != nil {
		return s.tlsConfig
	}
	return &tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}
}

func (s *smtpSender) Send(ctx context.Context, a alert.Alert) error {
	c, err := s.envelope(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(s.message(a)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// Check does everything Send does up to the message itself: connect, encrypt, log in, and have the
// server accept the sender and every recipient. Then it resets and hangs up, so nobody receives
// anything. A wrong host, port, password or sender shows here; an address the server accepts now
// and bounces later does not, which only a real message can prove.
func (s *smtpSender) Check(ctx context.Context) error {
	c, err := s.envelope(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Reset(); err != nil {
		return fmt.Errorf("reset: %w", err)
	}
	return c.Quit()
}

// envelope connects and runs the SMTP conversation up to the accepted recipients.
func (s *smtpSender) envelope(ctx context.Context) (*smtp.Client, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	addr := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	dialer := &net.Dialer{Deadline: deadline}

	var conn net.Conn
	var err error
	if s.mode == "tls" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: s.tlsConf()}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	_ = conn.SetDeadline(deadline)

	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("smtp greeting from %s: %w", addr, err)
	}
	if err := s.converse(c); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (s *smtpSender) converse(c *smtp.Client) error {
	if s.mode == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the server does not offer STARTTLS; set tls: tls or tls: none")
		}
		if err := c.StartTLS(s.tlsConf()); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if s.username != "" {
		// PlainAuth refuses to send the password over an unencrypted connection.
		if err := c.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
			return fmt.Errorf("login as %s: %w", s.username, err)
		}
	}
	if err := c.Mail(s.from); err != nil {
		return fmt.Errorf("sender %s refused: %w", s.from, err)
	}
	for _, to := range s.to {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("recipient %s refused: %w", to, err)
		}
	}
	return nil
}

func (s *smtpSender) message(a alert.Alert) []byte {
	clean := strings.NewReplacer("\r", " ", "\n", " ")
	crlf := func(t string) string { return strings.ReplaceAll(strings.ReplaceAll(t, "\r\n", "\n"), "\n", "\r\n") }
	boundary := "pulse-" + randomHex(12)
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", clean.Replace(s.from))
	fmt.Fprintf(&b, "To: %s\r\n", clean.Replace(strings.Join(s.to, ", ")))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", clean.Replace(a.Subject())))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	// Both forms: mail apps show the HTML, and fall back to the text when they cannot.
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n", boundary)
	b.WriteString(crlf(a.Text()))
	fmt.Fprintf(&b, "\r\n--%s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n", boundary)
	b.WriteString(crlf(a.HTML()))
	fmt.Fprintf(&b, "\r\n--%s--\r\n", boundary)
	return []byte(b.String())
}

// randomHex is a MIME boundary nothing in the message can contain by chance.
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}
