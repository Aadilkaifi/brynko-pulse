// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package changes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAFailingSourceIsReportedUntilItWorks(t *testing.T) {
	src := fakeSource{got: make(chan string, 4), err: errors.New("no access to the journal")}
	r, dir := newRec(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.follow(ctx, src) }()
	<-src.got
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.save()
		if Problems(dir)["fake"] != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	r.save()
	if got := Problems(dir)["fake"]; got != "no access to the journal" {
		t.Fatalf("problem: %q", got)
	}
	r.checkpoint("fake", "pos-8")
	r.save()
	if got := Problems(dir); len(got) != 0 {
		t.Fatalf("a source that moves on works again: %v", got)
	}
}

func TestDockerProblemsComeAndGo(t *testing.T) {
	up := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !up {
			http.Error(w, "permission denied", http.StatusForbidden)
		}
	}))
	defer srv.Close()
	r, dir := newRec(t, Options{Docker: &http.Client{Transport: toServer{srv.URL}}})
	now := time.Now()
	r.pollDocker(context.Background(), now)
	r.save()
	if got := Problems(dir)["docker"]; !strings.Contains(got, "403") {
		t.Fatalf("problem: %q", got)
	}
	up = true
	r.pollDocker(context.Background(), now.Add(time.Minute))
	r.save()
	if got := Problems(dir); len(got) != 0 {
		t.Fatalf("cleared: %v", got)
	}
}

func TestNoStateMeansNoProblems(t *testing.T) {
	if got := Problems(t.TempDir()); len(got) != 0 {
		t.Fatal(got)
	}
}
