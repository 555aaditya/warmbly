package repository

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/models"
)

const legacyHealthFeedbackCountsSQL = `SELECT
	(SELECT COUNT(*) FILTER (WHERE de.event_type='complaint') FROM deliverability_events de JOIN tasks t ON t.id=de.task_id
	 WHERE t.email_account_id=$1 AND de.created_at >= $3 AND de.event_type IN ('complaint','bounce')) AS complaints,
	(SELECT COUNT(*) FILTER (WHERE de.event_type='bounce') FROM deliverability_events de JOIN tasks t ON t.id=de.task_id
	 WHERE t.email_account_id=$1 AND de.created_at >= $3 AND de.event_type IN ('complaint','bounce')) AS bounces`

const legacyHealthTamperingCountsSQL = `SELECT
	(SELECT COUNT(*) FILTER (WHERE kind='deletion') FROM warmup_tampering_events WHERE email_account_id=$1 AND created_at >= $2) AS deletions,
	(SELECT COUNT(*) FILTER (WHERE kind='spam_flag') FROM warmup_tampering_events WHERE email_account_id=$1 AND created_at >= $2) AS spam_flags`

func legacyHealthCounts(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, since7d, since30d time.Time) models.WarmupHealthCounts {
	t.Helper()
	query := `SELECT
		(SELECT COALESCE(SUM(emails_sent),0) FROM warmup_statistics WHERE email_account_id=$1 AND date >= DATE($2)),
		(SELECT COUNT(*) FROM warmup_spam_reports WHERE reported_account_id=$1 AND created_at >= $2 AND report_type='spam_placement'),
		(SELECT COUNT(*) FROM warmup_spam_reports WHERE reported_account_id=$1 AND created_at >= $2 AND report_type IN ('user_complaint','spam','spam_folder')),
		f.complaints, f.bounces,
		(SELECT COUNT(*) FROM tasks WHERE email_account_id=$1 AND status='completed' AND completed_at >= $3),
		t.deletions, t.spam_flags, p.major, p.major_spam, p.other, p.other_spam
		FROM (` + legacyHealthFeedbackCountsSQL + `) f CROSS JOIN (` + legacyHealthTamperingCountsSQL + `) t
		CROSS JOIN (` + placementEvidenceSQL("$1", "$2") + `) p`
	var c models.WarmupHealthCounts
	p := &c.Placement
	if err := pool.QueryRow(t.Context(), query, id, since7d, since30d).Scan(&c.SentLast7d, &c.SpamPlacementsLast7d,
		&c.UserComplaintsLast7d, &c.ComplaintsLast30d, &c.BouncesLast30d, &c.DeliveredLast30d,
		&c.DeletionsLast7d, &c.SpamFlagsLast7d, &p.MajorDelivered, &p.MajorSpam, &p.OtherDelivered, &p.OtherSpam); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLiveWarmupHealthCountsShareScansWithoutChangingSignals(t *testing.T) {
	f, _ := lineageFixture(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)
	since7d, since30d := now.Add(-7*24*time.Hour), now.Add(-30*24*time.Hour)
	r := NewWarmupRepository(f.pool)
	for _, id := range []uuid.UUID{f.sender, f.recipient, uuid.New()} {
		got, err := r.HealthMetricCounts(ctx, id, since7d, since7d, since30d)
		want := legacyHealthCounts(t, f.pool, id, since7d, since30d)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("empty health=%+v want=%+v err=%v", got, want, err)
		}
	}
	for _, id := range []uuid.UUID{f.sender, f.recipient} {
		var task uuid.UUID
		if err := f.pool.QueryRow(ctx, `INSERT INTO tasks(email_account_id,task_type,status,message_id,completed_at)
			VALUES($1,'warmup','completed','',$2) RETURNING id`, id, now).Scan(&task); err != nil {
			t.Fatal(err)
		}
		for _, at := range []time.Time{since30d.Add(-time.Microsecond), since30d, since7d.Add(-time.Microsecond), since7d, now} {
			for _, event := range []string{"complaint", "bounce", "unrelated"} {
				if _, err := f.pool.Exec(ctx, `INSERT INTO deliverability_events(organization_id,task_id,event_type,recipient_email,idempotency_key,created_at)
					VALUES($1,$2,$3,'signal@test.local',$4,$5)`, f.org, task, event, uuid.NewString(), at); err != nil {
					t.Fatal(err)
				}
			}
			for _, kind := range []string{"deletion", "spam_flag", "other"} {
				if _, err := f.pool.Exec(ctx, `INSERT INTO warmup_tampering_events(email_account_id,message_id,kind,created_at,verified_at)
					VALUES($1,$2,$3,$4,NULL)`, id, uuid.NewString(), kind, at); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, id := range []uuid.UUID{f.sender, f.recipient, uuid.New()} {
		got, err := r.HealthMetricCounts(ctx, id, since7d, since7d, since30d)
		want := legacyHealthCounts(t, f.pool, id, since7d, since30d)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("health=%+v want=%+v err=%v", got, want, err)
		}
		if id == f.sender && (got.ComplaintsLast30d != 4 || got.BouncesLast30d != 4 || got.DeletionsLast7d != 2 || got.SpamFlagsLast7d != 2) {
			t.Fatalf("boundary counts changed: %+v", got)
		}
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO warmup_tampering_events(email_account_id,message_id,kind,created_at)
		SELECT $1,g::text || '@pressure.test',CASE WHEN g%2=0 THEN 'deletion' ELSE 'spam_flag' END,$2
		FROM generate_series(1,10000) g`, f.sender, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO deliverability_events(organization_id,task_id,event_type,recipient_email,idempotency_key,created_at)
		SELECT $1,$2,CASE WHEN g%2=0 THEN 'complaint' ELSE 'bounce' END,'signal@test.local',g::text || '@pressure.test',$3
		FROM generate_series(1,10000) g`, f.org, f.task, now); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"deliverability_events", "warmup_tampering_events"} {
		if _, err := f.pool.Exec(ctx, "ANALYZE "+table); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := pgx.ConnectConfig(ctx, f.pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `SET plan_cache_mode=force_generic_plan`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, before, after, args string }{
		{"deliverability_events", strings.ReplaceAll(legacyHealthFeedbackCountsSQL, "$3", "$2"), strings.ReplaceAll(healthFeedbackCountsSQL, "$3", "$2"), fmt.Sprintf("'%s','%s'::timestamptz", f.sender, since30d.Format(time.RFC3339Nano))},
		{"warmup_tampering_events", legacyHealthTamperingCountsSQL, healthTamperingCountsSQL, fmt.Sprintf("'%s','%s'::timestamptz", f.sender, since7d.Format(time.RFC3339Nano))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beforeRows, beforeMS, _ := taskPressureExplain(t, conn, tc.before, tc.args, tc.name)
			afterRows, afterMS, _ := taskPressureExplain(t, conn, tc.after, tc.args, tc.name)
			t.Logf("synthetic 10000 added signals: generic plan before %.3fms / %.0f signal rows, after %.3fms / %.0f signal rows", beforeMS, beforeRows, afterMS, afterRows)
			if afterRows == 0 || beforeRows != 2*afterRows {
				t.Fatalf("expected one signal scan instead of two: before=%v after=%v", beforeRows, afterRows)
			}
		})
	}
}
