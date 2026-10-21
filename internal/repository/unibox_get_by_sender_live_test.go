package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

// from_addr keeps the display name ("Name <addr>" or "Name (addr)"), so a
// lookup by the bare address has to match on the address part.
// WARMBLY_TEST_DB=postgres://... go test ./internal/repository/ -run LiveUniboxGetBySender -v
func TestLiveUniboxGetBySenderMatchesBareAddress(t *testing.T) {
	handle := liveUniboxFolderDB(t)
	f := newUniboxFolderFixture(t, handle.Pool)
	repo := NewUniboxRepository(handle)
	ctx := context.Background()

	from := func(addr string) uuid.UUID {
		id := f.message(t, repo, models.FolderInbox)
		if _, err := handle.Pool.Exec(ctx, "UPDATE unibox_emails SET from_addr = ARRAY[$2::text] WHERE id = $1", id, addr); err != nil {
			t.Fatalf("set from_addr: %v", err)
		}
		return id
	}
	angle := from("Jane Doe <Jane@Example.com>")
	paren := from("Centous Support (support@centous.com)")
	comment := from("Sam Roe <sam@example.net> (work)")

	cases := []struct {
		sender string
		want   uuid.UUID
	}{
		{"jane@example.com", angle},
		{" Jane@Example.com ", angle},
		{"Jane Doe <Jane@Example.com>", angle},
		{"support@centous.com", paren},
		{"sam@example.net", comment},
		{"work", uuid.Nil},
		{"jane@example.org", uuid.Nil},
	}
	for _, c := range cases {
		got, err := repo.GetBySender(ctx, f.user, c.sender, 10, "")
		if err != nil {
			t.Fatalf("GetBySender(%q): %v", c.sender, err)
		}
		var ids []uuid.UUID
		for _, m := range got.Data {
			ids = append(ids, m.ID)
		}
		if c.want == uuid.Nil && len(ids) != 0 || c.want != uuid.Nil && (len(ids) != 1 || ids[0] != c.want) {
			t.Errorf("GetBySender(%q) = %v, want %v", c.sender, ids, c.want)
		}
	}
}
