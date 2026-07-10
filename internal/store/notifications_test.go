package store

import (
	"context"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

// seedUserForNotifications creates a single user and returns its id.
func seedUserForNotifications(t *testing.T, st *Store, email string) string {
	t.Helper()
	var id string
	if err := st.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, nickname) VALUES ($1, $2) RETURNING id`, email, email).
		Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return id
}

// seedProject creates a project owned by ownerID and returns its id.
func seedProjectForNotifications(t *testing.T, st *Store, ownerID, name string) string {
	t.Helper()
	var id string
	if err := st.pool.QueryRow(context.Background(),
		`INSERT INTO projects (name, display_name, owner_id, status) VALUES ($1, $1, $2, 'active') RETURNING id`,
		name, ownerID).Scan(&id); err != nil {
		t.Fatalf("seed project %s: %v", name, err)
	}
	if _, err := st.pool.Exec(context.Background(),
		`INSERT INTO project_members (user_id, project_id, role) VALUES ($1, $2, 'owner')`, ownerID, id); err != nil {
		t.Fatalf("seed project_member: %v", err)
	}
	return id
}

func TestNotifications_CreateGetRoundTrip(t *testing.T) {
	st := openTestStore(t)
	uid := seedUserForNotifications(t, st, "creator@example.com")
	n := &types.Notification{
		Title:        "Hello",
		Body:         "World",
		AudienceType: types.AudienceTypeGlobal,
		CreatedBy:    uid,
	}
	if err := st.CreateNotification(n); err != nil {
		t.Fatalf("create: %v", err)
	}
	if n.ID == "" || n.CreatedAt.IsZero() {
		t.Fatalf("populate id/created_at: %+v", n)
	}
	got, err := st.GetNotification(n.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "Hello" || got.Body != "World" || got.AudienceType != "global" || got.AudienceID != nil {
		t.Fatalf("unexpected: %+v", got)
	}
	if got.ReadCount != 0 {
		t.Fatalf("read_count = %d, want 0", got.ReadCount)
	}
}

func TestNotifications_VisibilityUnion(t *testing.T) {
	st := openTestStore(t)
	me := seedUserForNotifications(t, st, "me@example.com")
	other := seedUserForNotifications(t, st, "other@example.com")
	myProj := seedProjectForNotifications(t, st, me, "myproj")
	otherProj := seedProjectForNotifications(t, st, other, "otherproj")

	// visible: global, my-project, addressed-to-me
	must := func(n *types.Notification) string {
		if err := st.CreateNotification(n); err != nil {
			t.Fatalf("create: %v", err)
		}
		return n.ID
	}
	visGlobal := must(&types.Notification{Title: "g", Body: "b", AudienceType: types.AudienceTypeGlobal, CreatedBy: me})
	proj := myProj
	visProj := must(&types.Notification{Title: "p", Body: "b", AudienceType: types.AudienceTypeProject, AudienceID: &proj, CreatedBy: me})
	meAud := me
	visUser := must(&types.Notification{Title: "u", Body: "b", AudienceType: types.AudienceTypeUser, AudienceID: &meAud, CreatedBy: me})

	// invisible: other project, addressed to other user
	op := otherProj
	must(&types.Notification{Title: "op", Body: "b", AudienceType: types.AudienceTypeProject, AudienceID: &op, CreatedBy: me})
	ou := other
	must(&types.Notification{Title: "ou", Body: "b", AudienceType: types.AudienceTypeUser, AudienceID: &ou, CreatedBy: me})

	list, total, err := st.ListVisibleForUser(me, types.DefaultPagination())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	wantIDs := map[string]bool{visGlobal: false, visProj: false, visUser: false}
	for _, item := range list {
		if _, ok := wantIDs[item.ID]; !ok {
			t.Fatalf("unexpected id %s in list", item.ID)
		}
		wantIDs[item.ID] = true
	}
	for id, seen := range wantIDs {
		if !seen {
			t.Fatalf("missing id %s in list", id)
		}
	}

	count, err := st.CountUnreadForUser(me)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Fatalf("unread = %d, want 3", count)
	}
}

func TestNotifications_MarkReadIdempotentAndInvisibleSilent(t *testing.T) {
	st := openTestStore(t)
	me := seedUserForNotifications(t, st, "me2@example.com")
	other := seedUserForNotifications(t, st, "other2@example.com")

	// Visible: global. Invisible: addressed to `other`.
	g := &types.Notification{Title: "g", Body: "b", AudienceType: types.AudienceTypeGlobal, CreatedBy: me}
	if err := st.CreateNotification(g); err != nil {
		t.Fatal(err)
	}
	ou := other
	inv := &types.Notification{Title: "u", Body: "b", AudienceType: types.AudienceTypeUser, AudienceID: &ou, CreatedBy: me}
	if err := st.CreateNotification(inv); err != nil {
		t.Fatal(err)
	}

	if err := st.MarkNotificationRead(me, g.ID); err != nil {
		t.Fatalf("first mark: %v", err)
	}
	if err := st.MarkNotificationRead(me, g.ID); err != nil {
		t.Fatalf("second mark (idempotent): %v", err)
	}
	// Invisible must silently no-op — no error, no row inserted.
	if err := st.MarkNotificationRead(me, inv.ID); err != nil {
		t.Fatalf("invisible mark should be silent nil, got: %v", err)
	}
	var inserted int
	st.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM notification_reads WHERE user_id = $1 AND notification_id = $2`,
		me, inv.ID).Scan(&inserted)
	if inserted != 0 {
		t.Fatalf("invisible notification silently inserted a read row (%d)", inserted)
	}

	// Unread count after marking the only visible one = 0.
	c, err := st.CountUnreadForUser(me)
	if err != nil {
		t.Fatal(err)
	}
	if c != 0 {
		t.Fatalf("unread = %d, want 0", c)
	}
}

func TestNotifications_SoftDeleteHidesFromUser(t *testing.T) {
	st := openTestStore(t)
	me := seedUserForNotifications(t, st, "me3@example.com")

	n := &types.Notification{Title: "g", Body: "b", AudienceType: types.AudienceTypeGlobal, CreatedBy: me}
	if err := st.CreateNotification(n); err != nil {
		t.Fatal(err)
	}
	if err := st.SoftDeleteNotification(n.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, total, err := st.ListVisibleForUser(me, types.DefaultPagination())
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0 after soft delete", total)
	}

	// Admin default (includeDeleted=false) also excludes it.
	_, total, err = st.ListAllNotifications(false, "", types.DefaultPagination())
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("admin alive total = %d, want 0", total)
	}
	// includeDeleted=true includes it.
	_, total, err = st.ListAllNotifications(true, "", types.DefaultPagination())
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("admin all total = %d, want 1", total)
	}
}
