package mailmanager

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

func TestMailTransportGenerationReplacesUnchangedCredentialsAndDrains(t *testing.T) {
	m := NewMailManager(func(models.JobEventType, string, any) error { return nil }, nil, nil, nil, nil, nil, nil)
	data := smtpPayload()
	data.TransportGeneration = uuid.New()
	at := time.Now().Add(-time.Hour)
	data.Sync.State.TransportGeneration = data.TransportGeneration
	data.Sync.State.LastSyncedAt = &at
	if err := m.AddWMail(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	old := m.Get(data.ID)
	done, ok := old.BeginExecution()
	if !ok {
		t.Fatal("old execution not admitted")
	}
	copied := *data
	copied.TransportGeneration = uuid.New()
	result := make(chan error, 1)
	go func() { result <- m.AddWMail(t.Context(), &copied) }()
	select {
	case <-old.Ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("generation did not replace unchanged credentials")
	}
	if m.Get(data.ID) != old || old.TransportGeneration != data.TransportGeneration {
		t.Fatal("old operation relabelled or not drained")
	}
	done()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement blocked")
	}
	next := m.Get(data.ID)
	if next == old || next.TransportGeneration != copied.TransportGeneration {
		t.Fatal("fresh incarnation missing")
	}
	if data.TransportGeneration == copied.TransportGeneration {
		t.Fatal("source snapshot mutated")
	}
	m.Terminate(data.ID)
}
