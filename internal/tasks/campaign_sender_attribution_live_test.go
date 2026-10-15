package tasks

import (
	"context"
	"os"
	"sort"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/encrypt"
	"github.com/warmbly/warmbly/internal/repository"
	"github.com/warmbly/warmbly/internal/scheduler"
	"github.com/warmbly/warmbly/internal/tasks/proto"
)

// Issue #392: a campaign rotating across several mailboxes attributed every
// send to the wrong one. The chain creates its successor before rotation has
// chosen a mailbox for it, so tasks.email_account_id named the previous tick's
// pick and was never corrected. That column is what attributes a send to a
// mailbox everywhere else (today's budget, the min-gap clock, round-robin's own
// position, bounce and complaint rates, the contact's activity feed), so the
// activity said one mailbox while another one actually sent.
//
// Skipped unless WARMBLY_TEST_DB is set:
//
//	WARMBLY_TEST_DB=postgres://warmbly:warmbly@localhost:15432/warmbly_dev?sslmode=disable \
//	  go test ./internal/tasks/ -run Live -v

// attributingSender records which mailbox each task was actually dispatched
// from, which is the ground truth the stamped task row has to agree with.
type attributingSender struct {
	mu   sync.Mutex
	from map[uuid.UUID]uuid.UUID
	msgs map[uuid.UUID]EmailMessage
}

func (s *attributingSender) Send(ctx context.Context, taskID uuid.UUID, msg EmailMessage, account models.Email) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.from == nil {
		s.from = map[uuid.UUID]uuid.UUID{}
		s.msgs = map[uuid.UUID]EmailMessage{}
	}
	s.from[taskID] = account.ID
	s.msgs[taskID] = msg
	return nil
}

func (s *attributingSender) sentFrom(taskID uuid.UUID) (uuid.UUID, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.from[taskID]
	return id, ok
}

// message is what went on the wire for a task, as the worker saw it.
func (s *attributingSender) message(taskID uuid.UUID) (EmailMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, ok := s.msgs[taskID]
	return msg, ok
}

type rotationFixture struct {
	pool           *pgxpool.Pool
	handle         *db.DB
	user, org      uuid.UUID
	mailboxes      []uuid.UUID
	campaign, step uuid.UUID
	leads          []uuid.UUID
	svc            *tasksService
	sender         *attributingSender
}

// newRotationFixture builds a round-robin campaign over two mailboxes with one
// lead per planned tick, so every tick sends and rotation is the only thing
// deciding which mailbox does it.
func newRotationFixture(t *testing.T, leads int) *rotationFixture {
	t.Helper()
	dsn := os.Getenv("WARMBLY_TEST_DB")
	if dsn == "" {
		t.Skip("WARMBLY_TEST_DB not set")
	}
	ctx := context.Background()
	handle, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { handle.Pool.Close() })
	pool := handle.Pool

	f := &rotationFixture{
		pool: pool, handle: handle, user: uuid.New(), org: uuid.New(),
		campaign: uuid.New(), step: uuid.New(),
		mailboxes: []uuid.UUID{uuid.New(), uuid.New()},
		sender:    &attributingSender{},
	}

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture %q: %v", sql[:min(60, len(sql))], err)
		}
	}
	exec(`INSERT INTO users (id, email, first_name, last_name) VALUES ($1, $2, 'Live', 'Rotation')`,
		f.user, "rot-"+f.user.String()[:8]+"@test.local")
	exec(`INSERT INTO organizations (id, name, slug, owner_user_id) VALUES ($1, 'Live Rotation', $2, $3)`,
		f.org, "rot-"+f.org.String()[:8], f.user)
	for _, mb := range f.mailboxes {
		exec(`INSERT INTO email_accounts (id, user_id, organization_id, email, name,
		          signature_plain, signature_html, provider, status, campaign_limit, min_wait_time, timezone)
		      VALUES ($1, $2, $3, $4, 'Live', '', '', 'smtp_imap', 'active', 50, 0, 'UTC')`,
			mb, f.user, f.org, "rot-"+mb.String()[:8]+"@test.local")
	}
	// No campaign_senders rows: the pool is "all active mailboxes", the shape a
	// customer gets by default, and the one where rotation reads its position
	// off each mailbox's own send count.
	exec(`INSERT INTO campaigns (id, user_id, organization_id, name, description, status,
	          daily_limit, timezone, days, start_time, end_time, rotation_mode, updated_at, created_at)
	      VALUES ($1, $2, $3, 'Live Rotation', '', 'active', 50, 'UTC', 127, '00:00', '23:59',
	              'round_robin', NOW(), NOW())`, f.campaign, f.user, f.org)
	exec(`INSERT INTO sequences (id, campaign_id, organization_id, name, subject,
	          body_plain, body_html, wait_after, position, kind)
	      VALUES ($1, $2, $3, 'Step 1', 'Hi', 'Hello', '<p>Hello</p>', 0, 0, 'email')`, f.step, f.campaign, f.org)
	for i := 0; i < leads; i++ {
		id := uuid.New()
		f.leads = append(f.leads, id)
		exec(`INSERT INTO contacts (id, user_id, organization_id, email, first_name, last_name, company, phone, custom_fields, verification_status, created_at)
		      VALUES ($1, $2, $3, $4, 'Live', 'Contact', '', '', '{}', 'valid', NOW() - make_interval(secs => $5))`,
			id, f.user, f.org, "rot-lead-"+id.String()[:8]+"@test.local", float64(leads-i))
		exec(`INSERT INTO campaign_leads (campaign_id, contact_id, position) VALUES ($1, $2, $3)`, f.campaign, id, i)
	}

	t.Cleanup(func() {
		c := context.Background()
		for _, step := range []struct {
			sql string
			arg any
		}{
			{`DELETE FROM task_failures WHERE task_id IN (SELECT task_id FROM campaign_tasks WHERE campaign_id = $1)`, f.campaign},
			{`DELETE FROM task_execution_keys WHERE task_id IN (SELECT task_id FROM campaign_tasks WHERE campaign_id = $1)`, f.campaign},
			{`DELETE FROM tasks WHERE id IN (SELECT task_id FROM campaign_tasks WHERE campaign_id = $1)`, f.campaign},
			{`DELETE FROM campaign_tasks WHERE campaign_id = $1`, f.campaign},
			{`DELETE FROM campaign_logs WHERE campaign_id = $1`, f.campaign},
			{`DELETE FROM campaign_daily_sends WHERE campaign_id = $1`, f.campaign},
			{`DELETE FROM campaign_contact_progress WHERE campaign_id = $1`, f.campaign},
			{`DELETE FROM campaign_leads WHERE campaign_id = $1`, f.campaign},
			{`DELETE FROM sequences WHERE campaign_id = $1`, f.campaign},
			{`DELETE FROM campaigns WHERE id = $1`, f.campaign},
			{`DELETE FROM email_accounts WHERE organization_id = $1`, f.org},
			{`DELETE FROM contacts WHERE organization_id = $1`, f.org},
			{`DELETE FROM organizations WHERE id = $1`, f.org},
			{`DELETE FROM users WHERE id = $1`, f.user},
		} {
			if _, err := pool.Exec(c, step.sql, step.arg); err != nil {
				t.Errorf("cleanup %q: %v", step.sql, err)
			}
		}
	})

	enc, err := encrypt.NewEncrypter([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("encrypter: %v", err)
	}
	emailRepo := repository.NewEmailRepostory(handle, enc)
	campaignRepo := repository.NewCampaignRepostory(handle)
	contactRepo := repository.NewContactRepostory(handle)
	logRepo := repository.NewCampaignLogRepository(handle)
	progressRepo := repository.NewCampaignProgressRepository(pool)
	taskRepo := repository.NewTaskRepository(pool)
	f.svc = &tasksService{
		tasksClient:          noopTaskScheduler{},
		scheduler:            scheduler.NewSchedulerService(taskRepo, repository.NewWarmupRepository(pool), progressRepo, emailRepo, campaignRepo, contactRepo, logRepo),
		cipherService:        noopCipher{},
		emailSender:          f.sender,
		taskRepo:             taskRepo,
		campaignProgressRepo: progressRepo,
		emailRepo:            emailRepo,
		campaignRepo:         campaignRepo,
		contactRepo:          contactRepo,
		campaignLogRepo:      logRepo,
		trackedLinkRepo:      repository.NewTrackedLinkRepository(pool),
	}
	return f
}

// tick runs one real campaign tick on a fresh due task seeded, as the chain
// seeds it, with the mailbox the PREVIOUS send used. Returns the task id.
func (f *rotationFixture) tick(t *testing.T, seed uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx,
		`UPDATE tasks SET status = 'cancelled' WHERE status = 'pending' AND id IN (
			SELECT task_id FROM campaign_tasks WHERE campaign_id = $1)`, f.campaign); err != nil {
		t.Fatalf("clear pending tasks: %v", err)
	}
	taskID := uuid.New()
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO tasks (id, task_type, email_account_id, status, message_id, scheduled_at, created_at, updated_at)
		VALUES ($1, 'campaign', $2, 'pending', '', NOW(), NOW(), NOW())`, taskID, seed); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO campaign_tasks (task_id, campaign_id) VALUES ($1, $2)`,
		taskID, f.campaign); err != nil {
		t.Fatalf("create campaign task: %v", err)
	}
	if xerr := f.svc.HandleCampaignTask(&proto.ProcessTask{TaskId: taskID.String()}); xerr != nil {
		t.Fatalf("campaign tick: %v", xerr)
	}
	return taskID
}

func (f *rotationFixture) taskAccount(t *testing.T, taskID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(context.Background(),
		`SELECT email_account_id FROM tasks WHERE id = $1`, taskID).Scan(&id); err != nil {
		t.Fatalf("read task mailbox: %v", err)
	}
	return id
}

// TestLiveRotatingSendIsAttributedToTheMailboxThatSentIt is issue #392: the
// task row must name the mailbox the email actually left from, whatever the
// chain guessed when it created the task.
func TestLiveRotatingSendIsAttributedToTheMailboxThatSentIt(t *testing.T) {
	f := newRotationFixture(t, 4)

	// Seed every tick with the WRONG mailbox on purpose: the first one in the
	// pool, which round-robin will stop choosing as soon as it has sent. This is
	// what the chain does naturally, and what used to be left uncorrected.
	seed := f.mailboxes[0]
	for i := 0; i < 4; i++ {
		taskID := f.tick(t, seed)
		want, ok := f.sender.sentFrom(taskID)
		if !ok {
			t.Fatalf("tick %d dispatched nothing", i+1)
		}
		if got := f.taskAccount(t, taskID); got != want {
			t.Fatalf("tick %d: the task is recorded against mailbox %s but the email was sent from %s",
				i+1, got, want)
		}
	}
}

// TestLiveRoundRobinSpreadsAcrossMailboxes is the consequence: round-robin
// reads its position from each mailbox's own send count, so mis-attributed
// sends made it rotate off the wrong tally and pile onto one mailbox.
func TestLiveRoundRobinSpreadsAcrossMailboxes(t *testing.T) {
	f := newRotationFixture(t, 4)

	seed := f.mailboxes[0]
	for i := 0; i < 4; i++ {
		f.tick(t, seed)
	}

	counts := map[uuid.UUID]int{}
	for _, mb := range f.mailboxes {
		var n int
		if err := f.pool.QueryRow(context.Background(), `
			SELECT COUNT(*) FROM tasks t
			JOIN campaign_tasks ct ON ct.task_id = t.id
			WHERE ct.campaign_id = $1 AND t.email_account_id = $2 AND t.status = 'completed'`,
			f.campaign, mb).Scan(&n); err != nil {
			t.Fatalf("count sends: %v", err)
		}
		counts[mb] = n
	}
	got := []int{counts[f.mailboxes[0]], counts[f.mailboxes[1]]}
	sort.Ints(got)
	if got[0] != 2 || got[1] != 2 {
		t.Fatalf("four round-robin sends across two mailboxes landed %v, want 2 each", got)
	}
}
