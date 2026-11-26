// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package changes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

var t0 = time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)

func newRec(t *testing.T, o Options) (*Recorder, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := NewRecorder(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	return r, dir
}

func all(t *testing.T, dir string) []host.Change {
	t.Helper()
	list, err := Read(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestTheSameChangeWithinAnHourIsRecordedOnce(t *testing.T) {
	r, dir := newRec(t, Options{})
	login := host.Change{Kind: host.ChangeLogin, Who: "deploy", What: "signed in from 10.0.0.5 with a key", Source: "journal"}
	for _, at := range []time.Time{t0, t0.Add(10 * time.Minute), t0.Add(59 * time.Minute), t0.Add(61 * time.Minute)} {
		login.At = at
		r.Emit(login)
	}
	if got := all(t, dir); len(got) != 2 || !got[0].At.Equal(t0.Add(61*time.Minute)) {
		t.Fatalf("want the first and the one after an hour, got %+v", got)
	}

	// A restart remembers the last hour, so the same sign-in is not recorded again.
	now := time.Now().UTC()
	login.At = now.Add(-5 * time.Minute)
	r.Emit(login)
	r2, err := NewRecorder(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	login.At = now
	r2.Emit(login)
	if got := all(t, dir); len(got) != 3 {
		t.Fatalf("a restart recorded the same change again: %d", len(got))
	}
}

func TestReadIsNewestFirstFromSince(t *testing.T) {
	r, dir := newRec(t, Options{})
	for i := range 3 {
		r.Emit(host.Change{At: t0.Add(time.Duration(i) * time.Hour), Kind: host.ChangeSudo, Who: "a", What: fmt.Sprint(i)})
	}
	got, err := Read(dir, t0.Add(30*time.Minute))
	if err != nil || len(got) != 2 || got[0].What != "2" || got[1].What != "1" {
		t.Fatalf("%v %+v", err, got)
	}
	if got, err := Read(t.TempDir(), t0); err != nil || got != nil {
		t.Fatalf("no file means no changes: %v %v", got, err)
	}
}

func TestOldChangesArePrunedAndTheFileIsCapped(t *testing.T) {
	r, dir := newRec(t, Options{})
	now := time.Now().UTC()
	r.Emit(host.Change{At: now.Add(-Keep - time.Hour), Kind: host.ChangeSudo, Who: "a", What: "old"})
	r.Emit(host.Change{At: now.Add(-time.Hour), Kind: host.ChangeSudo, Who: "a", What: "recent"})
	if err := prune(dir, now); err != nil {
		t.Fatal(err)
	}
	if got := all(t, dir); len(got) != 1 || got[0].What != "recent" {
		t.Fatalf("%+v", got)
	}

	// Past maxBytes the oldest go until it is three quarters full.
	f, _ := os.OpenFile(filepath.Join(dir, timelineFile), os.O_APPEND|os.O_WRONLY, 0)
	pad := strings.Repeat("x", 2000)
	for i := range maxBytes/2000 + 100 {
		b, _ := json.Marshal(host.Change{At: now.Add(-time.Duration(20000-i) * time.Second), Kind: host.ChangeFile,
			What: fmt.Sprint(i), Detail: pad})
		_, _ = f.Write(append(b, '\n'))
	}
	f.Close()
	if err := prune(dir, now); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(dir, timelineFile))
	got := all(t, dir)
	if fi.Size() > maxBytes*3/4 || got[0].What != "recent" {
		t.Fatalf("size %d, newest %q", fi.Size(), got[0].What)
	}
}

func TestARestartIsRecordedOnce(t *testing.T) {
	up := 10 * time.Minute
	r, dir := newRec(t, Options{Uptime: func() (time.Duration, error) { return up, nil }})
	now := time.Now()
	r.CheckBoot(now)
	r.CheckBoot(now.Add(30 * time.Second)) // the same boot, read a little later
	up = time.Minute
	r.CheckBoot(now.Add(time.Hour))
	got := all(t, dir)
	if len(got) != 2 || got[0].Kind != host.ChangeReboot || got[0].What != "the machine started" {
		t.Fatalf("%+v", got)
	}
	r.save()
	if st := loadState(dir); st.Boot.IsZero() {
		t.Fatal("the boot time is saved")
	}
}

func TestAMachineUpForDaysIsNotARestart(t *testing.T) {
	r, dir := newRec(t, Options{Uptime: func() (time.Duration, error) { return 40 * 24 * time.Hour, nil }})
	r.CheckBoot(time.Now())
	if got := all(t, dir); len(got) != 0 {
		t.Fatalf("a start long before pulse first ran is not news: %+v", got)
	}
}

func TestConfigFileChanges(t *testing.T) {
	etc := t.TempDir()
	conf := filepath.Join(etc, "app.conf")
	sites := filepath.Join(etc, "sites")
	_ = os.Mkdir(sites, 0o755)
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(conf, "listen 80\nworkers 2\ndb_password = hunter2\n")
	write(filepath.Join(sites, "a"), "server a\n")
	write(filepath.Join(etc, ".env"), "TOKEN=abc\n")
	r, dir := newRec(t, Options{Files: func() []string {
		return []string{conf, filepath.Join(sites, "*"), filepath.Join(etc, ".env"), filepath.Join(etc, "missing.conf")}
	}})

	r.ScanFiles(t0)
	if got := all(t, dir); len(got) != 0 {
		t.Fatalf("the first scan only remembers: %+v", got)
	}
	copyText, _ := os.ReadFile(r.copyPath(conf))
	if strings.Contains(string(copyText), "hunter2") || !strings.Contains(string(copyText), "db_password = ****") {
		t.Fatalf("the kept copy is masked: %q", copyText)
	}
	if _, err := os.Stat(r.copyPath(filepath.Join(etc, ".env"))); err == nil {
		t.Fatal("a secrets file is never copied")
	}

	r.Emit(host.Change{At: t0.Add(time.Minute), Kind: host.ChangeSudo, Who: "ubuntu", What: "vim app.conf", Detail: "in " + etc})
	write(conf, "listen 8080\nworkers 2\ndb_password = hunter3\n")
	write(filepath.Join(etc, ".env"), "TOKEN=xyz\n")
	write(filepath.Join(sites, "b"), "server b\n")
	r.ScanFiles(t0.Add(2 * time.Minute))
	r.ScanFiles(t0.Add(3 * time.Minute)) // nothing new: nothing recorded
	_ = os.Remove(filepath.Join(sites, "a"))
	r.ScanFiles(t0.Add(4 * time.Minute))

	byWhat := map[string]host.Change{}
	for _, c := range all(t, dir) {
		if c.Kind == host.ChangeFile {
			byWhat[c.What] = c
		}
	}
	if len(byWhat) != 4 {
		t.Fatalf("want changed, changed, added, removed: %+v", byWhat)
	}
	c := byWhat[conf+" changed"]
	if c.Who != "ubuntu" || !strings.Contains(c.Detail, "Probably by: sudo vim app.conf") ||
		!strings.Contains(c.Detail, "- listen 80") || !strings.Contains(c.Detail, "+ listen 8080") ||
		strings.Contains(c.Detail, "workers") || strings.Contains(c.Detail, "hunter") {
		t.Fatalf("%+v", c)
	}
	if c := byWhat[filepath.Join(etc, ".env")+" changed"]; !strings.Contains(c.Detail, "may hold secrets") || strings.Contains(c.Detail, "xyz") {
		t.Fatalf("%+v", c)
	}
	if _, ok := byWhat[filepath.Join(sites, "b")+" added"]; !ok {
		t.Fatal("a new file in a watched folder is a change")
	}
	if _, ok := byWhat[filepath.Join(sites, "a")+" removed"]; !ok {
		t.Fatal("a removed file is a change")
	}
}

func TestOnlyHiddenValuesChanged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "compose.yml")
	_ = os.WriteFile(p, []byte("environment:\n  DB_PASSWORD: one\n"), 0o644)
	r, dir := newRec(t, Options{Files: func() []string { return []string{p} }})
	r.ScanFiles(t0)
	_ = os.WriteFile(p, []byte("environment:\n  DB_PASSWORD: two\n"), 0o644)
	r.ScanFiles(t0.Add(time.Minute))
	got := all(t, dir)
	if len(got) != 1 || !strings.Contains(got[0].Detail, "Only hidden values") {
		t.Fatalf("%+v", got)
	}
}

func TestMaskConfig(t *testing.T) {
	cases := map[string]string{
		"DB_PASSWORD=s3cret":                  "DB_PASSWORD=****",
		"  password: \"x y\"":                 "  password: ****",
		"- MYSQL_ROOT_PASSWORD=abc":           "- MYSQL_ROOT_PASSWORD=****",
		"api_key = abc":                       "api_key = ****",
		"url: postgres://app:pw@db/x":         "url: postgres://app:****@db/x",
		"listen 80;":                          "listen 80;",
		"ssl_certificate_key /etc/ssl/k.pem;": "ssl_certificate_key /etc/ssl/k.pem;",
	}
	for in, want := range cases {
		if got := maskConfig(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestDockerEventsThatAreChanges(t *testing.T) {
	ev := func(typ, action string, attrs map[string]string) dockerEvent {
		var e dockerEvent
		e.Type, e.Action, e.Actor.ID, e.Actor.Attributes, e.Time = typ, action, "sha256:0123456789abcdef", attrs, t0.Unix()
		return e
	}
	web := map[string]string{"name": "web", "image": "nginx:1.27", "com.docker.compose.project": "shop", "oldName": "/web-old"}
	cases := map[string]dockerEvent{
		"web created from nginx:1.27": ev("container", "create", web),
		"web removed":                 ev("container", "destroy", web),
		"web renamed from web-old":    ev("container", "rename", web),
		"web stopped":                 ev("container", "stop", web),
		"image 0123456789ab deleted":  ev("image", "delete", nil),
	}
	for want, e := range cases {
		c, ok := dockerChange(e)
		if !ok || c.What != want || c.Kind != host.ChangeContainer || !c.At.Equal(t0) {
			t.Errorf("%s: %+v", want, c)
		}
	}
	if c, _ := dockerChange(ev("container", "create", web)); c.Detail != "compose project shop" {
		t.Errorf("%+v", c)
	}
	for _, action := range []string{"die", "start", "exec_start: sh -c ls", "health_status: healthy", "kill"} {
		if _, ok := dockerChange(ev("container", action, web)); ok {
			t.Errorf("%s is not a change", action)
		}
	}
}

// toServer sends the Docker client's requests to a test server.
type toServer struct{ url string }

func (s toServer) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(s.url)
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(req)
}

func TestDockerIsPolledFromWhereItLeftOff(t *testing.T) {
	var asked []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/events":
			asked = append(asked, req.URL.Query())
			fmt.Fprintf(w, `{"Type":"container","Action":"create","Actor":{"ID":"1","Attributes":{"name":"api","image":"app:2","%s":"/srv/app/compose.yml,/srv/app/override.yml"}},"time":%d}`+"\n", composeLabel, t0.Unix())
			fmt.Fprintf(w, `{"Type":"container","Action":"start","Actor":{"ID":"1","Attributes":{"name":"api"}},"time":%d}`+"\n", t0.Unix())
		case "/containers/json":
			fmt.Fprintf(w, `[{"Labels":{"%s":"/srv/db/compose.yml"}}]`, composeLabel)
		}
	}))
	defer srv.Close()
	r, dir := newRec(t, Options{Docker: &http.Client{Transport: toServer{srv.URL}}})
	now := time.Now()
	r.learnComposeFiles(context.Background())
	r.pollDocker(context.Background(), now)
	r.pollDocker(context.Background(), now.Add(30*time.Second))

	if len(asked) != 2 || asked[1].Get("since") != fmt.Sprint(now.Unix()) {
		t.Fatalf("the second poll starts where the first ended: %v", asked)
	}
	if got := all(t, dir); len(got) != 1 || got[0].What != "api created from app:2" {
		t.Fatalf("%+v", got)
	}
	for _, f := range []string{"/srv/app/compose.yml", "/srv/app/override.yml", "/srv/db/compose.yml"} {
		if !r.compose[f] {
			t.Errorf("compose file %s is watched", f)
		}
	}
}

// fakeSource emits what it is given, saves a cursor, and then fails or reports it does not apply.
type fakeSource struct {
	got  chan string
	emit []host.Change
	err  error
}

func (fakeSource) Name() string { return "fake" }
func (f fakeSource) Follow(_ context.Context, cursor string, emit func(host.Change), checkpoint func(string)) error {
	f.got <- cursor
	for _, c := range f.emit {
		emit(c)
	}
	checkpoint("pos-7")
	return f.err
}

func TestSourcesReportAndSaveTheirPlace(t *testing.T) {
	src := fakeSource{got: make(chan string, 2), err: fmt.Errorf("not here: %w", host.ErrUnsupported),
		emit: []host.Change{{At: time.Now(), Kind: host.ChangePackage, What: "installed htop", Source: "apt"}}}
	r, dir := newRec(t, Options{Sources: []host.ChangeSource{src}, Every: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()
	if c := <-src.got; c != "" {
		t.Fatalf("the first run starts fresh, got %q", c)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(all(t, dir)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if got := all(t, dir); len(got) != 1 || got[0].What != "installed htop" {
		t.Fatalf("%+v", got)
	}
	if st := loadState(dir); st.Cursors["fake"] != "pos-7" {
		t.Fatalf("the position is saved: %+v", st.Cursors)
	}
	select {
	case c := <-src.got:
		t.Fatalf("a source that does not apply is not started again (got %q)", c)
	default:
	}
}
