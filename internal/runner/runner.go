// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package runner is the long-running loop behind `pulse run`: it schedules
// every monitor, feeds results to the engine, and sends what the engine decides.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/channel"
	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/duration"
	"github.com/Aadilkaifi/brynko-pulse/internal/engine"
	"github.com/Aadilkaifi/brynko-pulse/internal/heal"
	"github.com/Aadilkaifi/brynko-pulse/internal/maintenance"
	"github.com/Aadilkaifi/brynko-pulse/internal/monitor"
	"github.com/Aadilkaifi/brynko-pulse/internal/notify"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
	"github.com/Aadilkaifi/brynko-pulse/internal/silence"
	"github.com/Aadilkaifi/brynko-pulse/internal/store"
	"github.com/Aadilkaifi/brynko-pulse/internal/watchdog"
)

const (
	saveEvery = 5 * time.Second
	fixPoll   = 2 * time.Second // how often approvals from pulse fix are looked for
	// watchdogFirst is the first check-in after start: soon, so the relay knows at once
	// that the server is back after a reboot.
	watchdogFirst = 3 * time.Second
	checkTimeout  = 90 * time.Second
	maxJitter     = 5 * time.Second
	stopTimeout   = 15 * time.Second
)

// Options configure Run.
type Options struct {
	ConfigDir  string
	DataDir    string
	Platform   platform.Platform
	Log        *slog.Logger
	Reload     <-chan struct{} // a value means: re-read the configuration
	Version    string
	HTTPClient *http.Client
	// SkipSecurityCheck is for tests only.
	SkipSecurityCheck bool
}

// Load reads and checks the configuration the way Run does.
func Load(o Options) (*config.Config, error) {
	cfg, res := config.Load(o.ConfigDir)
	if !res.OK() {
		msgs := make([]string, 0, len(res.Errors))
		for _, is := range res.Errors {
			loc := is.File
			if is.Line > 0 {
				loc = fmt.Sprintf("%s:%d", is.File, is.Line)
			}
			msgs = append(msgs, strings.TrimSpace(loc+" "+is.Message))
		}
		return nil, fmt.Errorf("the configuration has errors (run pulse validate):\n  %s", strings.Join(msgs, "\n  "))
	}
	if !o.SkipSecurityCheck {
		if err := o.Platform.CheckConfigSecure(o.ConfigDir); err != nil {
			return nil, fmt.Errorf("refusing to start: %w", err)
		}
		if err := o.Platform.CheckSecretFile(config.SecretsPath(o.ConfigDir)); err != nil {
			return nil, fmt.Errorf("refusing to start: %w", err)
		}
	}
	return cfg, nil
}

// Run monitors until ctx ends. It returns an error only if it cannot start.
func Run(ctx context.Context, o Options) error {
	cfg, err := Load(o)
	if err != nil {
		return err
	}
	st, err := store.Open(o.DataDir)
	if err != nil {
		return err
	}
	saved := map[string]engine.State{}
	var channels map[string]store.ChannelStatus
	if snap, err := store.ReadState(o.DataDir); err == nil && snap != nil {
		for id, m := range snap.Monitors {
			saved[id] = m.State
		}
		if channelsUnchangedSince(o.ConfigDir, snap.Updated) {
			channels = snap.Channels
		}
	}
	r := &runner{o: o, st: st, eng: engine.New(saved), started: time.Now(), savedChannels: channels,
		silences: silence.NewWatcher(o.DataDir), held: map[string]bool{},
		heals: heal.NewTracker(cfg.Limits.MaxHealsPerHour)}

	saveCtx, stopSaving := context.WithCancel(context.Background())
	saveDone := make(chan struct{})
	go func() { defer close(saveDone); r.saveLoop(saveCtx) }()
	fixDone := make(chan struct{})
	go func() { defer close(fixDone); r.serveFixes(saveCtx) }()
	watchDone := make(chan struct{})
	go func() { defer close(watchDone); r.runWatchdog(saveCtx) }()
	defer func() {
		stopSaving()
		<-saveDone
		<-fixDone
		<-watchDone
		r.save()
	}()

	for {
		gen := r.start(ctx, cfg)
		// Report at once, not at the first tick: right after an install `pulse status` would otherwise
		// find no state and say pulse is not running.
		r.save()
		o.Log.Info("pulse is running", "server", cfg.ServerName, "monitors", gen.running, "channels", len(cfg.Channels))
		for reloaded := false; !reloaded; {
			select {
			case <-ctx.Done():
				gen.stop()
				o.Log.Info("pulse stopped")
				return nil
			case <-o.Reload:
				next, err := Load(o)
				if err != nil {
					o.Log.Error("reload failed; still using the previous configuration", "error", err.Error())
					continue
				}
				gen.stop()
				cfg, reloaded = next, true
				o.Log.Info("configuration reloaded")
			}
		}
	}
}

type runner struct {
	o       Options
	st      *store.Store
	eng     *engine.Engine
	started time.Time
	current atomic.Pointer[generation]

	silences *silence.Watcher
	heldMu   sync.Mutex
	held     map[string]bool // monitors with alerts held back by a silence

	heals *heal.Tracker // self-healing: what it has done, across reloads

	savedChannels map[string]store.ChannelStatus // delivery health from before the last stop; used once

	watchMu  sync.Mutex
	watchdog *store.WatchdogStatus // nil while the watchdog is off
}

// generation is everything built from one version of the configuration.
type generation struct {
	cfg     *config.Config
	disp    *notify.Dispatcher
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running int
	windows []maintenance.Window

	mu     sync.Mutex
	status map[string]store.MonitorStatus

	plans  map[string]heal.Plan // monitors with a heal rule
	fixer  heal.Fixer
	nudges map[string]func() // checks a monitor now, such as after an approval
}

func (r *runner) start(parent context.Context, cfg *config.Config) *generation {
	ctx, cancel := context.WithCancel(parent)
	client := r.o.HTTPClient
	if client == nil {
		client = channelClient()
	}
	g := &generation{cfg: cfg, cancel: cancel, status: map[string]store.MonitorStatus{},
		disp: notify.New(cfg, client, r.o.Log), plans: map[string]heal.Plan{}, nudges: map[string]func(){},
		fixer: heal.Fixer{Platform: r.o.Platform, OutputLimit: cfg.Limits.CommandOutputBytes}}
	r.heals.SetLimit(cfg.Limits.MaxHealsPerHour)
	windows, err := maintenance.Compile(cfg.Maintenance)
	if err != nil { // validation catches this first; never let it stop monitoring
		r.o.Log.Error("maintenance windows ignored", "error", err.Error())
	}
	g.windows = windows
	if prev := r.current.Load(); prev != nil {
		g.disp.KeepStatus(prev.disp)
	} else if r.savedChannels != nil {
		g.disp.Restore(r.savedChannels)
		r.savedChannels = nil
	}
	g.disp.OnFailure(r.undelivered)
	g.disp.Start(ctx)

	deps := monitor.Deps{Platform: r.o.Platform, DataDir: r.o.DataDir,
		OutputLimit: cfg.Limits.CommandOutputBytes, Started: r.started, Log: r.o.Log,
		Docker: monitor.NewDockerEvents(ctx, r.o.Log)}
	if needsServiceEvents(cfg) {
		if w, err := r.o.Platform.NewServiceWatcher(ctx, r.o.Log); err != nil {
			r.o.Log.Info("service changes are checked at each interval only", "reason", err.Error())
		} else {
			deps.Services = w
		}
	}
	ids := map[string]bool{}
	for _, raw := range cfg.Monitors {
		m := raw.Effective(cfg.Defaults)
		ids[m.ID] = true
		ms := store.MonitorStatus{Name: m.DisplayName(), Type: m.Type, File: m.SourceFile,
			Severity: m.Severity, Interval: duration.Format(m.Interval.D()), Enabled: m.IsEnabled()}
		if !ms.Enabled {
			g.status[m.ID] = ms
			continue
		}
		chk, defaultAlert, err := monitor.New(m, deps)
		var alertC, clearC *rule.Condition
		if err == nil {
			alertC, clearC, err = conditions(m, defaultAlert)
		}
		if err != nil {
			ms.Error = err.Error()
			g.status[m.ID] = ms
			r.o.Log.Warn("monitor not started", "monitor", m.ID, "reason", err.Error())
			continue
		}
		g.status[m.ID] = ms
		if p, ok := heal.PlanFor(m); ok {
			g.plans[m.ID] = p
		}
		g.running++
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			r.loop(ctx, g, m, chk, alertC, clearC)
		}()
	}
	r.eng.Retain(ids)
	r.heals.Retain(ids)
	r.current.Store(g)
	return g
}

func needsServiceEvents(cfg *config.Config) bool {
	for _, m := range cfg.Monitors {
		if m.Type == "service" && m.IsEnabled() {
			return true
		}
	}
	return false
}

func (g *generation) stop() {
	g.cancel()
	g.wg.Wait()
	g.disp.Stop(stopTimeout)
}

// ruleKey names a monitor's conditions, so the engine notices when they change.
func ruleKey(alertC, clearC *rule.Condition) string {
	k := alertC.Raw
	if clearC != nil {
		k += " | " + clearC.Raw
	}
	return k
}

func conditions(m config.Monitor, defaultAlert string) (alertC, clearC *rule.Condition, err error) {
	src := m.AlertWhen
	if src == "" {
		src = defaultAlert
	}
	if src == "" {
		return nil, nil, errors.New("alert_when is required for this monitor type")
	}
	a, err := rule.Parse(src)
	if err != nil {
		return nil, nil, fmt.Errorf("alert_when: %w", err)
	}
	if m.ClearWhen != "" {
		c, err := rule.Parse(m.ClearWhen)
		if err != nil {
			return nil, nil, fmt.Errorf("clear_when: %w", err)
		}
		clearC = &c
	}
	return &a, clearC, nil
}

// minGap keeps a burst of events, such as a container restarting in a loop,
// from turning into a burst of checks.
const minGap = 250 * time.Millisecond

func (r *runner) loop(ctx context.Context, g *generation, m config.Monitor, chk monitor.Checker, alertC, clearC *rule.Condition) {
	kick := make(chan struct{}, 1)
	nudge := func() {
		select {
		case kick <- struct{}{}:
		default: // a check is already due
		}
	}
	g.mu.Lock()
	g.nudges[m.ID] = nudge
	g.mu.Unlock()
	if w, ok := chk.(monitor.Watcher); ok {
		go w.Watch(ctx, nudge)
	}
	interval := m.Interval.D()
	// Spread the first checks so every monitor does not run in the same instant.
	jitter := time.Duration(rand.Int64N(int64(min(interval/2, maxJitter)) + 1))
	t := time.NewTimer(jitter)
	defer t.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-kick:
			if wait := minGap - time.Since(last); wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
		}
		last = time.Now()
		r.checkOnce(ctx, g, m, chk, alertC, clearC, nudge)
		t.Reset(interval)
	}
}

func (r *runner) checkOnce(ctx context.Context, g *generation, m config.Monitor, chk monitor.Checker, alertC, clearC *rule.Condition, nudge func()) {
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	values, err := chk.Check(cctx)
	cancel()
	if errors.Is(err, monitor.ErrNotReady) || ctx.Err() != nil {
		return
	}

	var obs engine.Observation
	if err != nil {
		obs.Bad, obs.Detail = true, "check failed: "+err.Error()
	} else if bad, eerr := alertC.Eval(values); eerr != nil {
		obs.Bad, obs.Detail = true, eerr.Error()
	} else {
		obs.Bad, obs.Detail = bad, monitor.Describe(m.Type, *alertC, values)
		if clearC != nil {
			ok, cerr := clearC.Eval(values)
			obs.Clear = cerr == nil && ok
		}
	}

	now := time.Now()
	changes, window := g.cfg.Defaults.FlapLimits()
	ev := r.eng.Observe(m.ID, engine.Rule{For: m.For.D(), Cooldown: m.Cooldown.D(), HasClear: clearC != nil,
		Key: ruleKey(alertC, clearC), FlapChanges: changes, FlapWindow: window}, now, obs)

	g.mu.Lock()
	ms := g.status[m.ID]
	ms.Values = rounded(values)
	g.status[m.ID] = ms
	g.mu.Unlock()

	sil, silenced := r.silenced(g, m.ID, now)
	if !silenced && r.release(m.ID) && ev == nil {
		// The silence is over: a problem it hid must not stay unreported.
		if ev = r.eng.Remind(m.ID, now); ev != nil {
			ev.Detail = "Still failing after the silence ended. " + ev.Detail
		}
	}
	var hs heal.Step
	if p, ok := g.plans[m.ID]; ok {
		hs = r.healStep(ctx, g, m, p, now, ev, nudge)
		ev = hs.Send
	} else if held := r.heals.Take(m.ID); held != nil && ev == nil {
		ev = held // its heal rule was removed while its alert was held back
	}
	if ev == nil {
		return
	}
	info, _ := monitor.Info(m.Type)
	kind, severity := string(ev.Kind), m.Severity
	if hs.Kind != "" {
		kind = hs.Kind
	}
	if kind == alert.KindHealFailed {
		severity = "critical"
	}
	a := alert.Alert{Server: g.cfg.ServerName, ServerAddress: g.cfg.ServerAddress, Monitor: m.ID,
		MonitorName: m.DisplayName(), MonitorType: m.Type, TypeLabel: info.Label,
		Kind: kind, Severity: severity, Detail: ev.Detail, Condition: alertC.Raw, Heal: hs.Note,
		Rule: monitor.DescribeCondition(m.Type, *alertC), Since: ev.Since, At: ev.At, Duration: ev.Duration}
	rec := store.Record{At: a.At, Kind: a.Kind, Monitor: m.ID, Name: a.MonitorName,
		Severity: a.Severity, Detail: historyDetail(a), Channels: []string{}}
	if silenced {
		r.hold(m.ID)
		rec.Silenced = true
		r.o.Log.Info("alert held back by a silence", "kind", a.Kind, "monitor", m.ID, "until", sil.Until.Format(time.RFC3339))
	} else {
		rec.Channels = g.disp.Dispatch(a, m.Notify)
		r.o.Log.Info("alert", "kind", a.Kind, "monitor", m.ID, "severity", a.Severity, "detail", a.Detail)
	}
	if err := r.st.Append(rec); err != nil {
		r.o.Log.Error("cannot write alert history", "error", err.Error())
	}
}

// channelsUnchangedSince reports whether channels.yaml and secrets.env are as they were
// at saved, so the channel health saved then still describes the same channels. A file
// that does not exist has not changed.
func channelsUnchangedSince(dir string, saved time.Time) bool {
	if saved.IsZero() {
		return false
	}
	for _, p := range []string{filepath.Join(dir, config.ChannelsFile), config.SecretsPath(dir)} {
		fi, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || !fi.ModTime().Before(saved) {
			return false
		}
	}
	return true
}

// undelivered records in the history that a channel could not deliver an alert, so the
// alert's own record (which lists the channels it was handed to) is not read as delivered.
func (r *runner) undelivered(f notify.Failure) {
	rec := store.Record{At: time.Now(), Kind: store.KindUndelivered, Monitor: f.Alert.Monitor,
		Name: f.Alert.MonitorName, Severity: f.Alert.Severity, Detail: f.Err, Channels: []string{f.Channel},
		AlertKind: f.Alert.Kind, AlertAt: f.Alert.At}
	if err := r.st.Append(rec); err != nil {
		r.o.Log.Error("cannot write alert history", "error", err.Error())
	}
}

// historyDetail is the alert's line in the history: with self-healing, what pulse did,
// since "Fixed" alone says less than "it failed, pulse restarted it, it works again".
func historyDetail(a alert.Alert) string {
	switch {
	case a.Heal == "":
		return a.Detail
	case a.Kind == alert.KindHealed:
		return a.Heal
	}
	return strings.TrimSuffix(a.Detail, ".") + ". " + a.Heal
}

// healStep asks the tracker what self-healing does after this check, and starts a fix
// when it says so. No fix inside a maintenance window or while the monitor flaps; a
// silence does not stop a fix, it only holds back the alerts.
func (r *runner) healStep(ctx context.Context, g *generation, m config.Monitor, p heal.Plan, now time.Time,
	ev *engine.Event, nudge func()) heal.Step {
	st := r.eng.Get(m.ID)
	blocked := ""
	if w, _, open := maintenance.Covering(g.windows, m.ID, now); open {
		blocked = "the maintenance window " + w.Name + " is open"
	} else if st.Flapping {
		blocked = "it keeps switching between failing and working"
	} else if dep := r.failingDependency(p); dep != "" {
		blocked = dep + " is failing too, which is the likely cause"
	}
	step := r.heals.Step(m.ID, p, now, st, ev, blocked)
	if !step.Start {
		return step
	}
	r.o.Log.Info("self-healing", "monitor", m.ID, "fix", p.What, "attempt", step.Attempt, "approved", step.Approved)
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		out := g.fixer.Fix(ctx, p)
		out.Attempt, out.Approved = step.Attempt, step.Approved
		r.heals.Done(m.ID, out, p.Verify)
		detail := heal.Describe(p, out)
		if out.Err != nil {
			r.o.Log.Warn("self-healing fix failed", "monitor", m.ID, "error", out.Err.Error())
		} else {
			r.o.Log.Info("self-healing fix done", "monitor", m.ID, "fix", p.Done)
		}
		if err := r.st.Append(store.Record{At: out.At, Kind: "heal", Monitor: m.ID, Name: m.DisplayName(),
			Severity: m.Severity, Detail: detail, Channels: []string{}}); err != nil {
			r.o.Log.Error("cannot write alert history", "error", err.Error())
		}
		// Look again soon: a restarted service is usually back within seconds.
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			nudge()
		}
	}()
	return step
}

// runWatchdog checks in with the brynko relay soon after start and then every
// minute, with the configuration in force at that moment, until ctx ends.
func (r *runner) runWatchdog(ctx context.Context) {
	client := r.o.HTTPClient
	if client == nil {
		client = channelClient()
	}
	wait := watchdogFirst
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = watchdog.Every
		g := r.current.Load()
		if g == nil || g.cfg.Watchdog == nil {
			r.setWatchdog(nil)
			continue
		}
		cfg := g.cfg
		emails, phones, skipped := cfg.WatchdogRecipients()
		c := watchdog.Checkin{Server: cfg.ServerName, Address: cfg.ServerAddress, Version: r.o.Version,
			Email: emails, WhatsApp: phones, Every: int(watchdog.Every / time.Second)}
		if c.Email == nil {
			c.Email = []string{}
		}
		if c.WhatsApp == nil {
			c.WhatsApp = []string{}
		}
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := watchdog.Send(sctx, client, cfg.Watchdog.Endpoint(), cfg.Secrets.Resolve(cfg.Watchdog.Key), c)
		cancel()
		r.watchMu.Lock()
		st := store.WatchdogStatus{Email: emails, WhatsApp: phones, Skipped: skipped}
		if r.watchdog != nil {
			st.LastOK, st.LastError, st.LastErrorAt = r.watchdog.LastOK, r.watchdog.LastError, r.watchdog.LastErrorAt
		}
		if err != nil {
			st.LastError, st.LastErrorAt = err.Error(), time.Now()
		} else {
			st.LastOK, st.LastError = time.Now(), ""
		}
		r.watchdog = &st
		r.watchMu.Unlock()
		if err != nil && ctx.Err() == nil {
			r.o.Log.Warn("watchdog check-in failed", "error", err.Error())
		}
	}
}

func (r *runner) setWatchdog(st *store.WatchdogStatus) {
	r.watchMu.Lock()
	r.watchdog = st
	r.watchMu.Unlock()
}

func (r *runner) watchdogStatus() *store.WatchdogStatus {
	r.watchMu.Lock()
	defer r.watchMu.Unlock()
	if r.watchdog == nil {
		return nil
	}
	st := *r.watchdog
	return &st
}

// failingDependency names the first monitor in the plan's only_if_ok that is failing
// or about to, or "" when they are all fine.
func (r *runner) failingDependency(p heal.Plan) string {
	for _, id := range p.OnlyIfOK {
		if st := r.eng.Get(id); st.Status == engine.Firing || st.Status == engine.Pending {
			return id
		}
	}
	return ""
}

// serveFixes answers `pulse fix approve` and `pulse fix dismiss` until ctx ends.
func (r *runner) serveFixes(ctx context.Context) {
	if err := heal.PrepareRequests(r.o.DataDir); err != nil {
		r.o.Log.Warn("fixes cannot be approved from the command line", "error", err.Error())
	}
	t := time.NewTicker(fixPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if err := heal.Serve(r.o.DataDir, now, r.answerFix); err != nil {
				r.o.Log.Warn("cannot read fix approvals", "error", err.Error())
			}
		}
	}
}

// answerFix approves or dismisses a waiting fix, records it and checks the monitor at once.
func (r *runner) answerFix(req heal.Request) heal.Answer {
	g := r.current.Load()
	if g == nil {
		return heal.Answer{Error: "pulse is starting; try again in a moment"}
	}
	p, ok := g.plans[req.Monitor]
	if !ok {
		return heal.Answer{Error: fmt.Sprintf("monitor %s has no fix in use", req.Monitor)}
	}
	decide, verb := r.heals.Approve, "Approved"
	if req.Action == heal.ActionDismiss {
		decide, verb = r.heals.Dismiss, "Dismissed"
	}
	w, err := decide(req.Monitor, req.FixID)
	if err != nil {
		return heal.Answer{Error: err.Error()}
	}
	r.o.Log.Info("self-healing fix "+strings.ToLower(verb), "monitor", req.Monitor, "fix", p.What)
	name, severity := req.Monitor, ""
	g.mu.Lock()
	if ms, ok := g.status[req.Monitor]; ok {
		name, severity = ms.Name, ms.Severity
	}
	nudge := g.nudges[req.Monitor]
	g.mu.Unlock()
	if err := r.st.Append(store.Record{At: time.Now(), Kind: "heal", Monitor: req.Monitor, Name: name,
		Severity: severity, Detail: fmt.Sprintf("%s: %s", verb, p.What), Channels: []string{}}); err != nil {
		r.o.Log.Error("cannot write alert history", "error", err.Error())
	}
	// Written now, not at the next save tick, so a screen that refreshes after the answer
	// no longer shows the fix as waiting.
	r.save()
	if nudge != nil && req.Action == heal.ActionApprove {
		nudge()
	}
	return heal.Answer{OK: true, FixID: w.ID, What: p.What}
}

// silenced reports the silence or maintenance window covering id, whichever
// ends last. If silences cannot be read, the last good list is used and the
// problem is logged.
func (r *runner) silenced(g *generation, id string, now time.Time) (silence.Silence, bool) {
	list, err := r.silences.Current(now)
	if err != nil {
		r.o.Log.Warn("cannot read silences", "error", err.Error())
	}
	return covering(g, list, id, now)
}

func covering(g *generation, list []silence.Silence, id string, now time.Time) (silence.Silence, bool) {
	s, ok := silence.Covering(list, id, now)
	if w, until, open := maintenance.Covering(g.windows, id, now); open && (!ok || until.After(s.Until)) {
		s, ok = silence.Silence{Monitor: id, Until: until, Reason: "maintenance window " + w.Name}, true
	}
	return s, ok
}

func (r *runner) hold(id string) {
	r.heldMu.Lock()
	r.held[id] = true
	r.heldMu.Unlock()
}

// release reports whether id had alerts held back, and forgets it.
func (r *runner) release(id string) bool {
	r.heldMu.Lock()
	defer r.heldMu.Unlock()
	was := r.held[id]
	delete(r.held, id)
	return was
}

func (r *runner) saveLoop(ctx context.Context) {
	t := time.NewTicker(saveEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.save()
		}
	}
}

func (r *runner) save() {
	g := r.current.Load()
	if g == nil {
		return
	}
	snap := store.Snapshot{Server: g.cfg.ServerName, PID: os.Getpid(), Version: r.o.Version,
		Started: r.started, Updated: time.Now(), Monitors: map[string]store.MonitorStatus{},
		Channels: g.disp.Status(), Watchdog: r.watchdogStatus()}
	states := r.eng.Snapshot()
	now := time.Now()
	silences, _ := r.silences.Current(now)
	g.mu.Lock()
	for id, ms := range g.status {
		if s, ok := states[id]; ok {
			ms.State = s
		} else if ms.Status == "" {
			ms.Status = engine.Unknown
		}
		if sil, ok := covering(g, silences, id, now); ok {
			ms.SilencedUntil, ms.SilenceReason = sil.Until, sil.Reason
		}
		if p, ok := g.plans[id]; ok {
			ms.Heal = r.heals.Status(id, p, now)
		}
		snap.Monitors[id] = ms
	}
	g.mu.Unlock()
	if err := r.st.Save(snap); err != nil {
		r.o.Log.Error("cannot save state", "error", err.Error())
	}
}

func rounded(v rule.Values) map[string]any {
	if v == nil {
		return nil
	}
	out := make(map[string]any, len(v))
	for k, x := range v {
		if f, ok := x.(float64); ok {
			x = math.Round(f*100) / 100
		}
		out[k] = x
	}
	return out
}

func channelClient() *http.Client { return channel.NewHTTPClient() }
