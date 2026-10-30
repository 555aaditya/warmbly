package repository

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func contentPoolFixture(t *testing.T) (*poolLinkFixture, *warmupContentRepository, string, uuid.UUID) {
	t.Helper()
	f, _ := lineageFixture(t)
	segment, conversation := "fixture-"+uuid.NewString(), uuid.New()
	t.Cleanup(func() { _, _ = f.pool.Exec(t.Context(), `DELETE FROM warmup_conversations WHERE segment=$1`, segment) })
	_, err := f.pool.Exec(t.Context(), `INSERT INTO warmup_conversations(id,pool_type,segment,source,subject,description,messages,status,scenario_version,rendering_version,reply_eligible,lint_passed,semantic_review) VALUES($1,'premium',$2,'ai','A clock example','An automated warmup example','["Reply","Closure"]','active','diagnostic-v1','canonical-v1',true,true,'passed')`, conversation, segment)
	if err != nil {
		t.Fatal(err)
	}
	return f, NewWarmupContentRepository(f.pool).(*warmupContentRepository), segment, conversation
}

func contentPoolMailbox(t *testing.T, f *poolLinkFixture) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.pool.Exec(t.Context(), `INSERT INTO email_accounts(id,user_id,organization_id,email,name,signature_plain,signature_html,provider,status,campaign_limit,min_wait_time,timezone,test_mode,warmup,warmup_days)
 VALUES($1,$2,$3,$4,'Pool','','','smtp_imap','active',50,600,'UTC',NULL,NOW(),127)`, id, f.user, f.org, id.String()+"@test.local")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func contentPoolPick(t *testing.T, f *poolLinkFixture, segment string, sender, recipient uuid.UUID) WarmupContentPick {
	t.Helper()
	task := uuid.New()
	_, err := f.pool.Exec(t.Context(), `INSERT INTO tasks(id,task_type,email_account_id,status,message_id) VALUES($1,'warmup',$2,'pending','')`, task, sender)
	if err != nil {
		t.Fatal(err)
	}
	return WarmupContentPick{Segment: segment, TaskID: task, SenderID: sender, RecipientID: recipient}
}

func TestLiveContentPoolRetryReservesOnceAndReplenishesForExposedMailboxes(t *testing.T) {
	f, r, segment, conversation := contentPoolFixture(t)
	ctx := t.Context()
	if n, err := r.CountAvailableConversations(ctx, "premium", segment); err != nil || n != 1 {
		t.Fatalf("initial supply=%d err=%v", n, err)
	}
	pick := contentPoolPick(t, f, segment, f.sender, f.recipient)
	var successes atomic.Int32
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			c, err := r.PickConversation(ctx, pick)
			if err != nil || c == nil || c.ID != conversation {
				t.Errorf("retry=%+v err=%v", c, err)
				return
			}
			successes.Add(1)
		})
	}
	workers.Wait()
	if successes.Load() != 12 {
		t.Fatalf("successful retries=%d", successes.Load())
	}
	c, err := r.GetConversation(ctx, conversation)
	if err != nil || c == nil || c.UsageCount != 1 || !c.ReplyEligible {
		t.Fatalf("retry changed budget or replies: %+v %v", c, err)
	}
	if n, err := r.CountAvailableConversations(ctx, "premium", segment); err != nil || n != 0 {
		t.Fatalf("mailbox exhaustion hidden: %d %v", n, err)
	}
	if n, err := r.RefreshUnusedConversations(ctx, "premium", segment, 10); err != nil || n != 0 {
		t.Fatalf("ongoing thread retired: %d %v", n, err)
	}
	if _, err := f.pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, pick.TaskID); err != nil {
		t.Fatal(err)
	}
	other := contentPoolMailbox(t, f)
	for _, pair := range [][2]uuid.UUID{{f.sender, other}, {other, f.recipient}, {f.recipient, f.sender}} {
		c, err := r.PickConversation(ctx, contentPoolPick(t, f, segment, pair[0], pair[1]))
		if err != nil || c != nil {
			t.Fatalf("mailbox repeated after cleanup/role change: %+v %v", c, err)
		}
	}
}

func TestLiveContentPoolPreservesPlansForFutureDistinctMailboxes(t *testing.T) {
	f, r, segment, conversation := contentPoolFixture(t)
	for use := 1; use <= 12; use++ {
		sender, recipient := contentPoolMailbox(t, f), contentPoolMailbox(t, f)
		c, err := r.PickConversation(t.Context(), contentPoolPick(t, f, segment, sender, recipient))
		if err != nil || c == nil || c.ID != conversation || c.UsageCount != int64(use) {
			t.Fatalf("use %d: %+v %v", use, c, err)
		}
	}
	c, err := r.PickConversation(t.Context(), contentPoolPick(t, f, segment, f.sender, f.recipient))
	if err != nil || c == nil || c.ID != conversation || c.UsageCount != 13 {
		t.Fatalf("future distinct pair could not reuse saved plan: %+v %v", c, err)
	}
	if n, err := r.CountAvailableConversations(t.Context(), "premium", segment); err != nil || n != 0 {
		t.Fatalf("mailbox exhaustion hidden: %d %v", n, err)
	}
}

func TestLiveContentPoolConcurrentPairsCannotRepeatAMailbox(t *testing.T) {
	f, r, segment, conversation := contentPoolFixture(t)
	picks := make([]WarmupContentPick, 12)
	for i := range picks {
		picks[i] = contentPoolPick(t, f, segment, f.sender, contentPoolMailbox(t, f))
	}
	var successes atomic.Int32
	var workers sync.WaitGroup
	for _, pick := range picks {
		workers.Go(func() {
			c, err := r.PickConversation(t.Context(), pick)
			if err != nil {
				t.Error(err)
			} else if c != nil {
				successes.Add(1)
			}
		})
	}
	workers.Wait()
	c, err := r.GetConversation(t.Context(), conversation)
	if successes.Load() != 1 || err != nil || c == nil || c.UsageCount != 1 {
		t.Fatalf("concurrent mailbox repeat: successes=%d source=%+v err=%v", successes.Load(), c, err)
	}
}

func TestLiveContentPoolPrefersLessUsedEligiblePlans(t *testing.T) {
	f, r, segment, first := contentPoolFixture(t)
	if c, err := r.PickConversation(t.Context(), contentPoolPick(t, f, segment, f.sender, f.recipient)); err != nil || c == nil {
		t.Fatalf("first reservation: %+v %v", c, err)
	}
	second := uuid.New()
	_, err := f.pool.Exec(t.Context(), `INSERT INTO warmup_conversations(id,pool_type,segment,source,subject,description,messages,status,scenario_version,rendering_version,reply_eligible,lint_passed,semantic_review)
 SELECT $2,pool_type,segment,source,'A different example',description,messages,status,scenario_version,rendering_version,reply_eligible,lint_passed,semantic_review FROM warmup_conversations WHERE id=$1`, first, second)
	if err != nil {
		t.Fatal(err)
	}
	sender, recipient := contentPoolMailbox(t, f), contentPoolMailbox(t, f)
	c, err := r.PickConversation(t.Context(), contentPoolPick(t, f, segment, sender, recipient))
	if err != nil || c == nil || c.ID != second {
		t.Fatalf("less-used plan was not preferred: %+v %v", c, err)
	}
}

func TestLiveContentPoolHonorsHistoricalTokensAndExcludesFixedWording(t *testing.T) {
	f, r, segment, conversation := contentPoolFixture(t)
	_, err := f.pool.Exec(t.Context(), `INSERT INTO warmup_tokens(token,task_id,sender_account_id,recipient_account_id,subject,content_source,conversation_id) VALUES($1,$2,$3,$4,'A clock example','ai',$5)`, uuid.New(), f.task, f.sender, f.recipient, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if demand, err := r.WarmupMailboxDemandSince(t.Context(), time.Now().Add(-time.Hour)); err != nil || demand != 1 {
		t.Fatalf("mailbox demand=%d err=%v", demand, err)
	}
	other := contentPoolMailbox(t, f)
	for _, pair := range [][2]uuid.UUID{{f.sender, other}, {other, f.sender}, {f.recipient, other}, {other, f.recipient}} {
		c, err := r.PickConversation(t.Context(), contentPoolPick(t, f, segment, pair[0], pair[1]))
		if err != nil || c != nil {
			t.Fatalf("historical mailbox exposure ignored: %+v %v", c, err)
		}
	}
	_, err = f.pool.Exec(t.Context(), `UPDATE warmup_conversations SET subject='Simulated diagnostic: historical clock' WHERE id=$1`, conversation)
	if err != nil {
		t.Fatal(err)
	}
	sender, recipient := contentPoolMailbox(t, f), contentPoolMailbox(t, f)
	c, err := r.PickConversation(t.Context(), contentPoolPick(t, f, segment, sender, recipient))
	if err != nil || c != nil {
		t.Fatalf("fixed wording selected: %+v %v", c, err)
	}
}
