package wmail

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

func TestMailTransportGenerationRequiresFreshCompletedSyncPass(t *testing.T) {
	old, current := uuid.New(), uuid.New()
	prior := time.Now().Add(-time.Hour)
	seed := &models.SyncState{TransportGeneration: old, LastSyncedAt: &prior, BackfillStatus: models.SyncBackfillComplete}
	var events []models.SyncState
	tracker := newSyncTracker(seed, func(state models.SyncState) error { events = append(events, state); return nil })
	tracker.transportGeneration = current
	tracker.mark()
	tracker.flush(time.Now())
	if len(events) != 1 || events[0].TransportGeneration != uuid.Nil || !events[0].LastSyncedAt.Equal(prior) {
		t.Fatal("seed/progress re-badged as current success")
	}
	mail := &WMail{TransportGeneration: current, tracker: tracker}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	mail.endTick(cancelled, &tickStats{}, true)
	mail.endTick(t.Context(), &tickStats{aborted: true}, true)
	mail.endTick(t.Context(), &tickStats{deferred: 1}, true)
	if tracker.state.TransportGeneration != uuid.Nil {
		t.Fatal("cancelled/partial/deferred pass created proof")
	}
	mail.endTick(t.Context(), &tickStats{}, true)
	if tracker.state.TransportGeneration != current || !tracker.state.LastSyncedAt.After(prior) {
		t.Fatal("completed fresh pass lacks proof")
	}
	data := &models.AddWorkerEmail{TransportGeneration: uuid.New()}
	priorMail := &WMail{tracker: tracker}
	priorMail.ResumeInto(data)
	if data.TransportGeneration == current || data.Sync.State.TransportGeneration != uuid.Nil {
		t.Fatal("resume copied obsolete proof or overwrote fresh mailbox generation")
	}
	replacement := newSyncTracker(data.Sync.State, func(models.SyncState) error { return nil })
	next := &WMail{tracker: replacement}
	next.PreservePending(priorMail)
	replacement.flush(time.Now().Add(syncStateHeartbeat))
	if replacement.state.TransportGeneration != uuid.Nil {
		t.Fatal("pending heartbeat carried obsolete proof")
	}
}
