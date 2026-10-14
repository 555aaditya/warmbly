package scheduler

import (
	"testing"
	"time"
)

func TestCampaignPassLocationMatchesUncachedWindows(t *testing.T) {
	pass := &campaignPass{}
	zones := []string{"Europe/Budapest", "America/New_York", "Asia/Kolkata", "", "UTC", "Local", "not/a/timezone"}
	for _, zone := range zones {
		want := loadLocation(zone)
		got := pass.location(zone)
		if got != pass.location(zone) {
			t.Fatalf("timezone %q was not reused", zone)
		}
		for _, at := range []time.Time{
			time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC),
			time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC),
			time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC),
			time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC),
		} {
			gName, gOffset := at.In(got).Zone()
			wName, wOffset := at.In(want).Zone()
			if gName != wName || gOffset != wOffset || !businessHoursReopen(at, got).Equal(businessHoursReopen(at, want)) {
				t.Fatalf("timezone %q changed window at %s", zone, at)
			}
		}
	}
	if len(pass.locations) != len(zones) {
		t.Fatalf("loaded %d locations for %d zones", len(pass.locations), len(zones))
	}
	fresh := &campaignPass{}
	if fresh.location(zones[0]) == pass.location(zones[0]) {
		t.Fatal("locations must not be cached across scheduling passes")
	}
}

func BenchmarkCampaignPassLocations(b *testing.B) {
	zones := []string{"Europe/Budapest", "America/New_York", "Asia/Kolkata", "Europe/London"}
	for _, mode := range []string{"uncached", "per_pass"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				pass := &campaignPass{}
				for range 32 {
					for mailbox := range 100 {
						zone := zones[mailbox%len(zones)]
						var loc *time.Location
						if mode == "uncached" {
							loc = loadLocation(zone)
						} else {
							loc = pass.location(zone)
						}
						if loc == nil {
							b.Fatal("missing location")
						}
					}
				}
			}
		})
	}
}
