package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// The scheduler's backlog is exactly the contacts past a shelf life, in the
// order it promises, whichever reason makes each one due.
func TestLiveVerificationBacklogFollowsShelfLives(t *testing.T) {
	handle, pool := liveContactDB(t)
	f := newSharedOrgFixture(t, pool)
	ctx := context.Background()
	repo := &contactRepository{DB: handle}

	contact := func(status, source, checked, evidence string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO contacts (id, user_id, organization_id, email, first_name, last_name, company, phone,
		          custom_fields, updated_at, created_at,
		          verification_status, verification_provider, verification_source, verification_checked_at,
		          verification_evidence_at)
		      VALUES ($1, $2, $3, $4, 'Ada', 'Ng', '', '', '{}'::jsonb, NOW(), NOW(),
		          $5, 'builtin', $6, NOW() - $7::interval, NOW() - $8::interval)`,
			id, f.owner, f.org, "due-"+id.String()[:8]+"@test.local", status, source, checked, evidence); err != nil {
			t.Fatalf("fixture: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM contacts WHERE id = $1`, id)
		})
		return id
	}
	never := contact("unknown", "probe", "0 days", "1000 days")
	if _, err := pool.Exec(ctx, `UPDATE contacts SET verification_checked_at = NULL WHERE id = $1`, never); err != nil {
		t.Fatal(err)
	}
	unknownAged := contact("unknown", "probe", "31 days", "1000 days")
	validAged := contact("valid", "probe", "91 days", "1000 days")
	validFresh := contact("valid", "probe", "31 days", "1000 days")
	excused := contact("valid", "probe", "91 days", "1 day")
	manual := contact("valid", "manual", "91 days", "1000 days")

	got, xerr := repo.ListVerificationCandidates(ctx, 500)
	if xerr != nil {
		t.Fatalf("list candidates: %v", xerr)
	}
	pos := map[uuid.UUID]int{}
	for i, c := range got {
		pos[c.ID] = i + 1
	}
	for name, id := range map[string]uuid.UUID{"never checked": never, "unknown past 30 days": unknownAged, "valid past 90 days": validAged} {
		if pos[id] == 0 {
			t.Fatalf("%s is not a candidate", name)
		}
	}
	for name, id := range map[string]uuid.UUID{"valid inside 90 days": validFresh, "excused by fresh mail": excused, "manual": manual} {
		if pos[id] != 0 {
			t.Fatalf("%s is a candidate", name)
		}
	}
	if !(pos[never] < pos[validAged] && pos[validAged] < pos[unknownAged]) {
		t.Fatalf("order never=%d valid91=%d unknown31=%d, want never checked first, then oldest check", pos[never], pos[validAged], pos[unknownAged])
	}
}

// A linked instance's heartbeat is recorded at most once a minute, and a new
// version is recorded at once.
func TestLivePoolLinkTouchIsThrottledButKeepsTheVersion(t *testing.T) {
	_, pool := liveContactDB(t)
	f := newSharedOrgFixture(t, pool)
	ctx := context.Background()
	repo := &poolLinkRepository{db: pool}

	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO pool_link_instances (id, organization_id, name, version, token_hash, last_seen_at)
	      VALUES ($1, $2, 'self-hosted', 'v1', $3, NOW() - INTERVAL '10 seconds')`, id, f.org, "hash-"+id.String()); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM pool_link_instances WHERE id = $1`, id)
	})
	seen := func() (float64, string) {
		t.Helper()
		var age float64
		var version string
		if err := pool.QueryRow(ctx, `SELECT EXTRACT(EPOCH FROM NOW() - last_seen_at), version FROM pool_link_instances WHERE id = $1`, id).Scan(&age, &version); err != nil {
			t.Fatal(err)
		}
		return age, version
	}

	if err := repo.TouchInstance(ctx, id, "v1"); err != nil {
		t.Fatal(err)
	}
	if age, _ := seen(); age < 5 {
		t.Fatalf("a touch inside the minute was written (age %.1fs)", age)
	}
	if err := repo.TouchInstance(ctx, id, "v2"); err != nil {
		t.Fatal(err)
	}
	if age, version := seen(); version != "v2" || age > 5 {
		t.Fatalf("a new version was not recorded: version %q, age %.1fs", version, age)
	}
	if _, err := pool.Exec(ctx, `UPDATE pool_link_instances SET last_seen_at = NOW() - INTERVAL '2 minutes' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := repo.TouchInstance(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if age, version := seen(); version != "v2" || age > 5 {
		t.Fatalf("a stale heartbeat was not refreshed: version %q, age %.1fs", version, age)
	}
}
