// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package rule

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// Expect is a comparison with one fixed value, for a value that may be a number or
// text, such as the JSON field an http monitor reads (json_expect: "== ok",
// "> 0"). A number is compared as a number; anything else as text, where only
// == and != apply. Quotes around the value make it text ("== \"200\"").
type Expect struct {
	Op    string
	Text  string
	Num   float64
	IsNum bool
}

var expectRe = regexp.MustCompile(`^\s*(==|!=|>=|<=|>|<)\s*(.*?)\s*$`)

// ParseExpect reads "<comparison> <value>".
func ParseExpect(s string) (Expect, error) {
	m := expectRe.FindStringSubmatch(s)
	if m == nil || m[2] == "" {
		return Expect{}, errors.New(`write a comparison and a value, for example "== ok" or "> 0"`)
	}
	e := Expect{Op: m[1], Text: m[2]}
	if q := e.Text; len(q) >= 2 && q[0] == '"' && q[len(q)-1] == '"' {
		e.Text = q[1 : len(q)-1]
	} else if n, err := strconv.ParseFloat(e.Text, 64); err == nil {
		e.Num, e.IsNum = n, true
	}
	if !e.IsNum && e.Op != "==" && e.Op != "!=" {
		return Expect{}, errors.New("text can only be compared with == or !=; " + strconv.Quote(e.Text) + " is not a number")
	}
	return e, nil
}

// Met reports whether v satisfies the comparison. A number expected and text
// found (or the reverse) is a mismatch, so only != is met.
func (e Expect) Met(v any) bool {
	if e.IsNum {
		n, ok := v.(float64)
		if !ok {
			return e.Op == "!="
		}
		switch e.Op {
		case "==":
			return n == e.Num
		case "!=":
			return n != e.Num
		case ">":
			return n > e.Num
		case ">=":
			return n >= e.Num
		case "<":
			return n < e.Num
		default:
			return n <= e.Num
		}
	}
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	default:
		s = Format(v)
	}
	eq := strings.TrimSpace(s) == e.Text
	if e.Op == "==" {
		return eq
	}
	return !eq
}

// String writes the comparison back as people write it.
func (e Expect) String() string {
	if e.IsNum {
		return e.Op + " " + strconv.FormatFloat(e.Num, 'f', -1, 64)
	}
	return e.Op + " " + e.Text
}
