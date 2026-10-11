package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Live checks of the per-mailbox send claim and of what counts as a booked
// send (issue #942). Skipped unless WARMBLY_TEST_DB is set.

func TestLiveMailboxSendClaimAdmitsOnePerGap(t *testing.T) {
	_, pool := liveContactDB(t)
	requireSchemaVersion(t, pool, 289)
	ctx := context.Background()
	f := newWarmupUsageFixture(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tasks WHERE email_account_id = $1`, f.account)
	})
	r := NewTaskRepository(pool).(*taskRepository)
	window := 9 * time.Minute

	// Passes of several campaigns reach for the same mailbox at once.
	var won atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := r.ClaimMailboxSend(ctx, f.account, window)
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			if ok {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := won.Load(); n != 1 {
		t.Fatalf("%d concurrent claims won the same gap, want exactly 1", n)
	}

	// The claim is on the min-gap clock, single and batch, before any send.
	last, err := r.GetLastEmailTime(ctx, f.account)
	if err != nil || last == nil || time.Since(*last) > time.Minute {
		t.Fatalf("last email time = %v (err %v), want the claim just taken", last, err)
	}
	lasts, err := r.GetLastEmailTimes(ctx, []uuid.UUID{f.account})
	if err != nil || time.Since(lasts[f.account]) > time.Minute {
		t.Fatalf("batch last email times = %v (err %v), want the claim just taken", lasts, err)
	}

	// Once the window has passed the mailbox can be taken again.
	f.exec(`UPDATE email_accounts SET send_claimed_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`, f.account)
	if ok, err := r.ClaimMailboxSend(ctx, f.account, window); err != nil || !ok {
		t.Fatalf("claim after the window: ok=%v err=%v, want it taken", ok, err)
	}
}

func TestLiveMailboxSendClaimWaitsOutAReservedSend(t *testing.T) {
	_, pool := liveContactDB(t)
	requireSchemaVersion(t, pool, 289)
	ctx := context.Background()
	f := newWarmupUsageFixture(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tasks WHERE email_account_id = $1`, f.account)
	})
	r := NewTaskRepository(pool).(*taskRepository)

	// A warmup send reserved a minute ago holds the mailbox like a claim does.
	reserved := uuid.New()
	f.exec(`INSERT INTO tasks (id, task_type, email_account_id, status, message_id, completed_at, send_reserved_at)
	        VALUES ($1, 'warmup', $2, 'completed', '', NOW() - INTERVAL '1 minute', NOW() - INTERVAL '1 minute')`,
		reserved, f.account)
	if ok, err := r.ClaimMailboxSend(ctx, f.account, 5*time.Minute); err != nil || ok {
		t.Fatalf("claim inside a reserved send's gap: ok=%v err=%v, want it refused", ok, err)
	}

	// A reservation given back never left, so it holds nothing.
	f.exec(`UPDATE tasks SET send_released_at = NOW() WHERE id = $1`, reserved)
	if ok, err := r.ClaimMailboxSend(ctx, f.account, 5*time.Minute); err != nil || !ok {
		t.Fatalf("claim after the reservation was released: ok=%v err=%v, want it taken", ok, err)
	}
}

func TestLiveBookedSendsLeaveOutCampaignWakeups(t *testing.T) {
	_, pool := liveContactDB(t)
	ctx := context.Background()
	f := newWarmupUsageFixture(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tasks WHERE email_account_id = $1`, f.account)
	})
	r := NewTaskRepository(pool)
	now := time.Now()

	park, warmup, nextDay, farOff := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.exec(`INSERT INTO tasks (id, task_type, email_account_id, status, message_id, scheduled_at)
	        VALUES ($1, 'campaign', $2, 'pending', '', $3)`, park, f.account, now.Add(10*time.Minute))
	f.exec(`INSERT INTO tasks (id, task_type, email_account_id, status, message_id, scheduled_at)
	        VALUES ($1, 'warmup', $2, 'pending', '', $3)`, warmup, f.account, now.Add(12*time.Minute))
	// Later the next day is still within reach of the candidate.
	f.exec(`INSERT INTO tasks (id, task_type, email_account_id, status, message_id, scheduled_at)
	        VALUES ($1, 'email', $2, 'pending', '', $3)`, nextDay, f.account, now.Add(20*time.Hour))
	f.exec(`INSERT INTO tasks (id, task_type, email_account_id, status, message_id, scheduled_at)
	        VALUES ($1, 'warmup', $2, 'pending', '', $3)`, farOff, f.account, now.Add(50*time.Hour))

	booked, err := r.GetBookedSendsForAccount(ctx, f.account, now)
	if err != nil {
		t.Fatalf("booked sends: %v", err)
	}
	got := map[uuid.UUID]bool{}
	for _, b := range booked {
		got[b.ID] = true
	}
	if got[park] {
		t.Fatal("another campaign's parked wake-up was returned as a booked send")
	}
	if !got[warmup] || !got[nextDay] {
		t.Fatalf("booked sends %v, want the warmup send and the next-day email", got)
	}
	if got[farOff] {
		t.Fatal("a send two days out was returned as near the candidate")
	}
}
