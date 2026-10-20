// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package windows

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

const pulseSID = "S-1-5-80-1111111111-2222222222-3333333333-4444444444-5555555555"

// A typical service's permissions, as Windows writes them.
const serviceSDDL = "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)"

func TestHealACEIsAddedOnceAndRemovedCleanly(t *testing.T) {
	with := withHealACE(serviceSDDL, pulseSID, true)
	if !strings.HasPrefix(with, serviceSDDL) || !strings.HasSuffix(with, "(A;;LCRPWP;;;"+pulseSID+")") {
		t.Fatalf("with = %s", with)
	}
	if again := withHealACE(with, pulseSID, true); again != with {
		t.Fatalf("adding twice changed it: %s", again)
	}
	if without := withHealACE(with, pulseSID, false); without != serviceSDDL {
		t.Fatalf("without = %s, want the original back", without)
	}
	// Only the restart rights, never configuration or ownership.
	if strings.Contains(strings.TrimPrefix(with, serviceSDDL), "DC") || strings.Contains(strings.TrimPrefix(with, serviceSDDL), "WD") {
		t.Fatalf("pulse got more than start, stop and query: %s", with)
	}
}

func TestHealACEGoesIntoTheDACLNotTheSACL(t *testing.T) {
	with := withHealACE(serviceSDDL+"S:(AU;FA;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;WD)", pulseSID, true)
	d, s, _ := strings.Cut(with, "S:")
	if !strings.Contains(d, pulseSID) || strings.Contains(s, pulseSID) {
		t.Fatalf("with = %s", with)
	}
	if _, err := windows.SecurityDescriptorFromString(with); err != nil {
		t.Fatalf("not valid SDDL: %v", err)
	}
}

// Only the service account's own entries are touched, not another account's.
func TestHealACELeavesOtherAccountsAlone(t *testing.T) {
	other := serviceSDDL + "(A;;LCRPWP;;;S-1-5-80-9)"
	if got := withHealACE(other, pulseSID, false); got != other {
		t.Fatalf("got %s", got)
	}
}

// The status check, as the service account runs it, asks for query rights only,
// so it works without being an administrator.
func TestServiceStatusWorksWithoutAdministratorRights(t *testing.T) {
	s, err := (Platform{}).Service(context.Background(), "EventLog")
	if err != nil {
		t.Fatalf("query EventLog: %v", err)
	}
	if !s.Active {
		t.Fatalf("EventLog = %+v, want running", s)
	}
	if _, err := (Platform{}).Service(context.Background(), "no-such-service-pulse"); !errors.Is(err, host.ErrNotFound) {
		t.Fatalf("missing service: %v", err)
	}
}

func TestSetHealRightsNeedsAdministrator(t *testing.T) {
	if windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("running as Administrator")
	}
	if _, err := (Platform{}).SetHealRights(context.Background(), []string{"Spooler"}); !errors.Is(err, host.ErrNotAdmin) {
		t.Fatalf("err = %v", err)
	}
}

// Without the right, a restart says what to do instead of a raw access-denied.
func TestRestartWithoutTheRightSaysHowToGrantIt(t *testing.T) {
	if windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("an administrator may restart services")
	}
	err := (Platform{}).RestartService(context.Background(), "EventLog")
	if !errors.Is(err, host.ErrHealNotAllowed) || !strings.Contains(err.Error(), "pulse install as Administrator") {
		t.Fatalf("err = %v", err)
	}
}
