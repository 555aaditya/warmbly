package analytics

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
	"github.com/warmbly/warmbly/internal/repository"
)

// A status page reads its mailboxes' signals in a fixed number of queries, and
// every row matches the status the same mailbox reports on its own.
func TestLiveAccountStatusPageMatchesSingleReadsInBoundedQueries(t *testing.T) {
	f := newRampFixture(t, 12, 3, 2, 30)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	ids := []uuid.UUID{f.mailbox}
	for i := 0; i < 15; i++ {
		id := uuid.New()
		exec(`INSERT INTO email_accounts (id, user_id, organization_id, email, name, signature_plain,
		      signature_html, provider, status, campaign_limit, min_wait_time, timezone,
		      warmup, warmup_base, warmup_increase, warmup_max, test_mode)
		      VALUES ($1,$2,$3,$4,'Page','','','smtp_imap','active',50,600,'UTC',
		              NOW()-interval '12 days',3,2,30,NULL)`,
			id, f.user, f.org, "page-"+id.String()+"@example.test")
		ids = append(ids, id)
	}
	t.Cleanup(func() {
		for _, id := range ids[1:] {
			_, _ = f.pool.Exec(t.Context(), `DELETE FROM warmup_tokens WHERE sender_account_id = $1 OR recipient_account_id = $1`, id)
			_, _ = f.pool.Exec(t.Context(), `DELETE FROM tasks WHERE email_account_id = $1`, id)
			_, _ = f.pool.Exec(t.Context(), `DELETE FROM warmup_received WHERE email_account_id = $1`, id)
			_, _ = f.pool.Exec(t.Context(), `DELETE FROM warmup_spam_reports WHERE reported_account_id = $1`, id)
			_, _ = f.pool.Exec(t.Context(), `DELETE FROM email_accounts WHERE id = $1`, id)
		}
	})
	// Half sit in a pool, one quarantined; one is paused, one never warmed.
	for _, id := range ids[:8] {
		exec(`INSERT INTO warmup_pool_participants (pool_id, email_account_id, participant_role, health_state)
		      VALUES ($1,$2,'sender_receiver','healthy')`, models.WarmupPoolPremiumID, id)
	}
	exec(`UPDATE warmup_pool_participants SET health_state='quarantined', blocked_until=NOW()+interval '2 days' WHERE email_account_id=$1`, ids[1])
	exec(`UPDATE email_accounts SET warmup_paused_at=NOW() WHERE id=$1`, ids[2])
	exec(`UPDATE email_accounts SET warmup=NULL WHERE id=$1`, ids[9])
	// Traffic, a recent major-provider placement and a failed send to read back.
	for i, id := range ids[:6] {
		task := uuid.New()
		exec(`INSERT INTO tasks (id, task_type, email_account_id, status, message_id, completed_at) VALUES ($1,'warmup',$2,'completed','',NOW() - interval '2 hours')`, task, id)
		exec(`INSERT INTO warmup_tokens (token, task_id, sender_account_id, recipient_account_id, sent_message_id, created_at)
		      VALUES (gen_random_uuid(), $1, $2, $3, $4, NOW() - interval '1 hour')`, task, id, ids[(i+1)%6], "<"+task.String()+"@t>")
		exec(`INSERT INTO warmup_received (email_account_id, internal_id, message_id, sender_account_id, created_at)
		      VALUES ($1, gen_random_uuid(), $2, $3, NOW() - interval '1 hour')`, ids[(i+1)%6], "<"+task.String()+"@t>", id)
	}
	exec(`INSERT INTO warmup_spam_reports (id, reporter_account_id, reported_account_id, message_id, report_type, created_at)
	      VALUES (gen_random_uuid(), $1, $2, $3, 'spam_placement', NOW() - interval '2 hours')`, f.gmail, ids[3], "msg-"+uuid.NewString())
	exec(`INSERT INTO warmup_statistics (email_account_id, date, emails_sent, emails_replied, target_volume) VALUES ($1, CURRENT_DATE, 4, 0, 10)`, ids[3])
	failed := uuid.New()
	exec(`INSERT INTO tasks (id, task_type, email_account_id, status, message_id, updated_at) VALUES ($1,'warmup',$2,'failed','',NOW() - interval '10 minutes')`, failed, ids[4])
	exec(`INSERT INTO task_failures (task_id, title, message) VALUES ($1, 'Send failed', 'Mailbox refused the login')`, failed)

	counter := &statusQueryCounter{}
	poolConfig := f.pool.Config()
	poolConfig.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(t.Context(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	enc, err := encrypt.NewEncrypter([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	handle := &db.DB{Pool: pool}
	svc := NewService(repository.NewAnalyticsRepository(handle), repository.NewEmailRepostory(handle, enc),
		repository.NewCampaignRepostory(handle), repository.NewEmailAccountErrorRepository(handle), repository.NewWarmupRepository(pool))

	single := make(map[uuid.UUID]models.EmailAccountStatus)
	for _, id := range ids {
		status, xerr := svc.GetAccountStatus(t.Context(), f.org, id)
		if xerr != nil {
			t.Fatal(xerr)
		}
		single[id] = *status
	}
	queriesFor := func(ids []uuid.UUID) int64 {
		t.Helper()
		counter.queries.Store(0)
		page, xerr := svc.GetAccountStatusesPage(t.Context(), f.org, ids, "", config.AccountStatusLimitMax)
		if xerr != nil || len(page.Data) != len(ids) {
			t.Fatalf("page=%+v error=%v", page, xerr)
		}
		for _, got := range page.Data {
			want := single[got.ID]
			got.LastSyncedAt, want.LastSyncedAt = nil, nil
			if got.WarmupStatus != nil && want.WarmupStatus != nil {
				// DaysActive and the ramp read the clock; both reads fall on the same day.
				got.WarmupStatus.DaysActive = want.WarmupStatus.DaysActive
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: page=%+v single=%+v", got.ID, got, want)
			}
		}
		return counter.queries.Load()
	}
	// The small page touches every kind of read the large one does.
	small, large := queriesFor([]uuid.UUID{ids[0], ids[3], ids[4], ids[8]}), queriesFor(ids)
	if large != small {
		t.Fatalf("page of %d took %d queries, page of 4 took %d", len(ids), large, small)
	}
	if s := single[ids[3]]; s.WarmupStatus == nil || s.WarmupStatus.RampHold == nil {
		t.Fatalf("the placed mailbox reports no hold: %+v", s.WarmupStatus)
	}
	if s := single[ids[4]]; s.WarmupStatus == nil || s.WarmupStatus.SendFailure == nil {
		t.Fatalf("the failing mailbox reports no send failure: %+v", s.WarmupStatus)
	}
	if s := single[ids[0]]; s.WarmupHealth == nil || s.WarmupHealth.PartnerMailboxes7d != 1 || s.WarmupHealth.Received7d != 1 {
		t.Fatalf("partner diversity missing: %+v", s.WarmupHealth)
	}
	t.Logf("status page: %d queries for %d mailboxes, %d for %d", small, 4, large, len(ids))
}
