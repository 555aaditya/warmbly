package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

// A unibox forward reaches a suppressed domain; a reply and a non-manual lane
// to the same domain are refused, naming the address.
func TestLiveSuppressionExemptsOnlyForwards(t *testing.T) {
	f, r := lineageFixture(t)
	ctx := t.Context()
	worker := uuid.New()
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, q, a...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO fleet_nodes(id,role,active,last_seen_at,warmup_send_protocol)VALUES($1,'worker',true,NOW(),2)`, worker)
	exec(`INSERT INTO workers(id)VALUES($1)`, worker)
	t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM fleet_nodes WHERE id=$1`, worker) })
	exec(`UPDATE email_accounts SET worker_id=$1 WHERE id=$2`, worker, f.sender)
	exec(`INSERT INTO suppressed_recipients(organization_id,email,kind,reason,source)VALUES($1,'partner.test','domain','client list','import')`, f.org)

	const address = "lea@partner.test"
	reservation := func(lane string, forward bool) OutboundReservation {
		id := uuid.New()
		exec(`INSERT INTO tasks(id,task_type,email_account_id,status,message_id)VALUES($1,$2,$3,'active','')`, id, lane, f.sender)
		if lane == "email" {
			fwd := ""
			if forward {
				fwd = "---------- Forwarded message ---------"
			}
			exec(`INSERT INTO email_tasks(task_id,to_addrs,subject,body,forwarded_plain)VALUES($1,$2,'Lead','',$3)`, id, []string{address}, fwd)
		}
		return OutboundReservation{TaskID: id, MailboxID: f.sender, OrganizationID: f.org, WorkerID: worker, Provider: models.InboxProviderSMTPIMAP, Recipients: []string{address, "teammate@example.test"}}
	}

	in := reservation("email", true)
	nonce, err := r.ReserveOutbound(ctx, in)
	if err != nil {
		t.Fatal("forward to a suppressed domain refused", err)
	}
	state, err := r.BeginOutbound(ctx, in.TaskID, in.MailboxID, in.WorkerID, nonce)
	if err != nil || state.State != "execute" {
		t.Fatal("forward refused at execution", state, err)
	}
	result := models.SendEmailResult{TaskID: in.TaskID, Success: true, MessageID: "<fwd@example.test>"}
	if err = r.FinishOutbound(ctx, in.TaskID, in.MailboxID, in.WorkerID, result); err != nil {
		t.Fatal(err)
	}
	if err = r.ApplySendResult(ctx, result, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}

	for _, lane := range []string{"email", "placement"} {
		_, err := r.ReserveOutbound(ctx, reservation(lane, false))
		var suppressed *RecipientSuppressedError
		if !errors.As(err, &suppressed) || suppressed.Address != address || !errors.Is(err, ErrSendAdmissionDenied) {
			t.Fatalf("%s to a suppressed domain: %v", lane, err)
		}
		if SendAdmissionCode(err) != "RECIPIENT_SUPPRESSED" {
			t.Fatalf("%s code %q", lane, SendAdmissionCode(err))
		}
	}
}
