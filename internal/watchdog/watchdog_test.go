// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package watchdog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendChecksInWithTheKeyAndOnlyWhatIsNeeded(t *testing.T) {
	var got map[string]any
	var auth, agent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, agent = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	err := Send(context.Background(), srv.Client(), srv.URL, "k3y", Checkin{Server: "web-1", Address: "203.0.113.10",
		Version: "1.2.3", Email: []string{"ops@example.com"}, Every: 60})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer k3y" || agent != "brynko-pulse/1.2.3" {
		t.Fatalf("headers: %q %q", auth, agent)
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	if len(got) != 6 || got["server"] != "web-1" || got["every"] != float64(60) || got["goodbye"] != nil {
		t.Fatalf("body = %v (keys %v): only the server, its address, the version, who to tell and the interval", got, keys)
	}
}

func TestSendSaysWhatWentWrongWithoutTheURLOrKey(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
		text   string
	}{
		{http.StatusUnauthorized, ErrKey, ""},
		{http.StatusNotFound, ErrKey, ""},
		{http.StatusPaymentRequired, ErrCredits, ""},
		{http.StatusInternalServerError, nil, "the relay answered 500 Internal Server Error: busy"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte("busy"))
		}))
		err := Send(context.Background(), srv.Client(), srv.URL, "secret-key", Checkin{Version: "1"})
		srv.Close()
		if tc.want != nil && !errors.Is(err, tc.want) || tc.text != "" && (err == nil || err.Error() != tc.text) {
			t.Fatalf("%d: %v", tc.status, err)
		}
		if strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), srv.URL) {
			t.Fatalf("%d: the error shows the key or the URL: %v", tc.status, err)
		}
	}
	if err := Send(context.Background(), http.DefaultClient, "https://x.invalid", "", Checkin{}); err == nil || !strings.Contains(err.Error(), "key is empty") {
		t.Fatalf("empty key: %v", err)
	}
}
