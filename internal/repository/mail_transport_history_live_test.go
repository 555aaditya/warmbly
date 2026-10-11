package repository

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

func TestLiveMailTransportNativeDomainExcludesHistory(t *testing.T) {
	f := newTransportFixture(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	for _, op := range []models.MailTransportOperation{models.MailTransportUnknown, models.MailTransportSync, models.MailTransportSend} {
		ids := []uuid.UUID{uuid.New(), uuid.New()}
		for _, id := range ids {
			if _, err := f.pool.Exec(t.Context(), `INSERT INTO email_account_errors(id,email_account_id,user_id,error_code,severity,resolve_method,title,message,created_at,operation,observation_source,task_id)
				VALUES($1,$2,$3,'SERVER_UNREACHABLE','WARNING','RETRY','History','Historical report',$4,$5,'unknown',$6)`, id, f.account, f.user, at, op, f.task); err != nil {
				t.Fatal(err)
			}
		}
		created, err := f.repo.CreateOnce(t.Context(), f.report(op, &at))
		if err != nil || created == nil {
			t.Fatal("history occupied native dedupe", op, err)
		}
		if repeated, err := f.repo.CreateOnce(t.Context(), f.report(op, &at)); err != nil || repeated != nil {
			t.Fatal("native dedupe changed", op, err)
		}
		var source *uuid.UUID
		if op == models.MailTransportSend {
			source = &f.task
		}
		if err := f.repo.RecordTransportSuccess(t.Context(), f.account, op, at.Add(time.Minute), source, f.generation); err != nil {
			t.Fatal(err)
		}
		var preserved int
		if err := f.pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM email_account_errors WHERE id=ANY($1::uuid[]) AND created_at=$2 AND resolved_at IS NULL AND observation_source='unknown' AND task_id=$3`, ids, at, f.task).Scan(&preserved); err != nil || preserved != 2 {
			t.Fatal("recovery altered historical report/task evidence", op, preserved, err)
		}
		var nativeResolved bool
		if err := f.pool.QueryRow(t.Context(), `SELECT resolved_at IS NOT NULL FROM email_account_errors WHERE id=$1`, created.ID).Scan(&nativeResolved); err != nil || nativeResolved != op.Known() {
			t.Fatal("native recovery changed", op, nativeResolved, err)
		}
	}
	if reports := f.errors(t, false); len(reports) != 9 {
		t.Fatal("historical IDs or native rows disappeared", len(reports))
	}
}
