package repository

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLiveWarmupDispatchUsagePreservesBudgetAndLastSend(t *testing.T) {
	f, r := lineageFixture(t)
	ctx := t.Context()
	start := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	hourStart, hourEnd := start.Add(10*time.Hour), start.Add(11*time.Hour)
	excluded := uuid.New()
	lastSend := end.Add(time.Hour)
	for _, row := range []struct {
		id        uuid.UUID
		account   uuid.UUID
		kind      string
		status    string
		message   string
		completed any
		updated   time.Time
	}{
		{uuid.New(), f.sender, "warmup", "completed", "day-start@test.local", start, end},
		{uuid.New(), f.sender, "campaign", "active", "hour-start@test.local", nil, hourStart},
		{uuid.New(), f.sender, "email", "completed", "hour-end@test.local", hourEnd, end},
		{uuid.New(), f.sender, "placement", "completed", "day-end@test.local", end, end},
		{uuid.New(), f.sender, "warmup", "completed", "future@test.local", lastSend, lastSend},
		{uuid.New(), f.sender, "warmup", "completed", "historical@test.local", start.Add(-48 * time.Hour), start},
		{uuid.New(), f.sender, "warmup", "completed", "fallback@test.local", nil, hourStart.Add(time.Minute)},
		{excluded, f.sender, "warmup", "active", "excluded@test.local", nil, end.Add(10 * time.Hour)},
		{uuid.New(), f.sender, "warmup", "failed", "failed@test.local", nil, end.Add(11 * time.Hour)},
		{uuid.New(), f.sender, "warmup", "pending", "pending@test.local", nil, end.Add(12 * time.Hour)},
		{uuid.New(), f.sender, "warmup", "completed", "", nil, end.Add(13 * time.Hour)},
		{uuid.New(), f.recipient, "warmup", "completed", "other-mailbox@test.local", nil, end.Add(14 * time.Hour)},
	} {
		if _, err := f.pool.Exec(ctx, `INSERT INTO tasks(id,email_account_id,task_type,status,message_id,completed_at,updated_at)
			VALUES($1,$2,$3::task_type,$4::task_status,$5,$6,$7)`, row.id, row.account, row.kind, row.status, row.message, row.completed, row.updated); err != nil {
			t.Fatal(err)
		}
	}
	warmup, daily, hourly, last, err := r.WarmupDispatchUsage(ctx, f.sender, excluded, start, end, hourStart, hourEnd)
	if err != nil || warmup != 2 || daily != 4 || hourly != 2 || last == nil || !last.Equal(lastSend) {
		t.Fatalf("usage=%d/%d/%d last=%v error=%v", warmup, daily, hourly, last, err)
	}
	// The last send remains authoritative even when neither budget window contains it.
	warmup, daily, hourly, last, err = r.WarmupDispatchUsage(ctx, f.sender, excluded, end.Add(48*time.Hour), end.Add(72*time.Hour), start.Add(-72*time.Hour), start.Add(-47*time.Hour))
	if err != nil || warmup != 0 || daily != 0 || hourly != 1 || last == nil || !last.Equal(lastSend) {
		t.Fatalf("disjoint usage=%d/%d/%d last=%v error=%v", warmup, daily, hourly, last, err)
	}
	warmup, daily, hourly, last, err = r.WarmupDispatchUsage(ctx, uuid.New(), excluded, start, end, hourStart, hourEnd)
	if err != nil || warmup != 0 || daily != 0 || hourly != 0 || last != nil {
		t.Fatalf("empty usage=%d/%d/%d last=%v error=%v", warmup, daily, hourly, last, err)
	}
}

func TestLiveWarmupDispatchQueryPlansUseMailboxAndMessageIndexes(t *testing.T) {
	f, _ := lineageFixture(t)
	ctx := t.Context()
	if _, err := f.pool.Exec(ctx, `INSERT INTO tasks(email_account_id,task_type,status,message_id,completed_at)
		SELECT $1,'campaign','completed',g::text || '@query-plan.test',NOW()-INTERVAL '3 days'
		FROM generate_series(1,20000) g`, f.recipient); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO tasks(email_account_id,task_type,status,message_id,completed_at)
		SELECT $1,'warmup','completed',g::text || '@mailbox-history.test',NOW()-INTERVAL '3 days'
		FROM generate_series(1,5000) g`, f.sender); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `ANALYZE tasks`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query, args, index string
	}{
		{"budget", warmupDispatchUsageSQL, fmt.Sprintf("'%s','%s',date_trunc('day',now()),date_trunc('day',now())+interval '1 day',date_trunc('hour',now()),date_trunc('hour',now())+interval '1 hour'", f.sender, uuid.Nil), "idx_tasks_dispatch_usage"},
		{"conversation", ownConversationSQL, fmt.Sprintf("'%s','%s',ARRAY['absent@query-plan.test'],''", f.user, f.sender), "idx_tasks_message_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := genericWarmupQueryPlan(t, f.pool, tc.query, tc.args)
			if !strings.Contains(plan, tc.index) || strings.Contains(plan, "Seq Scan on tasks") {
				t.Fatalf("task lookup must use %s with generic parameters:\n%s", tc.index, plan)
			}
			if tc.name == "budget" && (!strings.Contains(plan, "Index Cond:") || !strings.Contains(plan, "Limit")) {
				t.Fatalf("budget range and last-send lookup must be bounded:\n%s", plan)
			}
		})
	}
}

func genericWarmupQueryPlan(t *testing.T, pool *pgxpool.Pool, query, args string) string {
	t.Helper()
	conn, err := pgx.ConnectConfig(t.Context(), pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(t.Context())
	if _, err := conn.Exec(t.Context(), `SET plan_cache_mode=force_generic_plan`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(t.Context(), `PREPARE warmup_pressure_plan AS `+query); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(t.Context(), `EXPLAIN EXECUTE warmup_pressure_plan(`+args+`)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan.String()
}
