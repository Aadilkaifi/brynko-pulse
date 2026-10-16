// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package monitor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
)

type fakePlatform struct {
	platform.Platform
	disk host.Disk
	mem  host.Memory
	cpu  []host.CPUTimes
	svc  host.Service
	err  error
}

func (f *fakePlatform) DiskUsage(string) (host.Disk, error) { return f.disk, f.err }
func (f *fakePlatform) Memory() (host.Memory, error)        { return f.mem, f.err }
func (f *fakePlatform) CPUTimes() (host.CPUTimes, error) {
	t := f.cpu[0]
	f.cpu = f.cpu[1:]
	return t, nil
}
func (f *fakePlatform) Service(context.Context, string) (host.Service, error) { return f.svc, f.err }

func check(t *testing.T, c Checker) rule.Values {
	t.Helper()
	v, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDiskLikeDf(t *testing.T) {
	// 100 GB disk, 90 used, 5 reserved for root, 5 available: df shows 95%.
	gb := uint64(1 << 30)
	p := &fakePlatform{disk: host.Disk{TotalBytes: 100 * gb, UsedBytes: 90 * gb, FreeBytes: 5 * gb}}
	v := check(t, &disk{p: p, path: "/"})
	if got := v["used_percent"].(float64); got < 94.7 || got > 94.8 {
		t.Fatalf("used_percent = %v, want 94.7", got)
	}
}

func TestMemory(t *testing.T) {
	p := &fakePlatform{mem: host.Memory{TotalBytes: 1000, AvailableBytes: 250}}
	if v := check(t, &memory{p: p}); v["used_percent"] != 75.0 {
		t.Fatalf("used_percent = %v", v["used_percent"])
	}
}

func TestCPUNeedsTwoSamples(t *testing.T) {
	p := &fakePlatform{cpu: []host.CPUTimes{{Idle: 100, Total: 200}, {Idle: 150, Total: 400}}}
	c := &cpu{p: p}
	if _, err := c.Check(context.Background()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("first sample err = %v", err)
	}
	// 200 ticks passed, 50 idle: 75% busy.
	if v := check(t, c); v["used_percent"] != 75.0 {
		t.Fatalf("used_percent = %v", v["used_percent"])
	}
}

func TestService(t *testing.T) {
	p := &fakePlatform{svc: host.Service{Active: false, State: "failed", Restarts: 3}}
	v := check(t, &service{p: p, name: "nginx"})
	if v["active"] != false || v["state"] != "failed" || v["restarts"] != 3.0 {
		t.Fatalf("values = %v", v)
	}
}

func TestPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	open := ln.Addr().(*net.TCPAddr).Port
	if v := check(t, &port{host: "127.0.0.1", port: open, timeout: time.Second}); v["open"] != true {
		t.Fatalf("open port: %v", v)
	}
	ln.Close()
	if v := check(t, &port{host: "127.0.0.1", port: open, timeout: time.Second}); v["open"] != false {
		t.Fatalf("closed port: %v", v)
	}
}

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/down" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/health" {
			fmt.Fprint(w, `{"data": {"status": "degraded", "queue": 12}}`)
			return
		}
		fmt.Fprint(w, "hello world")
	}))
	defer srv.Close()

	tests := []struct {
		name string
		m    config.Monitor
		ok   bool
	}{
		{"default ok", config.Monitor{URL: srv.URL}, true},
		{"503 fails", config.Monitor{URL: srv.URL + "/down"}, false},
		{"expected 503", config.Monitor{URL: srv.URL + "/down", ExpectStatus: []byte("[503]")}, true},
		{"body matches", config.Monitor{URL: srv.URL, BodyContains: "world"}, true},
		{"body missing", config.Monitor{URL: srv.URL, BodyContains: "absent"}, false},
		{"unreachable", config.Monitor{URL: "http://127.0.0.1:1", Timeout: config.Duration(time.Second)}, false},
		{"json read only", config.Monitor{URL: srv.URL + "/health", JSONPath: "data.status"}, true},
		{"json as expected", config.Monitor{URL: srv.URL + "/health", JSONPath: "data.status", JSONExpect: "== degraded"}, true},
		{"json not as expected", config.Monitor{URL: srv.URL + "/health", JSONPath: "data.status", JSONExpect: "== ok"}, false},
		{"json number", config.Monitor{URL: srv.URL + "/health", JSONPath: "data.queue", JSONExpect: "< 100"}, true},
		{"json field missing", config.Monitor{URL: srv.URL + "/health", JSONPath: "data.nope", JSONExpect: "!= x"}, false},
	}
	for _, tc := range tests {
		c, err := newHTTP(tc.m)
		if err != nil {
			t.Fatal(err)
		}
		if v := check(t, c); v["ok"] != tc.ok {
			t.Errorf("%s: ok = %v, values %v", tc.name, v["ok"], v)
		}
	}
}

func TestSSLSelfSigned(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	v := check(t, &ssl{host: srv.Listener.Addr().String(), timeout: 5 * time.Second})
	if v["valid"] != false || v["days_left"].(float64) < 1 {
		t.Fatalf("self-signed cert: %v", v)
	}
}

func TestFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "backup")
	if _, err := (&file{path: p}).Check(context.Background()); err == nil {
		t.Fatal("missing file should be an error")
	}
	os.WriteFile(p, []byte("abc"), 0o600)
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(p, old, old)
	v := check(t, &file{path: p})
	if v["size_bytes"] != 3.0 || v["age"].(float64) < 7100 {
		t.Fatalf("values = %v", v)
	}
}

// TestHelperProcess is run as a child process by the command tests.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("PULSE_HELPER") == "" {
		return
	}
	switch os.Getenv("PULSE_HELPER") {
	case "number":
		fmt.Println("42 items")
	case "fail":
		os.Exit(3)
	case "sleep":
		time.Sleep(10 * time.Second)
	case "flood":
		for i := 0; i < 100000; i++ {
			fmt.Print("xxxxxxxxxx")
		}
	}
	os.Exit(0)
}

func helper(t *testing.T, mode, parse string, timeout time.Duration) Checker {
	t.Setenv("PULSE_HELPER", mode)
	return newCommand(config.Monitor{Run: []string{os.Args[0], "-test.run=TestHelperProcess"}, Parse: parse,
		Timeout: config.Duration(timeout)}, Deps{OutputLimit: 1024})
}

func TestCommand(t *testing.T) {
	if v := check(t, helper(t, "number", "number", 10*time.Second)); v["value"] != 42.0 || v["exit_code"] != 0.0 {
		t.Fatalf("number: %v", v)
	}
	if v := check(t, helper(t, "fail", "exit_code", 10*time.Second)); v["exit_code"] != 3.0 {
		t.Fatalf("fail: %v", v)
	}
	if _, err := helper(t, "sleep", "exit_code", 300*time.Millisecond).Check(context.Background()); err == nil {
		t.Fatal("a command over its timeout must fail")
	}
	c := helper(t, "flood", "text", 10*time.Second)
	if v := check(t, c); len(v["text"].(string)) > 1024 {
		t.Fatal("output must be capped")
	}
}

func TestCommandMissingProgram(t *testing.T) {
	c := newCommand(config.Monitor{Run: []string{"/no/such/program"}}, Deps{OutputLimit: 1024})
	if _, err := c.Check(context.Background()); err == nil {
		t.Fatal("missing program must fail")
	}
}

type refusingPlatform struct{ *fakePlatform }

func (refusingPlatform) PrepareCommand(*exec.Cmd) error { return errors.New("refused") }

func TestCommandIsPreparedByThePlatform(t *testing.T) {
	c := newCommand(config.Monitor{Run: []string{os.Args[0], "-test.run=TestHelperProcess"}},
		Deps{OutputLimit: 1024, Platform: refusingPlatform{&fakePlatform{}}})
	t.Setenv("PULSE_HELPER", "number")
	if _, err := c.Check(context.Background()); err == nil || err.Error() != "refused" {
		t.Fatalf("a refusal from the platform must stop the check, got %v", err)
	}
}

func TestHeartbeat(t *testing.T) {
	dir := t.TempDir()
	h := &heartbeat{dir: dir, id: "job", every: time.Hour, grace: time.Minute, started: time.Now().Add(-2 * time.Hour)}
	if v := check(t, h); v["late"] != true {
		t.Fatalf("never pinged: %v", v)
	}
	os.MkdirAll(filepath.Dir(HeartbeatFile(dir, "job")), 0o750)
	os.WriteFile(HeartbeatFile(dir, "job"), nil, 0o640)
	if v := check(t, h); v["late"] != false {
		t.Fatalf("just pinged: %v", v)
	}
}

func TestContainer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket")
	}
	sock := filepath.Join(t.TempDir(), "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/web/json" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"RestartCount":4,"State":{"Status":"restarting","Running":false,"ExitCode":137,"OOMKilled":true}}`)
	})}
	go srv.Serve(ln)
	defer srv.Close()
	c := newContainer("web", nil).(*container)
	c.client.Transport.(*http.Transport).DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}
	v := check(t, c)
	if v["running"] != false || v["restarts"] != 4.0 || v["oom_killed"] != true {
		t.Fatalf("values = %v", v)
	}
	c.name = "missing"
	if _, err := c.Check(context.Background()); err == nil {
		t.Fatal("missing container must fail")
	}
}

func TestNewCoversEveryType(t *testing.T) {
	types := []string{"cpu", "memory", "load", "swap", "disk", "service", "container", "process",
		"port", "http", "ssl", "file", "command", "heartbeat", "log"}
	for _, typ := range types {
		m := config.Monitor{ID: "x", Type: typ, Run: []string{"true"}, URL: "http://x", Port: 1,
			Source: &config.LogSource{File: "/x"}, Pattern: "e"}
		if c, _, err := New(m, Deps{Platform: &fakePlatform{}}); err != nil || c == nil {
			t.Errorf("New(%s) = %v, %v", typ, c, err)
		}
	}
	if _, _, err := New(config.Monitor{Type: "nope"}, Deps{}); err == nil {
		t.Error("an unknown type must fail")
	}
}
