// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package rule parses and evaluates one-comparison conditions such as
// "used_percent > 85" or `state != "running"`.
package rule

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Aadilkaifi/brynko-pulse/internal/duration"
)

// Values are the measurements of one check: numbers (float64) or text (string).
type Values map[string]any

// Condition is a parsed comparison.
type Condition struct {
	Metric string
	Op     string
	Num    float64
	Text   string
	IsText bool
	Raw    string
}

var (
	condRe     = regexp.MustCompile(`^\s*([a-z_][a-z0-9_]*)\s*(>=|<=|==|!=|>|<)\s*(.+?)\s*$`)
	durationRe = regexp.MustCompile(`^([0-9]+(ms|s|m|h|d))+$`)
	wordRe     = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

// Parse reads a condition. Durations become seconds, so "age > 26h" compares seconds.
func Parse(s string) (Condition, error) {
	m := condRe.FindStringSubmatch(s)
	if m == nil {
		return Condition{}, fmt.Errorf("%q is not a comparison like used_percent > 85", s)
	}
	c := Condition{Metric: m[1], Op: m[2], Raw: strings.TrimSpace(s)}
	val := m[3]
	switch {
	case strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) && len(val) >= 2:
		c.Text, c.IsText = val[1:len(val)-1], true
	case durationRe.MatchString(val):
		d, err := duration.Parse(val)
		if err != nil {
			return Condition{}, err
		}
		c.Num = d.Seconds()
	case wordRe.MatchString(val):
		c.Text, c.IsText = val, true
	default:
		n, err := strconv.ParseFloat(val, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return Condition{}, fmt.Errorf("%q in %q is not a number, duration or quoted text", val, s)
		}
		c.Num = n
	}
	if c.IsText && c.Op != "==" && c.Op != "!=" {
		return Condition{}, fmt.Errorf("%q compares text with %s; text only works with == and !=", s, c.Op)
	}
	return c, nil
}

// Eval reports whether the condition holds for v.
func (c Condition) Eval(v Values) (bool, error) {
	got, ok := v[c.Metric]
	if !ok {
		return false, fmt.Errorf("%s is not measured by this monitor (available: %s)", c.Metric, strings.Join(Names(v), ", "))
	}
	if c.IsText {
		s := fmt.Sprint(got)
		if c.Op == "==" {
			return s == c.Text, nil
		}
		return s != c.Text, nil
	}
	n, ok := toFloat(got)
	if !ok {
		return false, fmt.Errorf("%s is text (%q), so it cannot be compared with a number", c.Metric, fmt.Sprint(got))
	}
	switch c.Op {
	case ">":
		return n > c.Num, nil
	case ">=":
		return n >= c.Num, nil
	case "<":
		return n < c.Num, nil
	case "<=":
		return n <= c.Num, nil
	case "==":
		return n == c.Num, nil
	default:
		return n != c.Num, nil
	}
}

// Describe returns "metric is value", for alert messages.
func (c Condition) Describe(v Values) string {
	return c.Metric + " is " + Format(v[c.Metric])
}

// Format renders a value for people: numbers without noise, booleans as yes/no.
func Format(v any) string {
	if n, ok := toFloat(v); ok {
		if n == math.Trunc(n) && math.Abs(n) < 1e15 {
			return strconv.FormatInt(int64(n), 10)
		}
		return strconv.FormatFloat(n, 'f', 1, 64)
	}
	return fmt.Sprint(v)
}

// Names lists the measured metrics, sorted.
func Names(v Values) []string {
	names := make([]string, 0, len(v))
	for k := range v {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint64:
		return float64(t), true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}
