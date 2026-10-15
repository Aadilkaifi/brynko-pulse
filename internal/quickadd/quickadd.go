// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package quickadd turns a one-line request, such as "disk /data above 85",
// into a monitor file. It only builds the file; config.Apply validates and writes it.
package quickadd

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Spec is what the user asked for. Empty fields mean "use the default".
type Spec struct {
	Type   string
	Target string   // path, name, port, URL or host, depending on Type
	Run    []string // command monitors: the program and its arguments

	ID, Name, Severity string
	Notify             []string
	For, Interval      string
	Above              *float64 // percent, or load per CPU
	When, ClearWhen    string   // full conditions, for anything --above cannot say
	Days               int      // ssl: alert when fewer days are left
	Status             int      // http: expected status code
	Every, Grace       string   // heartbeat
}

// Result is the monitor file to write.
type Result struct {
	ID   string
	Path string // relative to the config folder
	YAML []byte
}

// Default thresholds for --above, and the margin below it that counts as recovered.
var defaultAbove = map[string]float64{"disk": 85, "memory": 90, "cpu": 95, "swap": 80, "load": 2}

const clearMargin = 5

// Types lists what quickadd can build, in help order.
var Types = []string{"disk", "memory", "cpu", "swap", "load", "service", "process", "container",
	"port", "http", "ssl", "file", "heartbeat", "command"}

var invalidID = regexp.MustCompile(`[^a-z0-9_-]+`)

// Build checks the spec and returns the monitor file.
func Build(s Spec) (Result, error) {
	m := &node{}
	m.add("id", "") // filled in below, kept first for readability
	m.add("type", s.Type)
	if s.Name != "" {
		m.add("name", s.Name)
	}

	if !slices.Contains(Types, s.Type) {
		return Result{}, fmt.Errorf("pulse add cannot create %q monitors. It can create: %s", s.Type, strings.Join(Types, ", "))
	}
	needsTarget := map[string]string{"disk": "a path", "service": "a service name", "process": "a process name",
		"container": "a container name", "port": "a port, or host:port", "http": "a URL",
		"ssl": "a host name, or host:port", "file": "a path"}
	if what, ok := needsTarget[s.Type]; ok && s.Target == "" {
		return Result{}, fmt.Errorf("pulse add %s needs %s", s.Type, what)
	}
	if _, ok := needsTarget[s.Type]; !ok && s.Target != "" {
		return Result{}, fmt.Errorf("pulse add %s takes no target, got %q", s.Type, s.Target)
	}
	if s.Above != nil && defaultAbove[s.Type] == 0 {
		return Result{}, fmt.Errorf("--above is for disk, memory, cpu, swap and load; use --when for %s", s.Type)
	}

	idHint := s.Type
	switch s.Type {
	case "disk":
		m.add("path", s.Target)
		idHint = "disk-" + pathHint(s.Target)
		threshold(m, s, "used_percent")
	case "memory", "cpu", "swap":
		threshold(m, s, "used_percent")
	case "load":
		threshold(m, s, "per_cpu")
	case "service":
		m.add("service", s.Target)
		idHint = "service-" + s.Target
	case "process":
		m.add("match", s.Target)
		idHint = "process-" + s.Target
	case "container":
		m.add("container", s.Target)
		idHint = "container-" + s.Target
	case "port":
		host, port, err := splitPort(s.Target)
		if err != nil {
			return Result{}, err
		}
		if host != "" {
			m.add("host", host)
		}
		m.addInt("port", port)
		idHint = "port-" + strconv.Itoa(port)
	case "http":
		u, err := url.Parse(s.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Result{}, fmt.Errorf("%q is not a web address; it must start with http:// or https://", s.Target)
		}
		m.add("url", s.Target)
		if s.Status != 0 {
			m.addInt("expect_status", s.Status)
		}
		idHint = "http-" + u.Hostname() + "-" + pathHint(u.Path) // never the query, which may hold a token
	case "ssl":
		m.add("host", s.Target)
		if s.Days > 0 {
			m.add("alert_when", fmt.Sprintf("days_left < %d", s.Days))
		}
		host, _, _ := strings.Cut(s.Target, ":")
		idHint = "ssl-" + host
	case "file":
		m.add("path", s.Target)
		idHint = "file-" + pathHint(s.Target)
		if s.When == "" {
			return Result{}, errors.New(`pulse add file needs --when, for example --when "age > 1d"`)
		}
	case "heartbeat":
		if s.ID == "" || s.Every == "" {
			return Result{}, errors.New("pulse add heartbeat needs --id (the name jobs ping) and --every, for example --every 1d")
		}
		m.add("expect_every", s.Every)
		if s.Grace != "" {
			m.add("grace", s.Grace)
		}
	case "command":
		if len(s.Run) == 0 {
			return Result{}, errors.New("pulse add command needs the program after --, for example: pulse add command --id backup-ok -- /usr/local/bin/check-backup")
		}
		m.addList("run", s.Run)
		idHint = "command-" + pathHint(s.Run[0])
		if s.When == "" {
			m.add("alert_when", "exit_code != 0")
		}
	default:
		return Result{}, fmt.Errorf("pulse add cannot create %q monitors. It can create: %s", s.Type, strings.Join(Types, ", "))
	}
	if s.Type != "command" && len(s.Run) > 0 {
		return Result{}, fmt.Errorf("arguments after -- are only for command monitors")
	}
	if s.When != "" {
		m.set("alert_when", s.When)
	}
	if s.ClearWhen != "" {
		m.set("clear_when", s.ClearWhen)
	}
	for _, kv := range [][2]string{{"for", s.For}, {"interval", s.Interval}, {"severity", s.Severity}} {
		if kv[1] != "" {
			m.add(kv[0], kv[1])
		}
	}
	if len(s.Notify) > 0 {
		m.addList("notify", s.Notify)
	}

	id := s.ID
	if id == "" {
		id = makeID(idHint)
	}
	m.set("id", id)

	data, err := render(m, time.Now())
	if err != nil {
		return Result{}, err
	}
	return Result{ID: id, Path: "monitors/" + id + ".yaml", YAML: data}, nil
}

// threshold writes alert_when and a recovery margin from --above or the default.
func threshold(m *node, s Spec, metric string) {
	if s.When != "" {
		return
	}
	above := defaultAbove[s.Type]
	if s.Above != nil {
		above = *s.Above
	}
	m.add("alert_when", fmt.Sprintf("%s > %s", metric, num(above)))
	if s.ClearWhen == "" && metric == "used_percent" && above > clearMargin {
		m.add("clear_when", fmt.Sprintf("%s < %s", metric, num(above-clearMargin)))
	}
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func splitPort(target string) (string, int, error) {
	host, portText := "", target
	if strings.Contains(target, ":") {
		h, p, err := net.SplitHostPort(target)
		if err != nil {
			return "", 0, fmt.Errorf("%q is not a port or host:port", target)
		}
		host, portText = h, p
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("%q is not a port number between 1 and 65535", portText)
	}
	return host, port, nil
}

func pathHint(p string) string {
	p = strings.Trim(strings.ReplaceAll(p, `\`, "/"), "/")
	if p == "" || p == ":" || strings.HasSuffix(p, ":") {
		return "root"
	}
	return p
}

func makeID(hint string) string {
	id := strings.Trim(invalidID.ReplaceAllString(strings.ToLower(hint), "-"), "-_")
	if len(id) > 63 {
		id = strings.Trim(id[:63], "-_")
	}
	if id == "" {
		id = "monitor"
	}
	return id
}

// node is a YAML mapping that keeps keys in the order they were added.
type node struct{ keys, vals []*yaml.Node }

func (n *node) put(k string, v *yaml.Node, replace bool) {
	for i, key := range n.keys {
		if key.Value == k {
			if replace {
				n.vals[i] = v
			}
			return
		}
	}
	n.keys = append(n.keys, &yaml.Node{Kind: yaml.ScalarNode, Value: k})
	n.vals = append(n.vals, v)
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func (n *node) add(k, v string) { n.put(k, str(v), false) }
func (n *node) set(k, v string) { n.put(k, str(v), true) }
func (n *node) addInt(k string, v int) {
	n.put(k, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)}, false)
}

func (n *node) addList(k string, items []string) {
	l := &yaml.Node{Kind: yaml.SequenceNode}
	if k == "notify" {
		l.Style = yaml.FlowStyle
	}
	for _, it := range items {
		l.Content = append(l.Content, str(it))
	}
	n.put(k, l, false)
}

func render(m *node, now time.Time) ([]byte, error) {
	item := &yaml.Node{Kind: yaml.MappingNode}
	for i := range m.keys {
		item.Content = append(item.Content, m.keys[i], m.vals[i])
	}
	doc := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "monitors"},
		{Kind: yaml.SequenceNode, Content: []*yaml.Node{item}},
	}}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Added by pulse add on %s. Edit it freely; delete this file to remove the monitor.\n",
		now.Format("2006-01-02"))
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
