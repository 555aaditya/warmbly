package models

import (
	"testing"
	"time"
)

func TestManualSendAllowanceIsGenerousButFair(t *testing.T) {
	month := 30 * 24 * time.Hour
	for _, tc := range []struct {
		name string
		in   ManualSendStanding
		want ManualSendAllowance
	}{
		{"workspace mailbox in good standing", ManualSendStanding{Provider: InboxProviderGoogle, Address: "a@acme.com", ConnectedFor: month},
			ManualSendAllowance{Daily: 1500, Hourly: 300, NewConversations: 375, PerMessage: 100}},
		{"consumer gmail stays under Gmail's own cap", ManualSendStanding{Provider: InboxProviderGoogle, Address: "a@Gmail.com", ConnectedFor: month},
			ManualSendAllowance{Daily: 400, Hourly: 80, NewConversations: 100, PerMessage: 80}},
		{"consumer outlook", ManualSendStanding{Provider: InboxProviderOutlook, Address: "a@hotmail.com", ConnectedFor: month},
			ManualSendAllowance{Daily: 250, Hourly: 50, NewConversations: 62, PerMessage: 50}},
		{"microsoft 365", ManualSendStanding{Provider: InboxProviderOutlook, Address: "a@acme.com", ConnectedFor: month},
			ManualSendAllowance{Daily: 1500, Hourly: 300, NewConversations: 375, PerMessage: 100}},
		{"smtp host", ManualSendStanding{Provider: InboxProviderSMTPIMAP, Address: "a@acme.com", ConnectedFor: month},
			ManualSendAllowance{Daily: 750, Hourly: 150, NewConversations: 187, PerMessage: 100}},
		{"connected this week", ManualSendStanding{Provider: InboxProviderGoogle, Address: "a@acme.com", ConnectedFor: 5 * 24 * time.Hour},
			ManualSendAllowance{Daily: 750, Hourly: 150, NewConversations: 187, PerMessage: 100}},
		{"throttled in warmup", ManualSendStanding{Provider: InboxProviderGoogle, Address: "a@acme.com", ConnectedFor: month, Health: "throttled"},
			ManualSendAllowance{Daily: 750, Hourly: 150, NewConversations: 187, PerMessage: 100}},
		{"restricted workspace", ManualSendStanding{Provider: InboxProviderGoogle, Address: "a@acme.com", ConnectedFor: month, OrgRisk: OrgRiskRestricted},
			ManualSendAllowance{Daily: 375, Hourly: 75, NewConversations: 93, PerMessage: 75}},
		{"everything against it still keeps conversations going", ManualSendStanding{Provider: InboxProviderOutlook, Address: "a@live.com", OrgRisk: OrgRiskRestricted, Health: "blocked"},
			ManualSendAllowance{Daily: 50, Hourly: 20, NewConversations: 25, PerMessage: 20}},
		{"suspended workspace", ManualSendStanding{Provider: InboxProviderGoogle, Address: "a@acme.com", ConnectedFor: month, OrgRisk: OrgRiskSuspended},
			ManualSendAllowance{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ManualSendAllowanceFor(tc.in); got != tc.want {
				t.Errorf("allowance = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestManualSendExceededChargesNewConversationsOnly(t *testing.T) {
	a := ManualSendAllowance{Daily: 100, Hourly: 30, NewConversations: 20, PerMessage: 10}
	for _, tc := range []struct {
		name   string
		used   ManualSendUsage
		n      int
		starts bool
		want   ManualSendWindow
	}{
		{"within every bound", ManualSendUsage{Day: 50, Hour: 10, NewConversations: 10}, 5, true, ""},
		{"too many on one message", ManualSendUsage{}, 11, false, ManualSendWindowMessage},
		{"burst", ManualSendUsage{Day: 40, Hour: 25}, 6, false, ManualSendWindowHour},
		{"day", ManualSendUsage{Day: 95, Hour: 0}, 6, false, ManualSendWindowDay},
		{"new conversations spent", ManualSendUsage{Day: 20, NewConversations: 18}, 3, true, ManualSendWindowNew},
		{"a reply ignores the new-conversation bound", ManualSendUsage{Day: 20, NewConversations: 20}, 3, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			window, _, over := a.Exceeded(tc.used, tc.n, tc.starts)
			if window != tc.want || over != (tc.want != "") {
				t.Errorf("Exceeded = %q (%v), want %q", window, over, tc.want)
			}
		})
	}
}
