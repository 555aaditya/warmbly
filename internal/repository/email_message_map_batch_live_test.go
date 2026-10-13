package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestLiveMessageMapBatchScopesMissingAndNewMappings(t *testing.T) {
	handle := liveUniboxFolderDB(t)
	f := newUniboxFolderFixture(t, handle.Pool)
	other := newUniboxFolderFixture(t, handle.Pool)
	repo := NewEmailMessageMapRepository(handle)
	keys := []string{"batch-known-" + uuid.NewString(), "batch-new-" + uuid.NewString()}
	mapping := EmailMessageData{UserID: f.user.String(), EmailID: f.mailbox.String(), MessageID: keys[0], ID: uuid.NewString(), ThreadID: "preserved-thread"}
	t.Cleanup(func() {
		_, _ = handle.Pool.Exec(context.Background(), `DELETE FROM email_message_map WHERE email_id=ANY($1::uuid[])`, []uuid.UUID{f.mailbox, other.mailbox})
	})
	if err := repo.Add(t.Context(), mapping); err != nil {
		t.Fatal(err)
	}
	got, err := GetEmailMessageMaps(t.Context(), repo, f.user, f.mailbox, keys)
	if err != nil || len(got) != 1 || got[keys[0]].ID != mapping.ID || got[keys[0]].ThreadID != mapping.ThreadID {
		t.Fatalf("mappings=%+v err=%v", got, err)
	}
	for _, scope := range [][2]uuid.UUID{{other.user, f.mailbox}, {f.user, other.mailbox}} {
		if got, err := GetEmailMessageMaps(t.Context(), repo, scope[0], scope[1], keys); err != nil || len(got) != 0 {
			t.Fatalf("cross-scope lookup=%+v err=%v", got, err)
		}
	}
	mapping.MessageID = keys[1]
	if err := repo.Add(t.Context(), mapping); err != nil {
		t.Fatal(err)
	}
	got, err = GetEmailMessageMaps(t.Context(), repo, f.user, f.mailbox, keys)
	if err != nil || len(got) != 2 {
		t.Fatalf("new mapping not found: %+v %v", got, err)
	}
}

func TestLiveMailboxLookupIndexesSupportNormalizedAndGenerationPredicates(t *testing.T) {
	handle := liveUniboxFolderDB(t)
	f := newUniboxFolderFixture(t, handle.Pool)
	t.Cleanup(func() {
		_, _ = handle.Pool.Exec(context.Background(), `DELETE FROM tasks WHERE email_account_id=$1`, f.mailbox)
	})
	if _, err := handle.Pool.Exec(t.Context(), `INSERT INTO tasks(id,task_type,email_account_id,status,message_id)
		SELECT gen_random_uuid(),'warmup',$1,'completed','<' || g || '@lookup-fixture.test>' FROM generate_series(1,10000) g`, f.mailbox); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Pool.Exec(t.Context(), `INSERT INTO unibox_emails(id,user_id,email_id,folder_path,mailbox,uid,message_id)
		SELECT gen_random_uuid(),$1,$2,'Drafts',6,g,'<' || g || '@lookup-fixture.test>' FROM generate_series(1,10000) g`, f.user, f.mailbox); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"tasks", "unibox_emails"} {
		if _, err := handle.Pool.Exec(t.Context(), "ANALYZE "+table); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query, index string
		args         []any
	}{
		{`EXPLAIN (FORMAT JSON) SELECT id FROM tasks WHERE btrim(message_id,'<>')=$1`, "idx_tasks_normalized_message_id", []any{"9999@lookup-fixture.test"}},
		{`EXPLAIN (FORMAT JSON) SELECT uid,id,message_id FROM unibox_emails WHERE user_id=$1 AND email_id=$2 AND folder_path=$3 AND mailbox=$4`, "idx_unibox_emails_folder_generation", []any{f.user, f.mailbox, "Drafts", 7}},
	} {
		var plan string
		if err := handle.Pool.QueryRow(t.Context(), tc.query, tc.args...).Scan(&plan); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, tc.index) {
			t.Fatalf("lookup did not use %s: %s", tc.index, plan)
		}
		t.Logf("lookup uses %s", tc.index)
	}
}

func TestLiveFolderGenerationLookupPreservesIdentityAndLargeMessageIDs(t *testing.T) {
	handle := liveUniboxFolderDB(t)
	f := newUniboxFolderFixture(t, handle.Pool)
	repo := NewEmailSyncStateRepository(handle)
	wanted := uuid.New()
	for _, row := range []struct {
		id         uuid.UUID
		path       string
		generation int
		uid        int
	}{
		{wanted, "Drafts", 7, 101}, {uuid.New(), "Drafts", 6, 101}, {uuid.New(), "INBOX", 7, 101},
	} {
		if _, err := handle.Pool.Exec(t.Context(), `INSERT INTO unibox_emails(id,user_id,email_id,folder_path,mailbox,uid,message_id)VALUES($1,$2,$3,$4,$5,$6,repeat('legacy',1000))`, row.id, f.user, f.mailbox, row.path, row.generation, row.uid); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := repo.ListFolderMessages(t.Context(), f.user, f.mailbox, "Drafts", 7)
	if err != nil || len(rows) != 1 || rows[0].ID != wanted || rows[0].UID != 101 || len(rows[0].MessageID) != 6000 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if rows, err = repo.ListFolderMessages(t.Context(), uuid.New(), f.mailbox, "Drafts", 7); err != nil || len(rows) != 0 {
		t.Fatal("foreign user could read folder")
	}
}
