package wmail

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type imapBatchMessageMap struct {
	fakeMessageMap
	batches, gets int
	err           error
}

func (r *imapBatchMessageMap) GetMany(_ context.Context, userID, emailID uuid.UUID, ids []string) (map[string]*repository.EmailMessageData, error) {
	r.batches++
	result := make(map[string]*repository.EmailMessageData, len(ids))
	for _, id := range ids {
		result[id] = &repository.EmailMessageData{UserID: userID.String(), EmailID: emailID.String(), MessageID: id, ID: uuid.NewString(), ThreadID: "thread"}
	}
	return result, r.err
}

func (r *imapBatchMessageMap) Get(context.Context, uuid.UUID, uuid.UUID, string) (*repository.EmailMessageData, error) {
	r.gets++
	return nil, nil
}

func TestImapApplyBatchUsesOneLookupAndHoldsOnControlPlaneFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			conn := &fakeImapConn{}
			w, events := newIMAPTestMail(conn, &fixedBudget{allow: 200}, &models.Mailbox{Name: "INBOX"})
			repo := &imapBatchMessageMap{}
			if fail {
				repo.err = &repository.ControlPlaneHTTPError{Status: 503}
			}
			w.EmailMessageMapRepository = repo
			fetched, err := conn.FetchEnvelopes(t.Context(), uidRange(200))
			if err != nil {
				t.Fatal(err)
			}
			stats := &tickStats{}
			done, mailErr := w.imapApply(t.Context(), fetched, false, stats, nil, 0)
			if mailErr != nil || done == fail || repo.batches != 1 || repo.gets != 0 || stats.aborted != fail {
				t.Fatalf("done=%t err=%v batch=%d gets=%d aborted=%t", done, mailErr, repo.batches, repo.gets, stats.aborted)
			}
			want := 200
			if fail {
				want = 0
			}
			if len(*events) != want {
				t.Fatalf("updates=%d want=%d", len(*events), want)
			}
		})
	}
}
