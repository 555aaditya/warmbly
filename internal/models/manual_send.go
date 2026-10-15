package models

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Manual (unibox) sends sit outside the campaign and warmup allocations and
// are held to fair use instead: a generous daily recipient allowance sized to
// what the mailbox's provider accepts, lowered by what is known about the
// mailbox and its workspace, with a burst, a new-conversation and a
// per-message bound under it, and a workspace-wide bound from its plan. Replies and forwards inside a conversation only
// spend the daily and hourly allowance; new conversations are the cold-outreach
// path, so they also spend their own, smaller one.
const (
	// Daily recipient bases, each below the provider's own published cap.
	ManualSendDailyWorkspace   = 1500 // Google Workspace and Microsoft 365
	ManualSendDailyConsumerG   = 400  // gmail.com, googlemail.com (Gmail caps at 500)
	ManualSendDailyConsumerMS  = 250  // outlook.com, hotmail.com, live.com, msn.com (300)
	ManualSendDailySMTP        = 750  // any other host; its own limits are unknown
	ManualSendDailyFloor       = 50   // a conversation never stalls below this
	ManualSendPerMessageMax    = 100  // more on one message is a list, not a conversation
	ManualSendHourlyFloor      = 20
	ManualSendNewConvFloor     = 25
	manualSendHourlyDivisor    = 5 // a fifth of the day in any hour
	manualSendNewConvDivisor   = 4 // a quarter of the day to new conversations
	manualSendYoungMailboxDays = 14
	manualSendNewMailboxDays   = 2

	// The workspace's plan bounds its unibox sends across every mailbox:
	// twice the plan's daily email limit, never under the floor.
	ManualSendPlanMultiple   = 2
	ManualSendWorkspaceFloor = 100
)

var (
	manualSendConsumerGoogle    = map[string]bool{"gmail.com": true, "googlemail.com": true}
	manualSendConsumerMicrosoft = map[string]bool{"outlook.com": true, "hotmail.com": true, "live.com": true, "msn.com": true}
)

// ManualSendStanding is what the allowance is decided from.
type ManualSendStanding struct {
	Provider InboxProvider
	Address  string
	// ConnectedFor is how long the mailbox has been connected to Warmbly.
	ConnectedFor time.Duration
	OrgRisk      OrgRiskState
	// Health is the mailbox's worst warmup standing, pool row or address ledger.
	Health string
}

// ManualSendAllowance is one mailbox's fair-use bounds, in recipient
// occurrences (each To, Cc and Bcc address counts).
type ManualSendAllowance struct {
	Daily            int // rolling 24 hours
	Hourly           int // rolling hour
	NewConversations int // rolling 24 hours, sends that start a conversation
	PerMessage       int
}

// ManualSendAllowanceFor decides a mailbox's allowance. Every factor only
// lowers the provider base, and none takes it below the floors.
func ManualSendAllowanceFor(s ManualSendStanding) ManualSendAllowance {
	if s.OrgRisk.BlocksSending() {
		return ManualSendAllowance{}
	}
	factor := s.OrgRisk.CapMultiplier()
	switch s.Health {
	case "throttled":
		factor *= 0.5
	case "quarantined", "blocked":
		factor *= 0.25
	}
	switch days := s.ConnectedFor.Hours() / 24; {
	case days < manualSendNewMailboxDays:
		factor *= 0.25
	case days < manualSendYoungMailboxDays:
		factor *= 0.5
	}
	daily := max(ManualSendDailyFloor, int(math.Round(float64(manualSendDailyBase(s.Provider, s.Address))*factor)))
	hourly := max(ManualSendHourlyFloor, daily/manualSendHourlyDivisor)
	return ManualSendAllowance{
		Daily:            daily,
		Hourly:           hourly,
		NewConversations: max(ManualSendNewConvFloor, daily/manualSendNewConvDivisor),
		PerMessage:       min(ManualSendPerMessageMax, hourly),
	}
}

func manualSendDailyBase(provider InboxProvider, address string) int {
	domain := strings.ToLower(address)
	if at := strings.LastIndexByte(domain, '@'); at >= 0 {
		domain = domain[at+1:]
	}
	switch provider {
	case InboxProviderGoogle:
		if manualSendConsumerGoogle[domain] {
			return ManualSendDailyConsumerG
		}
		return ManualSendDailyWorkspace
	case InboxProviderOutlook:
		if manualSendConsumerMicrosoft[domain] {
			return ManualSendDailyConsumerMS
		}
		return ManualSendDailyWorkspace
	default:
		return ManualSendDailySMTP
	}
}

// ManualSendWorkspaceAllowance is a workspace's unibox recipients in 24 hours
// across all its mailboxes, from its plan's daily email limit. A negative
// limit is an unlimited plan (or a self-hosted instance) and returns 0, no bound.
func ManualSendWorkspaceAllowance(planDaily int) int {
	if planDaily < 0 {
		return 0
	}
	return max(ManualSendWorkspaceFloor, planDaily*ManualSendPlanMultiple)
}

// ManualSendWindow names the bound a manual send ran into.
type ManualSendWindow string

const (
	ManualSendWindowMessage ManualSendWindow = "message"
	ManualSendWindowHour    ManualSendWindow = "hour"
	ManualSendWindowDay     ManualSendWindow = "day"
	ManualSendWindowNew     ManualSendWindow = "new_conversations"
	// ManualSendWindowPlan is the workspace's plan allowance, across mailboxes.
	ManualSendWindowPlan ManualSendWindow = "plan"
)

// ManualSendUsage is what the mailbox has already sent manually, in recipient
// occurrences.
type ManualSendUsage struct {
	Day, Hour, NewConversations int
}

// Exceeded reports the first bound a send of n recipients would cross.
func (a ManualSendAllowance) Exceeded(used ManualSendUsage, n int, startsConversation bool) (ManualSendWindow, int, bool) {
	switch {
	case n > a.PerMessage:
		return ManualSendWindowMessage, a.PerMessage, true
	case used.Hour+n > a.Hourly:
		return ManualSendWindowHour, a.Hourly, true
	case used.Day+n > a.Daily:
		return ManualSendWindowDay, a.Daily, true
	case startsConversation && used.NewConversations+n > a.NewConversations:
		return ManualSendWindowNew, a.NewConversations, true
	}
	return "", 0, false
}

// ManualSendRefusal words a bound for the person whose send it stopped.
func ManualSendRefusal(window ManualSendWindow, limit int) string {
	switch window {
	case ManualSendWindowMessage:
		return fmt.Sprintf("one email can go to at most %d recipients from the unibox. Split the list or use a campaign.", limit)
	case ManualSendWindowHour:
		return fmt.Sprintf("the mailbox has reached its fair-use limit of %d recipients in an hour. Try again later or send from another mailbox.", limit)
	case ManualSendWindowNew:
		return fmt.Sprintf("the mailbox has started conversations with %d recipients in the last 24 hours, its fair-use limit for new emails. Replies still go out; use a campaign for outreach.", limit)
	case ManualSendWindowPlan:
		return fmt.Sprintf("your workspace has reached its plan's fair-use limit of %d unibox recipients in 24 hours across all mailboxes. Try again later or upgrade the plan for a higher limit.", limit)
	default:
		return fmt.Sprintf("the mailbox has reached its fair-use limit of %d recipients in 24 hours. Try again later or send from another mailbox.", limit)
	}
}
