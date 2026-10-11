package arrivaldiag

import (
	"testing"
	"time"
)

func TestArrivalDiagnosticsFlushOnlyBufferedReturnedPasses(t *testing.T) {
	var w Window
	if w.Flush() != nil {
		t.Fatal("empty shutdown invented a pass")
	}
	now := time.Unix(100, 0)
	stats := Snapshot{}
	stats.stages[Claim] = counter{attempts: 3, duration: time.Millisecond}
	if w.Record(stats, time.Second, false, false, false, false, now) == nil {
		t.Fatal("first pass was not logged")
	}
	if w.Record(stats, 2*time.Second, true, false, false, false, now.Add(5*time.Second)) != nil {
		t.Fatal("cadence was not preserved")
	}
	flushed := w.Flush()
	if flushed == nil || flushed.passes != 1 || flushed.failed != 1 || flushed.cancelled != 0 || flushed.deadline != 0 || flushed.duration != 2*time.Second || flushed.stats.stages[Claim].attempts != 3 {
		t.Fatalf("flush changed recorded facts: %+v", flushed)
	}
	if w.Flush() != nil {
		t.Fatal("duplicate shutdown repeated prior observations")
	}
	if w.Record(stats, time.Second, false, false, false, false, now.Add(10*time.Second)) != nil {
		t.Fatal("flush changed thirty-second cadence")
	}
}
