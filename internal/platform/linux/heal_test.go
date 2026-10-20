// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

package linux

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

func TestHealUnitsAreNamedAndChecked(t *testing.T) {
	units, skipped := healUnits([]string{"nginx", "php8.3-fpm", "nginx.service", "redis@main", "evil\");x(\"", "-x"})
	if strings.Join(units, " ") != "nginx.service php8.3-fpm redis@main.service" {
		t.Fatalf("units = %v", units)
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped = %v, want the injection attempt and the option-like name", skipped)
	}
}

// The rule allows only restart, only these units, only for the pulse account.
func TestPolkitRuleAllowsOnlyRestartOfTheListedUnits(t *testing.T) {
	rule := string(polkitRuleText([]string{"nginx.service", "php8.3-fpm"}))
	for _, want := range []string{
		`action.id == "org.freedesktop.systemd1.manage-units"`,
		`subject.user == "brynko-pulse"`,
		`var units = ["nginx.service","php8.3-fpm"];`,
		`action.lookup("verb") == "restart"`,
		`units.indexOf(action.lookup("unit")) >= 0`,
		"return polkit.Result.YES;",
	} {
		if !strings.Contains(rule, want) {
			t.Errorf("rule lacks %q:\n%s", want, rule)
		}
	}
	if strings.Contains(rule, "Result.YES;\n    }\n    return") || strings.Count(rule, "polkit.Result") != 1 {
		t.Errorf("the rule may only ever say YES for the listed restart:\n%s", rule)
	}
}

func TestSetHealRightsNeedsRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	if _, err := (Platform{}).SetHealRights(context.Background(), []string{"nginx"}); !errors.Is(err, host.ErrNotAdmin) {
		t.Fatalf("err = %v, want ErrNotAdmin", err)
	}
}

func TestRestartServiceRefusesOddNames(t *testing.T) {
	if err := (Platform{}).RestartService(context.Background(), "-nginx"); err == nil || !strings.Contains(err.Error(), "invalid service name") {
		t.Fatalf("err = %v", err)
	}
}
