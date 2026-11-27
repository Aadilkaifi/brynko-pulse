// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package changes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

const (
	dockerCursor = "docker"
	composeLabel = "com.docker.compose.project.config_files"
)

// dockerEvent is one entry of the engine's /events.
type dockerEvent struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
	Time int64 `json:"time"`
}

// pollDocker reads the engine's events since the last poll. The engine keeps a short history of
// events, so a poll every 30 seconds loses nothing and needs no open stream.
func (r *Recorder) pollDocker(ctx context.Context, now time.Time) {
	r.mu.Lock()
	since, err := strconv.ParseInt(r.st.Cursors[dockerCursor], 10, 64)
	r.mu.Unlock()
	if err != nil || since <= 0 {
		since = now.Add(-backfill).Unix()
	}
	until := now.Unix()
	if until <= since {
		return
	}
	q := url.Values{}
	q.Set("since", strconv.FormatInt(since, 10))
	q.Set("until", strconv.FormatInt(until, 10))
	q.Set("filters", `{"type":["container","image"]}`)
	events, err := dockerGet[dockerEvent](ctx, r.o.Docker, "/events?"+q.Encode(), true)
	if err != nil {
		r.setProblem(dockerCursor, err)
		r.warnOnce("docker", "changes: Docker events not read", err)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearProblemLocked(dockerCursor)
	for _, e := range events {
		if f := e.Actor.Attributes[composeLabel]; f != "" {
			r.addComposeFiles(f)
		}
		if c, ok := dockerChange(e); ok {
			r.emit(c)
		}
	}
	r.st.Cursors[dockerCursor] = strconv.FormatInt(until, 10)
	r.dirty = true
}

// learnComposeFiles reads the compose files of every container from its labels, so they are
// fingerprinted with the other configuration files.
func (r *Recorder) learnComposeFiles(ctx context.Context) {
	type summary struct {
		Labels map[string]string `json:"Labels"`
	}
	list, err := dockerGet[[]summary](ctx, r.o.Docker, "/containers/json?all=1", false)
	if err != nil || len(list) == 0 {
		if err != nil {
			r.setProblem(dockerCursor, err)
			r.warnOnce("docker", "changes: Docker containers not read", err)
		}
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range list[0] {
		if f := c.Labels[composeLabel]; f != "" {
			r.addComposeFiles(f)
		}
	}
}

func (r *Recorder) addComposeFiles(list string) {
	for _, f := range strings.Split(list, ",") {
		if f = strings.TrimSpace(f); f != "" && len(r.compose) < maxWatched {
			r.compose[f] = true
		}
	}
}

// dockerGet asks the engine for path. With stream set the answer is a run of JSON values (events);
// otherwise it is one value.
func dockerGet[T any](ctx context.Context, client *http.Client, path string, stream bool) ([]T, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Docker (is the pulse user in the docker group?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker engine answered %s", resp.Status)
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 16<<20))
	var out []T
	for {
		var v T
		if err := dec.Decode(&v); errors.Is(err, io.EOF) {
			return out, nil
		} else if err != nil {
			return out, fmt.Errorf("read Docker answer: %w", err)
		}
		out = append(out, v)
		if !stream {
			return out, nil
		}
	}
}

// dockerChange turns an engine event into a change, for the events that mean one: a container
// created, removed, renamed, its settings changed, stopped, restarted or paused, and an image
// pulled or deleted. A container stopping on its own ("die") is a failure, not a change, and is
// left to container monitors.
func dockerChange(e dockerEvent) (host.Change, bool) {
	a := e.Actor.Attributes
	name := a["name"]
	if name == "" {
		name = shortID(e.Actor.ID)
	}
	action, _, _ := strings.Cut(e.Action, ":") // "exec_start: sh" and the like carry a suffix
	var what string
	switch e.Type + " " + action {
	case "container create":
		what = name + " created from " + a["image"]
	case "container destroy":
		what = name + " removed"
	case "container rename":
		what = name + " renamed from " + strings.TrimPrefix(a["oldName"], "/")
	case "container update":
		what = name + " settings changed"
	case "container stop":
		what = name + " stopped"
	case "container restart":
		what = name + " restarted"
	case "container pause":
		what = name + " paused"
	case "container unpause":
		what = name + " resumed"
	case "image pull":
		what = "image " + e.Actor.ID + " pulled"
	case "image delete":
		what = "image " + shortID(e.Actor.ID) + " deleted"
	default:
		return host.Change{}, false
	}
	detail := ""
	if p := a["com.docker.compose.project"]; p != "" {
		detail = "compose project " + p
	}
	return host.Change{At: time.Unix(e.Time, 0).UTC(), Kind: host.ChangeContainer, What: what, Detail: detail,
		Source: "docker"}, true
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
