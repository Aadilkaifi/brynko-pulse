// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package monitor

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
)

type port struct {
	host    string
	port    int
	timeout time.Duration
}

func (c *port) Check(ctx context.Context) (rule.Values, error) {
	d := net.Dialer{Timeout: c.timeout}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(c.host, strconv.Itoa(c.port)))
	if err != nil {
		return rule.Values{"open": false}, nil
	}
	conn.Close()
	return rule.Values{"open": true, "latency_ms": float64(time.Since(start).Milliseconds())}, nil
}

const maxBody = 1 << 20

type httpCheck struct {
	client   *http.Client
	url      string
	method   string
	statuses []int // empty means any 2xx or 3xx
	contains string
	jsonPath []string // json_path split on dots; empty when not set
	expect   *rule.Expect
}

func newHTTP(m config.Monitor) (Checker, error) {
	c := &httpCheck{url: m.URL, method: or(m.Method, http.MethodGet), contains: m.BodyContains}
	if m.JSONPath != "" {
		c.jsonPath = strings.Split(m.JSONPath, ".")
	}
	if m.JSONExpect != "" {
		e, err := rule.ParseExpect(m.JSONExpect)
		if err != nil {
			return nil, fmt.Errorf("json_expect: %w", err)
		}
		c.expect = &e
	}
	if len(m.ExpectStatus) > 0 {
		var one int
		if err := json.Unmarshal(m.ExpectStatus, &one); err == nil {
			c.statuses = []int{one}
		} else if err := json.Unmarshal(m.ExpectStatus, &c.statuses); err != nil {
			return nil, fmt.Errorf("expect_status: %w", err)
		}
	}
	follow := m.FollowRedirects == nil || *m.FollowRedirects
	c.client = &http.Client{
		Timeout:   orDur(m.Timeout, 10*time.Second),
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 10 * time.Second},
	}
	if !follow {
		c.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return c, nil
}

func (c *httpCheck) Check(ctx context.Context) (rule.Values, error) {
	req, err := http.NewRequestWithContext(ctx, c.method, c.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "brynko-pulse")
	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return rule.Values{"ok": false, "status": 0.0, "error": shortErr(err)}, nil
	}
	defer resp.Body.Close()
	ok := resp.StatusCode >= 200 && resp.StatusCode < 400
	if len(c.statuses) > 0 {
		ok = false
		for _, s := range c.statuses {
			ok = ok || s == resp.StatusCode
		}
	}
	v := rule.Values{"status": float64(resp.StatusCode)}
	if c.contains != "" || len(c.jsonPath) > 0 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		if c.contains != "" {
			found := strings.Contains(string(body), c.contains)
			v["body_match"] = found
			ok = ok && found
		}
		if len(c.jsonPath) > 0 {
			val, found := jsonAt(body, c.jsonPath)
			v["json_found"] = found
			if found {
				v["json_value"] = val
			}
			// With json_expect the field is part of "answering as expected", like the status and the text.
			if c.expect != nil {
				match := found && c.expect.Met(val)
				v["json_match"] = match
				ok = ok && match
			}
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	}
	v["latency_ms"] = float64(time.Since(start).Milliseconds())
	v["ok"] = ok
	return v, nil
}

type ssl struct {
	host    string
	file    string
	timeout time.Duration
}

func (c *ssl) Check(ctx context.Context) (rule.Values, error) {
	var cert *x509.Certificate
	valid := true
	if c.file != "" {
		data, err := os.ReadFile(c.file)
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("%s does not contain a PEM certificate", c.file)
		}
		if cert, err = x509.ParseCertificate(block.Bytes); err != nil {
			return nil, err
		}
	} else {
		addr := c.host
		if _, _, err := net.SplitHostPort(addr); err != nil {
			addr = net.JoinHostPort(addr, "443")
		}
		name, _, _ := net.SplitHostPort(addr)
		var err error
		cert, err = peerCert(ctx, addr, name, c.timeout, false)
		if err != nil {
			// Still report the expiry of an invalid certificate.
			valid = false
			if cert, err = peerCert(ctx, addr, name, c.timeout, true); err != nil {
				return nil, err
			}
		}
	}
	days := time.Until(cert.NotAfter).Hours() / 24
	return rule.Values{"days_left": float64(int(days)), "valid": valid && days > 0}, nil
}

func peerCert(ctx context.Context, addr, name string, timeout time.Duration, skipVerify bool) (*x509.Certificate, error) {
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: timeout},
		Config: &tls.Config{ServerName: name, InsecureSkipVerify: skipVerify}} //nolint:gosec // only to read expiry
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("server sent no certificate")
	}
	return certs[0], nil
}

func shortErr(err error) string {
	var ue interface{ Timeout() bool }
	if errors.As(err, &ue) && ue.Timeout() {
		return "timed out"
	}
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		s = s[i+2:]
	}
	return s
}

// jsonAt reads the value at path (object keys, or list positions as numbers) in a
// JSON document. Numbers come back as float64 and everything else as text, so a
// condition can compare either; a missing path or a body that is not JSON is not found.
func jsonAt(body []byte, path []string) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var cur any
	if err := dec.Decode(&cur); err != nil {
		return nil, false
	}
	for _, key := range path {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[key]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	switch t := cur.(type) {
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f, true
		}
		return t.String(), true
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case nil:
		return "null", true
	default:
		out, _ := json.Marshal(t)
		return string(out), true
	}
}
