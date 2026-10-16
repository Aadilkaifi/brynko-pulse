// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package monitor

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
)

// Describe words what a monitor measures right now, for status lists and alerts:
// the value its condition compares, in the catalog's words ("Used space: 91.2%",
// "Not running", "Not answering as expected (status 502)"). The numbers in --json
// are untouched; only the words for people come from here.
func Describe(typ string, c rule.Condition, v rule.Values) string {
	info, _ := Info(typ)
	out := DescribeValue(typ, c.Metric, v[c.Metric])
	vi, ok := valueInfo(info, c.Metric)
	if !ok || vi.Kind != "flag" {
		return out
	}
	var why []string
	if extra, has := v[info.Extra]; has && info.Extra != "" && info.Extra != c.Metric {
		why = append(why, fmt.Sprintf("%s %s", strings.ToLower(labelOf(info, info.Extra)), formatAs(info, info.Extra, extra)))
	}
	for _, r := range info.Because {
		b, has := v[r.Flag]
		if !has {
			continue
		}
		if r.Value != "" { // a value is worth showing either way: "JSON field is \"ok\""
			if val, ok := v[r.Value]; ok {
				why = append(why, r.Text+" "+strconv.Quote(rule.Format(val)))
			}
			continue
		}
		if !truthy(v[c.Metric]) && !truthy(b) {
			why = append(why, r.Text)
		}
	}
	if len(why) > 0 {
		out += " (" + strings.Join(why, ", ") + ")"
	}
	return out
}

// DescribeValue words one value: a flag as its state, anything else as "Label: value".
func DescribeValue(typ, name string, value any) string {
	info, _ := Info(typ)
	vi, ok := valueInfo(info, name)
	if !ok {
		return name + ": " + rule.Format(value)
	}
	if vi.Kind == "flag" {
		if truthy(value) {
			return vi.Label
		}
		return "Not " + strings.ToLower(vi.Label)
	}
	return vi.Label + ": " + formatAs(info, name, value)
}

// DescribeCondition words a condition: "used space is above 85%", "it is not running".
func DescribeCondition(typ string, c rule.Condition) string {
	info, _ := Info(typ)
	vi, ok := valueInfo(info, c.Metric)
	if !ok {
		return c.Raw
	}
	label := strings.ToLower(vi.Label)
	if vi.Kind == "flag" && !c.IsText && (c.Op == "==" || c.Op == "!=") {
		if (c.Num == 1) == (c.Op == "==") {
			return label
		}
		return "not " + label
	}
	var amount string
	switch {
	case c.IsText:
		amount = strconv.Quote(c.Text)
	case vi.Kind == "duration":
		amount = humanSeconds(c.Num)
	default:
		amount = withUnit(vi, c.Num)
	}
	return fmt.Sprintf("%s %s %s", label, opWords[c.Op], amount)
}

var opWords = map[string]string{">": "is above", ">=": "is at least", "<": "is below", "<=": "is at most", "==": "is", "!=": "is not"}

func valueInfo(info TypeInfo, name string) (ValueInfo, bool) {
	for _, v := range info.Values {
		if v.Name == name {
			return v, true
		}
	}
	return ValueInfo{}, false
}

func labelOf(info TypeInfo, name string) string {
	if v, ok := valueInfo(info, name); ok {
		return v.Label
	}
	return name
}

func formatAs(info TypeInfo, name string, value any) string {
	vi, _ := valueInfo(info, name)
	n, isNum := number(value)
	switch {
	case vi.Kind == "flag":
		if truthy(value) {
			return "yes"
		}
		return "no"
	case vi.Kind == "duration" && isNum:
		return humanSeconds(n)
	case isNum:
		return withUnit(vi, n)
	case value == nil:
		return "none"
	}
	return fmt.Sprint(value)
}

func withUnit(vi ValueInfo, n float64) string {
	switch vi.Unit {
	case "%":
		return trim(n) + "%"
	case "bytes":
		return humanBytes(n)
	case "":
		return trim(n)
	}
	return trim(n) + " " + vi.Unit
}

func trim(n float64) string {
	if n == math.Trunc(n) && math.Abs(n) < 1e15 {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'f', 1, 64)
}

func humanBytes(n float64) string {
	units := []string{"bytes", "KB", "MB", "GB", "TB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return trim(n) + " bytes"
	}
	return strconv.FormatFloat(n, 'f', 1, 64) + " " + units[i]
}

func humanSeconds(s float64) string {
	d := int64(s)
	switch {
	case d < 60:
		return fmt.Sprintf("%ds", d)
	case d < 3600:
		return fmt.Sprintf("%dm", d/60)
	case d < 86400:
		if m := d % 3600 / 60; m != 0 {
			return fmt.Sprintf("%dh %dm", d/3600, m)
		}
		return fmt.Sprintf("%dh", d/3600)
	}
	if h := d % 86400 / 3600; h != 0 {
		return fmt.Sprintf("%dd %dh", d/86400, h)
	}
	return fmt.Sprintf("%dd", d/86400)
}

func number(v any) (float64, bool) {
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
	}
	return 0, false
}

func truthy(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	n, ok := number(v)
	return ok && n != 0
}
