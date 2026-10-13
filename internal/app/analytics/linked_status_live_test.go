package analytics

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
	"github.com/warmbly/warmbly/internal/repository"
)

type statusQueryCounter struct{ queries atomic.Int64 }

func (c *statusQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.queries.Add(1)
	return ctx
}

func (*statusQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestLiveLinkedMailboxStatusBatchPreservesDetails(t *testing.T) {
	f := newRampFixture(t, 12, 3, 2, 30)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE organizations SET risk_state='trusted' WHERE id=$1`, f.org)
	exec(`UPDATE email_accounts SET test_mode=NULL WHERE id IN ($1,$2)`, f.mailbox, f.gmail)
	ids := []uuid.UUID{f.mailbox}
	for i := 0; i < 14; i++ {
		id := uuid.New()
		provider := "gmail"
		if i < 4 {
			provider = "smtp_imap"
		}
		exec(`INSERT INTO email_accounts (id, user_id, organization_id, email, name, signature_plain,
		      signature_html, provider, status, campaign_limit, min_wait_time, timezone,
		      warmup, warmup_base, warmup_increase, warmup_max, test_mode)
		      VALUES ($1,$2,$3,$4,'Linked','','',$5,'active',50,600,'UTC',
		              NOW()-interval '12 days',3,2,30,NULL)`,
			id, f.user, f.org, "linked-"+id.String()+"@example.test", provider)
		ids = append(ids, id)
	}
	for _, id := range append(append([]uuid.UUID{}, ids...), f.gmail) {
		exec(`INSERT INTO warmup_pool_participants (pool_id, email_account_id, participant_role, health_state)
		      VALUES ($1,$2,'sender_receiver','healthy')`, models.WarmupPoolPremiumID, id)
	}
	// A restrictive row and a paused mailbox must retain the same detail in the list.
	exec(`UPDATE warmup_pool_participants SET health_state='quarantined', blocked_until=NOW()+interval '2 days' WHERE email_account_id=$1`, ids[1])
	exec(`UPDATE email_accounts SET warmup_paused_at=NOW() WHERE id=$1`, ids[2])
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
	start := time.Now()
	individual := make(map[uuid.UUID]models.EmailAccountStatus)
	for _, id := range ids {
		status, xerr := svc.GetAccountStatusDetail(t.Context(), f.org, id)
		if xerr != nil {
			t.Fatal(xerr)
		}
		individual[id] = *status
	}
	oldQueries, oldDuration := counter.queries.Swap(0), time.Since(start)
	start = time.Now()
	page, xerr := svc.GetAccountStatusDetailsPage(t.Context(), f.org, ids, "", config.AccountStatusLimitMax)
	if xerr != nil || len(page.Data) != 15 {
		t.Fatalf("page=%+v error=%v", page, xerr)
	}
	newQueries, newDuration := counter.queries.Swap(0), time.Since(start)
	for _, status := range page.Data {
		want := individual[status.ID]
		if !reflect.DeepEqual(status.WarmupStatus, want.WarmupStatus) || !reflect.DeepEqual(status.WarmupHealth, want.WarmupHealth) || !reflect.DeepEqual(status.Errors, want.Errors) || !reflect.DeepEqual(status.DailyUsage, want.DailyUsage) {
			t.Fatalf("%s: batched=%+v individual=%+v", status.ID, status, want)
		}
	}
	if newQueries >= oldQueries {
		t.Fatalf("batch queries=%d individual=%d", newQueries, oldQueries)
	}
	t.Logf("15 mixed-provider mailbox details: individual %d queries in %s; batch %d queries in %s", oldQueries, oldDuration, newQueries, newDuration)
	foreign := newRampFixture(t, 12, 3, 2, 30)
	page, xerr = svc.GetAccountStatusDetailsPage(t.Context(), f.org, []uuid.UUID{ids[0], foreign.mailbox}, "", config.AccountStatusLimitMax)
	if xerr != nil || len(page.Data) != 1 || page.Data[0].ID != ids[0] {
		t.Fatalf("foreign organization leaked: %+v %v", page, xerr)
	}
}
