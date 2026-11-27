// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package host

import "testing"

func TestAccessGrant(t *testing.T) {
	cases := []struct {
		name                       string
		a                          Access
		isNew, groupExists, member bool
		want                       bool
	}{
		{"allow wins over no membership", AccessAllow, false, true, false, true},
		{"deny wins over membership", AccessDeny, false, true, true, false},
		{"deny on a new account", AccessDeny, true, true, false, false},
		{"keep gives a new account the right", AccessKeep, true, true, false, true},
		{"keep leaves a member in", AccessKeep, false, true, true, true},
		{"keep leaves a non-member out: someone took the right away", AccessKeep, false, true, false, false},
		{"keep wants a right whose group does not exist yet", AccessKeep, false, false, false, true},
	}
	for _, c := range cases {
		if got := c.a.Grant(c.isNew, c.groupExists, c.member); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}
