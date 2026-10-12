package models

import (
	"slices"
	"testing"
)

func TestWarmupEngagementRequiresReceivingParticipation(t *testing.T) {
	actions := []string{WarmupActionFile, WarmupActionRescueFromSpam, WarmupActionMarkRead, WarmupActionMarkImportant, WarmupActionStar, WarmupActionDelete, WarmupActionVerifyRemoval, "unknown"}
	cleanup := []string{WarmupActionDelete, WarmupActionVerifyRemoval}
	for _, tc := range []struct {
		name    string
		mode    TestParticipationMode
		send    bool
		receive bool
		allowed bool
	}{
		{"legacy NULL", "", false, false, true},
		{"explicit legacy", TestParticipationLegacy, false, false, true},
		{"send and receive", TestParticipationDiagnostic, true, true, true},
		{"receive only", TestParticipationDiagnostic, false, true, true},
		{"send only", TestParticipationDiagnostic, true, false, false},
		{"neither direction", TestParticipationDiagnostic, false, false, false},
		{"off with old flags", TestParticipationOff, true, true, false},
		{"unknown mode", "unknown", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Email{TestSendEnabled: tc.send, TestReceiveEnabled: tc.receive}
			if tc.mode != "" {
				e.TestMode = &tc.mode
			}
			want := cleanup
			if tc.allowed {
				want = actions[:len(actions)-1]
			}
			if got := e.PermittedWarmupActions(actions); !slices.Equal(got, want) || e.SyntheticActionsAllowed() != tc.allowed {
				t.Fatalf("actions=%v want=%v engagement=%v", got, want, e.SyntheticActionsAllowed())
			}
		})
	}
}
