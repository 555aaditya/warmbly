package models

import "testing"

func TestWarmupPartnerCandidateStarvation(t *testing.T) {
	cases := []struct {
		name           string
		sent, received int
		want           float64
	}{
		{"sends nothing, owes nothing", 0, 3, 0},
		{"heard back from nobody", 20, 0, 1},
		{"heard back from one in twenty", 20, 1, 0.95},
		{"in balance", 10, 10, 0},
		{"received more than it sent", 5, 12, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := WarmupPartnerCandidate{Sent7d: tc.sent, Received7d: tc.received}
			if got := c.Starvation(); got != tc.want {
				t.Fatalf("Starvation() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWarmupPoolReturnsToMirrorsTheBorrow(t *testing.T) {
	for _, pool := range []string{"free", "premium"} {
		borrowFrom, borrows := WarmupPoolBorrowsFrom(pool)
		returnTo, returns := WarmupPoolReturnsTo(pool)
		if borrows == returns {
			t.Fatalf("%s both borrows and returns, or neither", pool)
		}
		if borrows {
			if back, ok := WarmupPoolReturnsTo(borrowFrom); !ok || back != pool {
				t.Fatalf("%s borrows from %s, which returns to %q", pool, borrowFrom, back)
			}
		}
		if returns {
			if from, ok := WarmupPoolBorrowsFrom(returnTo); !ok || from != pool {
				t.Fatalf("%s returns to %s, which borrows from %q", pool, returnTo, from)
			}
		}
	}
}

// Only a small host's own filter is read, and only on a sample: at the major
// providers a junk verdict is the senders' reputation, not the recipient's.
func TestWarmupPartnerCandidateFilterJunkRate(t *testing.T) {
	cases := []struct {
		name string
		c    WarmupPartnerCandidate
		want float64
	}{
		{"small host junking half", WarmupPartnerCandidate{Provider: "smtp_imap", Received7d: 20, Junked7d: 10}, 0.5},
		{"below the sample", WarmupPartnerCandidate{Provider: "smtp_imap", Received7d: 9, Junked7d: 9}, 0},
		{"gmail is never read", WarmupPartnerCandidate{Provider: "gmail", Received7d: 20, Junked7d: 20}, 0},
		{"workspace on imap is never read", WarmupPartnerCandidate{Provider: "smtp_imap", MailHost: "google_workspace", Received7d: 20, Junked7d: 20}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.FilterJunkRate(); got != tc.want {
				t.Fatalf("FilterJunkRate() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A recipient is drawn by the chance its mail arrives, read only on a sample
// and never down to nothing, so a recovered inbox can show it.
func TestWarmupPartnerCandidateArrivalRate(t *testing.T) {
	cases := []struct {
		name               string
		delivered, arrived int
		want               float64
	}{
		{"nothing handed off", 0, 0, 1},
		{"below the sample", 4, 0, 1},
		{"everything arrived", 20, 20, 1},
		{"half arrived", 20, 10, 0.5},
		{"nothing arrived keeps the floor", 20, 0, warmupArrivalFloor},
		{"late arrivals never exceed one", 5, 6, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := WarmupPartnerCandidate{Delivered7d: tc.delivered, Arrived7d: tc.arrived}
			if got := c.ArrivalRate(); got != tc.want {
				t.Fatalf("ArrivalRate() = %v, want %v", got, tc.want)
			}
		})
	}
}
