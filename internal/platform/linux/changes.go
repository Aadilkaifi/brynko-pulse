// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

package linux

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// backfill is how far back a source reads the first time, so the timeline is not empty on day one.
const backfill = 24 * time.Hour

// ChangeSources reads sudo and sshd from the journal and package changes from apt's history.
func (Platform) ChangeSources(log *slog.Logger) []host.ChangeSource {
	return []host.ChangeSource{journalChanges{log: log}, aptChanges{path: "/var/log/apt/history.log", every: time.Minute}}
}

// ConfigFiles are common server configuration files. Files that do not exist, or that the service
// account cannot read, are skipped.
func (Platform) ConfigFiles() []string {
	return []string{
		"/etc/hosts", "/etc/fstab", "/etc/crontab", "/etc/cron.d/*",
		"/etc/ssh/sshd_config", "/etc/ssh/sshd_config.d/*", "/etc/sudoers", "/etc/sudoers.d/*",
		"/etc/nginx/nginx.conf", "/etc/nginx/conf.d/*.conf", "/etc/nginx/sites-enabled/*",
		"/etc/apache2/apache2.conf", "/etc/apache2/sites-enabled/*",
		"/etc/docker/daemon.json", "/etc/systemd/system/*.service",
		"/etc/mysql/my.cnf", "/etc/mysql/mysql.conf.d/*.cnf", "/etc/redis/redis.conf",
		"/etc/postgresql/*/main/postgresql.conf", "/etc/postgresql/*/main/pg_hba.conf",
	}
}

// ── sudo and sign-ins from the journal ──

type journalChanges struct{ log *slog.Logger }

func (journalChanges) Name() string { return "journal" }

// Follow reads sudo and sshd entries after the saved journal cursor and keeps following. A cursor
// the journal no longer has (it was vacuumed) starts over from the last day.
func (j journalChanges) Follow(ctx context.Context, cursor string, emit func(host.Change), checkpoint func(string)) error {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return errors.New("journalctl was not found, so sudo commands and sign-ins are not recorded")
	}
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		read, err := runChangeJournal(ctx, cursor, func(c host.Change, next string) {
			cursor = next
			if c.Kind != "" {
				emit(c)
			}
			checkpoint(next)
		})
		if ctx.Err() != nil {
			return nil
		}
		if read == 0 && cursor != "" && time.Since(started) < 10*time.Second {
			j.log.Warn("changes: the journal does not have the saved position any more; reading the last day again")
			cursor = ""
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		j.log.Warn("changes: journalctl stopped; starting it again", "error", fmt.Sprint(err), "in", backoff.String())
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	return nil
}

func runChangeJournal(ctx context.Context, cursor string, on func(host.Change, string)) (int, error) {
	args := []string{"--follow", "--output=json", "--quiet",
		"SYSLOG_IDENTIFIER=sudo", "SYSLOG_IDENTIFIER=sshd", "SYSLOG_IDENTIFIER=sshd-session"}
	if cursor != "" {
		args = append(args, "--after-cursor="+cursor)
	} else {
		args = append(args, "--since="+time.Now().Add(-backfill).Format("2006-01-02 15:04:05"))
	}
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	read := 0
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		c, next, ok := journalChange(sc.Bytes())
		if ok {
			read++
			on(c, next)
		}
	}
	return read, cmd.Wait()
}

// journalChange reads one journal entry in JSON. It returns the entry's cursor, and the change when
// the entry is one (a sudo command, a sign-in).
func journalChange(line []byte) (host.Change, string, bool) {
	var e struct {
		Cursor   string `json:"__CURSOR"`
		Realtime string `json:"__REALTIME_TIMESTAMP"`
		Ident    string `json:"SYSLOG_IDENTIFIER"`
		Message  any    `json:"MESSAGE"`
	}
	if json.Unmarshal(line, &e) != nil || e.Cursor == "" {
		return host.Change{}, "", false
	}
	msg, _ := e.Message.(string) // an entry that is not text is not one we read
	usec, _ := strconv.ParseInt(e.Realtime, 10, 64)
	at := time.UnixMicro(usec).UTC()
	var c host.Change
	switch e.Ident {
	case "sudo":
		c, _ = host.ParseSudo(at, msg)
	case "sshd", "sshd-session":
		c, _ = host.ParseLogin(at, msg)
	}
	return c, e.Cursor, true
}

// ── packages from apt's history ──

type aptChanges struct {
	path  string
	every time.Duration
}

func (aptChanges) Name() string { return "apt" }

// Follow reads new blocks of the history file each minute. The cursor is "inode:offset", so a file
// rotated since the last read is finished from history.log.1 before the new one is read.
func (a aptChanges) Follow(ctx context.Context, cursor string, emit func(host.Change), checkpoint func(string)) error {
	if _, err := os.Stat(a.path); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %w", a.path, host.ErrUnsupported) // not a Debian or Ubuntu system
	}
	ino, off := parseFileCursor(cursor)
	since := time.Time{}
	if cursor == "" {
		since = time.Now().Add(-backfill)
	}
	tick := time.NewTicker(a.every)
	defer tick.Stop()
	for {
		ino, off = a.read(ino, off, func(c host.Change) {
			if !c.At.Before(since) {
				emit(c)
			}
		})
		checkpoint(fmt.Sprintf("%d:%d", ino, off))
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func (a aptChanges) read(ino uint64, off int64, emit func(host.Change)) (uint64, int64) {
	cur, size, err := inode(a.path)
	if err != nil {
		return ino, off
	}
	if ino != 0 && cur != ino {
		if old, _, err := inode(a.path + ".1"); err == nil && old == ino {
			readAptFrom(a.path+".1", off, emit)
		}
		off = 0
	}
	if size < off {
		off = 0 // the file was emptied in place
	}
	return cur, readAptFrom(a.path, off, emit)
}

// readAptFrom parses the whole blocks after off and returns where the next read starts: after the
// last block apt has finished writing.
func readAptFrom(path string, off int64, emit func(host.Change)) int64 {
	f, err := os.Open(path)
	if err != nil {
		return off
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return off
	}
	b, err := io.ReadAll(io.LimitReader(f, 32<<20))
	if err != nil {
		return off
	}
	end := strings.LastIndex(string(b), "\nEnd-Date: ")
	if end < 0 {
		return off
	}
	nl := strings.IndexByte(string(b[end+1:]), '\n')
	if nl < 0 {
		return off
	}
	done := end + 1 + nl + 1
	for _, c := range host.ParseAptHistory(string(b[:done]), time.Local) {
		emit(c)
	}
	return off + int64(done)
}

func inode(path string) (uint64, int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fi.Size(), nil
	}
	return st.Ino, fi.Size(), nil
}

func parseFileCursor(c string) (uint64, int64) {
	a, b, ok := strings.Cut(c, ":")
	if !ok {
		return 0, 0
	}
	ino, err1 := strconv.ParseUint(a, 10, 64)
	off, err2 := strconv.ParseInt(b, 10, 64)
	if err1 != nil || err2 != nil || off < 0 {
		return 0, 0
	}
	return ino, off
}
