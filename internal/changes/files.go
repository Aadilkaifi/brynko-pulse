// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package changes

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

const (
	maxWatched   = 300     // files fingerprinted at most
	maxCopyBytes = 1 << 20 // larger files are fingerprinted but not copied, so no diff is shown
	maxDiffLines = 20
	maxDiffBytes = 2 << 10
)

type fileStat struct {
	mod  time.Time
	size int64
}

// ScanFiles fingerprints the watched files and records the ones that changed, appeared (in a watched
// folder) or were removed. The first scan of a file only remembers it.
func (r *Recorder) ScanFiles(now time.Time) {
	var patterns []string
	if r.o.Files != nil {
		patterns = r.o.Files()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for f := range r.compose {
		patterns = append(patterns, f)
	}
	paths, globbed := expand(patterns)
	watched := make(map[string]bool, len(paths))
	for _, p := range paths {
		watched[p] = true
	}
	seeded := len(r.st.Files) > 0
	present := map[string]bool{}
	var blocked []string // compose files the service cannot read
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err == nil && fi.Mode().IsRegular() {
			present[p] = true
		}
		if errors.Is(err, fs.ErrPermission) && r.compose[p] {
			blocked = append(blocked, p)
		}
		if !present[p] {
			continue
		}
		st := fileStat{mod: fi.ModTime(), size: fi.Size()}
		old, known := r.st.Files[p]
		if known && r.stats[p] == st {
			continue
		}
		sum, content, err := fingerprint(p, fi.Size())
		if err != nil {
			// The common files are skipped quietly when the service may not read them (some are
			// readable only by their own service on many systems); a compose file is reported.
			if errors.Is(err, fs.ErrPermission) && r.compose[p] {
				blocked = append(blocked, p)
			}
			continue
		}
		r.stats[p] = st
		if known && old == sum {
			continue
		}
		r.st.Files[p] = sum
		r.dirty = true
		switch {
		case !known && seeded && globbed[p]:
			r.emit(r.fileChange(now, p, "added", "", content))
		case known:
			r.emit(r.fileChange(now, p, "changed", r.readCopy(p), content))
		}
		r.writeCopy(p, content)
	}
	for p := range r.st.Files {
		if present[p] {
			continue
		}
		_, err := os.Stat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			r.emit(r.fileChange(now, p, "removed", "", nil))
		case watched[p]:
			continue // still watched, only unreadable for now: keep what is known of it
		}
		// Removed, or no longer watched (a compose project that is gone): forget it.
		delete(r.st.Files, p)
		delete(r.stats, p)
		_ = os.Remove(r.copyPath(p))
		r.dirty = true
	}
	if len(blocked) == 0 {
		r.clearProblemLocked(fileSource)
	} else {
		r.setProblemLocked(fileSource, blockedProblem(blocked))
	}
}

// fileSource names the file fingerprints among the sources, in changes and in problems.
const fileSource = "file"

// blockedProblem says which compose files cannot be read and which folder stops the service.
func blockedProblem(paths []string) string {
	sort.Strings(paths)
	msg := fmt.Sprintf("%d compose file(s) cannot be read by the service, so their changes are not recorded: %s",
		len(paths), paths[0])
	if len(paths) > 1 {
		msg += fmt.Sprintf(" and %d more", len(paths)-1)
	}
	if dir := closedFolder(paths[0]); dir != "" {
		msg += fmt.Sprintf(". The service cannot enter %s; to let it pass through without listing it: sudo setfacl -m u:%s:x %s",
			dir, host.ServiceName, dir)
	}
	return msg
}

// closedFolder is the deepest folder above path that the service can see but not enter, if any.
func closedFolder(path string) string {
	var chain []string
	for d := filepath.Dir(path); ; d = filepath.Dir(d) {
		chain = append(chain, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	for i := len(chain) - 1; i > 0; i-- {
		if _, err := os.Stat(chain[i-1]); errors.Is(err, fs.ErrPermission) {
			return chain[i]
		}
	}
	return ""
}

func (r *Recorder) fileChange(now time.Time, path, verb, before string, after []byte) host.Change {
	c := host.Change{At: now, Kind: host.ChangeFile, What: path + " " + verb, Source: fileSource}
	var detail []string
	if s, ok := r.likelyCause(now, path); ok {
		c.Who = s.Who
		detail = append(detail, "Probably by: sudo "+s.What)
	}
	switch {
	case secretLooking(path):
		if verb == "changed" {
			detail = append(detail, "Contents not shown: the file may hold secrets.")
		}
	case verb == "changed" && after != nil:
		if d := lineDiff(before, maskConfig(string(after))); d != "" {
			detail = append(detail, d)
		} else {
			detail = append(detail, "Only hidden values (passwords, keys) or whitespace changed.")
		}
	}
	c.Detail = strings.Join(detail, "\n")
	return c
}

// likelyCause is the latest sudo command before now that names the file, by its full path or by
// its name in the folder the command ran in.
func (r *Recorder) likelyCause(now time.Time, path string) (host.Change, bool) {
	dir, base := filepath.Dir(path), filepath.Base(path)
	for i := len(r.sudo) - 1; i >= 0; i-- {
		s := r.sudo[i]
		if s.At.After(now) || now.Sub(s.At) > attributeWithin {
			continue
		}
		if strings.Contains(s.What, path) || (strings.Contains(s.What, base) && strings.Contains(s.Detail, "in "+dir)) {
			return s, true
		}
	}
	return host.Change{}, false
}

// expand turns patterns into paths, at most maxWatched, and marks the ones that came from a pattern
// with *: a new file in a watched folder is a change, a named file seen the first time is not.
func expand(patterns []string) ([]string, map[string]bool) {
	seen := map[string]bool{}
	globbed := map[string]bool{}
	var out []string
	add := func(p string, glob bool) {
		if len(out) < maxWatched && !seen[p] {
			seen[p] = true
			globbed[p] = glob
			out = append(out, p)
		}
	}
	sort.Strings(patterns)
	for _, pat := range patterns {
		if !strings.ContainsAny(pat, "*?[") {
			add(filepath.Clean(pat), false)
			continue
		}
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			add(m, true)
		}
	}
	return out, globbed
}

// fingerprint hashes the file and returns its contents when it is small enough to copy.
func fingerprint(path string, size int64) (string, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	h := sha256.New()
	if size > maxCopyBytes {
		if _, err := io.Copy(h, f); err != nil {
			return "", nil, err
		}
		return hex.EncodeToString(h.Sum(nil)), nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(f, maxCopyBytes+1))
	if err != nil {
		return "", nil, err
	}
	h.Write(b)
	if len(b) > maxCopyBytes {
		b = nil
	}
	return hex.EncodeToString(h.Sum(nil)), b, nil
}

// The copy kept for the next diff is masked: the data folder never holds a password from a watched
// file. Files that look like they hold secrets are not copied at all.
func (r *Recorder) copyPath(path string) string {
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(r.dir, copiesDir, hex.EncodeToString(sum[:12])+".txt")
}

func (r *Recorder) writeCopy(path string, content []byte) {
	if content == nil || secretLooking(path) {
		_ = os.Remove(r.copyPath(path))
		return
	}
	_ = os.WriteFile(r.copyPath(path), []byte(maskConfig(string(content))), filePerm)
}

func (r *Recorder) readCopy(path string) string {
	b, _ := os.ReadFile(r.copyPath(path))
	return string(b)
}

var secretName = regexp.MustCompile(`(?i)(^\.env($|\.)|\.env$|\.(key|pem|p12|pfx|jks|keystore)$|secret|passw|credential|token|^id_(rsa|dsa|ecdsa|ed25519)|^shadow-?$|^gshadow-?$)`)

// secretLooking is true for files whose name says they hold secrets.
func secretLooking(path string) bool { return secretName.MatchString(filepath.Base(path)) }

var configSecret = regexp.MustCompile(`(?i)^(\s*-?\s*["']?[\w.-]*(?:password|passwd|secret|token|api[_-]?key|apikey|private[_-]?key|access[_-]?key|auth[_-]?key|credentials?)[\w.-]*["']?\s*[:=]\s*)(\S.*)$`)

// maskConfig hides values whose key names a secret ("DB_PASSWORD=...", "password: ...") and
// secrets inside command lines and URLs, line by line.
func maskConfig(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		l = configSecret.ReplaceAllString(l, "${1}****")
		lines[i] = host.MaskSecrets(l)
	}
	return strings.Join(lines, "\n")
}

// lineDiff lists the lines removed ("- ") and added ("+ "), in order, up to maxDiffLines. It
// compares lines as sets, which is enough to show what was edited in a configuration file.
func lineDiff(before, after string) string {
	count := func(s string) map[string]int {
		m := map[string]int{}
		for _, l := range strings.Split(s, "\n") {
			if strings.TrimSpace(l) != "" {
				m[strings.TrimRight(l, " \t\r")]++
			}
		}
		return m
	}
	old, cur := count(before), count(after)
	var out []string
	pick := func(text string, other map[string]int, mine map[string]int, sign string) {
		left := map[string]int{}
		for l, n := range mine {
			left[l] = n - other[l]
		}
		for _, l := range strings.Split(text, "\n") {
			l = strings.TrimRight(l, " \t\r")
			if left[l] > 0 {
				left[l]--
				out = append(out, sign+strings.TrimSpace(l))
			}
		}
	}
	pick(before, cur, old, "- ")
	pick(after, old, cur, "+ ")
	total := len(out)
	if total > maxDiffLines {
		out = out[:maxDiffLines]
	}
	d := strings.Join(out, "\n")
	if len(d) > maxDiffBytes {
		d = d[:maxDiffBytes]
	}
	if total > len(out) {
		d += "\n(" + strconv.Itoa(total-len(out)) + " more lines)"
	}
	return d
}
