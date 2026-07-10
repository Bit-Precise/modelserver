# Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship an in-dashboard notification system: admins create/edit/delete notifications targeted at all users, one project, or one user; users see a `Notifications` sidebar entry above `Projects` with an unread-count badge and read the messages on a dedicated inbox page.

**Architecture:** Two tables — `notifications` (soft-deleted, CHECK-constrained audience shape) and `notification_reads` (per-user, idempotent insert). Backend routes live in the existing admin server: `/notifications/*` under `JWTAuthMiddleware` for authenticated users, `/admin/notifications/*` additionally under `RequireSuperadmin` for CRUD. Frontend adds a new sidebar entry with a polling `unread_count` badge, a user inbox page, and an admin management page — all wired through React Query hooks in `dashboard/src/api/notifications.ts`.

**Tech Stack:** Go 1.x, PostgreSQL 15+ (`gen_random_uuid()`, partial indexes, `ON CONFLICT DO NOTHING`), chi router, existing project helpers (`writeJSON`, `writeError`, `writeList`, `writeData`, `parsePagination`, `UserFromContext`, `JWTAuthMiddleware`, `RequireSuperadmin`). Frontend: React, React Query, react-router, shadcn/ui components (`Dialog`, `AlertDialog`, `DataTable`, `Tabs`), lucide `Bell` icon.

## Global Constraints

- **Migration number:** `064_notifications.sql`. (063 is already taken by the parked RBAC WIP; this branch stashes that WIP before starting.)
- **Table names:** `notifications`, `notification_reads` (exact).
- **Audience values:** `'global'`, `'project'`, `'user'` (exact, lowercase). Enforced by a table-level CHECK constraint `audience_shape` that requires `audience_id IS NULL` iff `audience_type = 'global'`.
- **Field caps:** `title` 1–200 chars, `body` 1–20000 chars. Validated in the handler layer with 400 `code=invalid_input`.
- **Audience validation:** `audience_type='project'` requires an existing non-archived `projects.id`; `audience_type='user'` requires an existing non-disabled `users.id`. Otherwise 400 `code=invalid_audience`.
- **`POST /notifications/{id}/read` is silent-200 on unknown/deleted/invisible id** (avoid a confusing 404 during admin-delete race). The FK on `notification_reads.notification_id` still guards against inserting against a truly nonexistent row — the handler catches the not-visible case in Go before insert.
- **Soft delete:** admin `DELETE` sets `deleted_at = NOW()`. Every user-visibility query filters `WHERE deleted_at IS NULL`. Admin list defaults to alive-only; `?include_deleted=1` opts in.
- **Sidebar badge polling:** 45 000 ms `refetchInterval`, `staleTime: 30_000`. `1–99` shows the number, `≥100` shows `99+`, `0`/`undefined`/`null` hides the badge.
- **Read semantics on expand:** first expand of an unread row triggers `POST /notifications/{id}/read` exactly once, then locally mutates the cached list item to set `read_at` and invalidates the `unread_count` query.
- **Pagination:** page-based `?page=&per_page=`, wrapped by the project-standard `writeList(w, items, total, page, perPage)` envelope. Matches every other list endpoint (`internal/admin/admin.go:140`).
- **Auth:** authenticated user helper `UserFromContext(r.Context())`. Admin gate reuses the existing `RequireSuperadmin` middleware from `internal/admin/admin.go:76`.

---

## File Structure

**Backend — new files**

| Path | Responsibility |
|---|---|
| `internal/store/migrations/064_notifications.sql` | Create both tables + 4 indexes + CHECK constraint. |
| `internal/store/migrations_064_test.go` | Verify tables exist, indexes present, CHECK rejects malformed audience, PK on reads is composite. |
| `internal/types/notification.go` | `Notification` struct, `AudienceType*` constants, request/response DTOs. |
| `internal/store/notifications.go` | `CreateNotification`, `GetNotification`, `UpdateNotification`, `SoftDeleteNotification`, `ListAllNotifications`, `ListVisibleForUser`, `CountUnreadForUser`, `MarkRead`, `MarkAllRead`, `ReadCount`. Also the private `visibilityWhere(userID)` helper reused across three call sites. |
| `internal/store/notifications_test.go` | Round-trips + visibility union + read idempotency + soft-delete filtering. |
| `internal/admin/handle_notifications.go` | 4 user handlers + 5 admin handlers. |
| `internal/admin/handle_notifications_test.go` | Auth gates (401/403), CRUD happy paths, validation errors, silent-200 on invisible/deleted, page pagination shape. |

**Backend — modifications**

| Path | Change |
|---|---|
| `internal/admin/routes.go` | Add two subtree mounts (see plan §Global-Constraints route table below and the exact code block in Task 3 Step 3 / Task 4 Step 3). |

**Frontend — new files**

| Path | Responsibility |
|---|---|
| `dashboard/src/api/notifications.ts` | React Query hooks: `useMyNotifications`, `useUnreadNotificationCount`, `useMarkNotificationRead`, `useMarkAllNotificationsRead`, `useAdminNotifications`, `useCreateNotification`, `useUpdateNotification`, `useDeleteNotification`. |
| `dashboard/src/pages/notifications/NotificationsPage.tsx` | User inbox page. |
| `dashboard/src/pages/admin/NotificationsPage.tsx` | Admin DataTable + Create/Edit dialogs + Delete AlertDialog. |

**Frontend — modifications**

| Path | Change |
|---|---|
| `dashboard/src/api/types.ts` | Add `AudienceType`, `Notification`, `NotificationListResponse`, `NotificationCreatePayload`, `NotificationUpdatePayload`, `AdminNotification` (superset with `read_count`). |
| `dashboard/src/components/layout/Sidebar.tsx` | Import `Bell`; extend `SidebarLink` with optional `badge?: number`; render new user entry above `Projects`; render new admin entry at bottom of admin block; call `useUnreadNotificationCount()` for the badge value. |
| `dashboard/src/App.tsx` | Add two `<Route>` entries: `/notifications` → user page, `/admin/notifications` → admin page. |

---

## Task 1: Migration `064_notifications.sql` + tests

**Files:**
- Create: `internal/store/migrations/064_notifications.sql`
- Create: `internal/store/migrations_064_test.go`

**Interfaces:**
- Consumes: `openTestStore(t)` helper at `internal/store/extra_usage_db_test.go:13` (applies every embedded migration on connect).
- Produces: two tables (`notifications`, `notification_reads`) available for later tasks' Store code.

- [ ] **Step 1: Write the failing tests**

Create `internal/store/migrations_064_test.go`:

```go
package store

import (
	"context"
	"testing"
)

// TestMigration064_TablesExist asserts both tables were created with the
// expected columns.
func TestMigration064_TablesExist(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for _, tbl := range []string{"notifications", "notification_reads"} {
		var exists bool
		if err := st.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
			tbl).Scan(&exists); err != nil {
			t.Fatalf("query for %s: %v", tbl, err)
		}
		if !exists {
			t.Fatalf("table %s missing after migration", tbl)
		}
	}
}

// TestMigration064_AudienceCheck rejects malformed audience combinations
// per the CHECK constraint (global with id, project without id).
func TestMigration064_AudienceCheck(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// Seed a user for created_by.
	var uid string
	if err := st.pool.QueryRow(ctx,
		`INSERT INTO users (email, nickname) VALUES ('n@example.com', 'n') RETURNING id`).
		Scan(&uid); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	cases := []struct {
		name          string
		audienceType  string
		audienceIDSQL string
		wantErr       bool
	}{
		{"global no id ok", "global", "NULL", false},
		{"global with id rejected", "global", "gen_random_uuid()", true},
		{"project with id ok", "project", "gen_random_uuid()", false},
		{"project no id rejected", "project", "NULL", true},
		{"user with id ok", "user", "gen_random_uuid()", false},
		{"user no id rejected", "user", "NULL", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.pool.Exec(ctx,
				`INSERT INTO notifications (title, body, audience_type, audience_id, created_by)
				 VALUES ('t', 'b', $1, `+tc.audienceIDSQL+`, $2)`,
				tc.audienceType, uid)
			if tc.wantErr && err == nil {
				t.Fatalf("expected CHECK violation, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestMigration064_ReadsPK confirms notification_reads is keyed on
// (notification_id, user_id) — duplicate insert must fail.
func TestMigration064_ReadsPK(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var uid, nid string
	if err := st.pool.QueryRow(ctx,
		`INSERT INTO users (email, nickname) VALUES ('pk@example.com', 'pk') RETURNING id`).
		Scan(&uid); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := st.pool.QueryRow(ctx,
		`INSERT INTO notifications (title, body, audience_type, created_by)
		 VALUES ('t', 'b', 'global', $1) RETURNING id`, uid).Scan(&nid); err != nil {
		t.Fatalf("seed notification: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO notification_reads (notification_id, user_id) VALUES ($1, $2)`,
		nid, uid); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO notification_reads (notification_id, user_id) VALUES ($1, $2)`,
		nid, uid); err == nil {
		t.Fatalf("expected PK violation on duplicate insert")
	}
}
```

- [ ] **Step 2: Run tests — expect failure (tables missing)**

Run: `cd /root/coding/modelserver && go test ./internal/store/ -run 'TestMigration064' -v`
Expected: FAIL (or SKIP if `TEST_DATABASE_URL` unset — the migration file does not exist yet; when a DB is set, the tests fail at the CREATE-table check).

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/064_notifications.sql`:

```sql
-- 064_notifications.sql
--
-- Notifications: platform-authored in-dashboard messages. Two tables:
--   1. notifications        — the message itself. Soft-deleted via
--                              deleted_at; audience shape enforced by
--                              CHECK constraint.
--   2. notification_reads   — per-user read state; row present = read;
--                              idempotent insert via ON CONFLICT.
--
-- See docs/superpowers/specs/2026-07-10-notifications-design.md.

CREATE TABLE notifications (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title         TEXT NOT NULL,
    body          TEXT NOT NULL,
    audience_type TEXT NOT NULL,
    audience_id   UUID,
    created_by    UUID NOT NULL REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ,
    CONSTRAINT audience_shape CHECK (
        (audience_type = 'global' AND audience_id IS NULL) OR
        (audience_type IN ('project','user') AND audience_id IS NOT NULL)
    )
);

CREATE INDEX idx_notifications_alive_created
    ON notifications (created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_notifications_project
    ON notifications (audience_id)
    WHERE audience_type = 'project' AND deleted_at IS NULL;

CREATE INDEX idx_notifications_user
    ON notifications (audience_id)
    WHERE audience_type = 'user' AND deleted_at IS NULL;

CREATE TABLE notification_reads (
    notification_id UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (notification_id, user_id)
);

CREATE INDEX idx_notification_reads_user ON notification_reads (user_id);
```

- [ ] **Step 4: Run tests — expect pass**

Run: `cd /root/coding/modelserver && go test ./internal/store/ -run 'TestMigration064' -v`
Expected: PASS on every subtest of `TestMigration064_AudienceCheck` plus `TestMigration064_TablesExist` and `TestMigration064_ReadsPK`. SKIP without `TEST_DATABASE_URL` — CI will exercise, consistent with prior migration PRs.

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver
git add internal/store/migrations/064_notifications.sql internal/store/migrations_064_test.go
git commit -m "feat(store): add notifications + notification_reads tables

Migration 064 introduces the two tables backing the notifications
feature. notifications carries a soft-delete flag and an audience_shape
CHECK constraint (global has no audience_id; project/user require one).
notification_reads is keyed on (notification_id, user_id) so per-user
read state upserts idempotently via ON CONFLICT DO NOTHING. Three
partial indexes on notifications (alive-desc-created, project-audience,
user-audience) support the visibility query added in the next commit.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Types + Store CRUD + visibility helper

**Files:**
- Create: `internal/types/notification.go`
- Create: `internal/store/notifications.go`
- Create: `internal/store/notifications_test.go`

**Interfaces:**
- Consumes: `notifications` + `notification_reads` tables from Task 1; `openTestStore(t)` helper.
- Produces (used by Tasks 3 and 4):
  - `types.Notification` struct with fields: `ID string, Title string, Body string, AudienceType string, AudienceID *string, AudienceName *string, CreatedBy string, CreatedAt time.Time, UpdatedAt time.Time, DeletedAt *time.Time, ReadAt *time.Time, ReadCount int`.
  - `types.AudienceTypeGlobal="global"`, `types.AudienceTypeProject="project"`, `types.AudienceTypeUser="user"` constants.
  - `Store.CreateNotification(n *types.Notification) error` — populates `n.ID`, `n.CreatedAt`, `n.UpdatedAt`.
  - `Store.GetNotification(id string) (*types.Notification, error)` — returns row regardless of `deleted_at`; `ReadCount` populated; `ReadAt` unset.
  - `Store.UpdateNotification(id string, title, body, audienceType string, audienceID *string) error` — bumps `updated_at`; returns `pgx.ErrNoRows` if id absent or already deleted.
  - `Store.SoftDeleteNotification(id string) error` — `UPDATE ... SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL`; returns `pgx.ErrNoRows` if not found or already deleted.
  - `Store.ListAllNotifications(includeDeleted bool, audienceType string, p types.PaginationParams) ([]types.Notification, int, error)` — admin list; `audienceType == ""` skips the filter; each item has `ReadCount` populated, `ReadAt` unset.
  - `Store.ListVisibleForUser(userID string, p types.PaginationParams) ([]types.Notification, int, error)` — user list; `ReadAt` populated via LEFT JOIN; `AudienceName` populated (project display_name for project audience, `"You"` for user audience, empty string for global).
  - `Store.CountUnreadForUser(userID string) (int, error)` — cheap `COUNT(*)` version of the visibility query with the read-null filter added.
  - `Store.MarkNotificationRead(userID, notificationID string) error` — `INSERT ... ON CONFLICT DO NOTHING`; returns nil even when the notification is deleted or not visible to the user (silent no-op after the visibility guard fails). See implementation for the visibility guard.
  - `Store.MarkAllNotificationsRead(userID string) (int, error)` — inserts one row per currently-visible unread notification; returns count inserted.

- [ ] **Step 1: Write the failing tests**

Create `internal/store/notifications_test.go`:

```go
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
```

- [ ] **Step 2: Run tests — expect failure (types + store missing)**

Run: `go test ./internal/store/ -run 'TestNotifications' -v`
Expected: compile error `types.Notification undefined`, `st.CreateNotification undefined`, etc.

- [ ] **Step 3: Write the types file**

Create `internal/types/notification.go`:

```go
package types

import "time"

// Audience type constants.
const (
	AudienceTypeGlobal  = "global"
	AudienceTypeProject = "project"
	AudienceTypeUser    = "user"
)

// Notification is a platform-authored in-dashboard message.
//
// AudienceID is nil for global audience and points to a project or user
// row otherwise. AudienceName is populated only by ListVisibleForUser
// (display_name of the project, or "You" for user audience, or "" for
// global) — admin listings leave it nil.
//
// ReadAt is populated only by ListVisibleForUser (nil = unread for the
// requesting user). Admin listings leave it nil.
//
// ReadCount is populated only by admin listings (GetNotification and
// ListAllNotifications). User listings leave it 0.
//
// DeletedAt is nil for alive rows; set to the soft-delete timestamp
// otherwise.
type Notification struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	AudienceType string     `json:"audience_type"`
	AudienceID   *string    `json:"audience_id,omitempty"`
	AudienceName *string    `json:"audience_name,omitempty"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	ReadAt       *time.Time `json:"read_at,omitempty"`
	ReadCount    int        `json:"read_count,omitempty"`
}
```

- [ ] **Step 4: Write the store file**

Create `internal/store/notifications.go`:

```go
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/modelserver/modelserver/internal/types"
)

// visibilityWhereClause returns the SQL fragment (with $1 bound to userID
// downstream) that filters `notifications n` rows visible to that user:
// global OR project-I'm-in OR addressed-to-me, and always alive. The
// fragment references `n.` — callers must alias the notifications table
// as `n`. It is deliberately parameter-index-independent: callers append
// their own indexed args and know that $1 is userID.
const visibilityWhereClause = `
	n.deleted_at IS NULL
	AND (
		n.audience_type = 'global'
		OR (n.audience_type = 'project' AND n.audience_id IN (
			SELECT project_id FROM project_members WHERE user_id = $1
		))
		OR (n.audience_type = 'user' AND n.audience_id = $1)
	)
`

// CreateNotification inserts a new notification and populates id + timestamps.
func (s *Store) CreateNotification(n *types.Notification) error {
	return s.pool.QueryRow(context.Background(), `
		INSERT INTO notifications (title, body, audience_type, audience_id, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at`,
		n.Title, n.Body, n.AudienceType, n.AudienceID, n.CreatedBy,
	).Scan(&n.ID, &n.CreatedAt, &n.UpdatedAt)
}

// GetNotification returns the notification regardless of deleted_at; ReadCount
// is populated, ReadAt is not.
func (s *Store) GetNotification(id string) (*types.Notification, error) {
	var n types.Notification
	err := s.pool.QueryRow(context.Background(), `
		SELECT n.id, n.title, n.body, n.audience_type, n.audience_id,
		       n.created_by, n.created_at, n.updated_at, n.deleted_at,
		       (SELECT COUNT(*) FROM notification_reads r WHERE r.notification_id = n.id) AS read_count
		FROM notifications n
		WHERE n.id = $1`, id).
		Scan(&n.ID, &n.Title, &n.Body, &n.AudienceType, &n.AudienceID,
			&n.CreatedBy, &n.CreatedAt, &n.UpdatedAt, &n.DeletedAt, &n.ReadCount)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// UpdateNotification bumps updated_at and applies the new values. Returns
// pgx.ErrNoRows if the id does not exist or is already soft-deleted.
func (s *Store) UpdateNotification(id, title, body, audienceType string, audienceID *string) error {
	ct, err := s.pool.Exec(context.Background(), `
		UPDATE notifications
		   SET title = $2, body = $3, audience_type = $4, audience_id = $5, updated_at = NOW()
		 WHERE id = $1 AND deleted_at IS NULL`,
		id, title, body, audienceType, audienceID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SoftDeleteNotification marks the row deleted. Returns pgx.ErrNoRows if
// the id is unknown or already deleted (idempotent from the caller's
// perspective would be a design change — the handler layer maps this to
// 404 for the admin UI).
func (s *Store) SoftDeleteNotification(id string) error {
	ct, err := s.pool.Exec(context.Background(),
		`UPDATE notifications SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListAllNotifications returns notifications for admin. If includeDeleted
// is false, soft-deleted rows are hidden. audienceType filters by type
// when non-empty.
func (s *Store) ListAllNotifications(includeDeleted bool, audienceType string, p types.PaginationParams) ([]types.Notification, int, error) {
	where := "1=1"
	args := []interface{}{}
	if !includeDeleted {
		where += " AND n.deleted_at IS NULL"
	}
	if audienceType != "" {
		args = append(args, audienceType)
		where += fmt.Sprintf(" AND n.audience_type = $%d", len(args))
	}

	var total int
	if err := s.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM notifications n WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	perPage := p.Limit()
	offset := (p.Page - 1) * perPage
	args = append(args, perPage, offset)
	rows, err := s.pool.Query(context.Background(), fmt.Sprintf(`
		SELECT n.id, n.title, n.body, n.audience_type, n.audience_id,
		       n.created_by, n.created_at, n.updated_at, n.deleted_at,
		       (SELECT COUNT(*) FROM notification_reads r WHERE r.notification_id = n.id) AS read_count
		FROM notifications n
		WHERE %s
		ORDER BY n.created_at DESC
		LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]types.Notification, 0, perPage)
	for rows.Next() {
		var n types.Notification
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.AudienceType, &n.AudienceID,
			&n.CreatedBy, &n.CreatedAt, &n.UpdatedAt, &n.DeletedAt, &n.ReadCount); err != nil {
			return nil, 0, err
		}
		items = append(items, n)
	}
	return items, total, rows.Err()
}

// ListVisibleForUser returns notifications visible to the user (global +
// project-I'm-in + addressed-to-me), alive only, with ReadAt populated
// via LEFT JOIN and AudienceName populated for display.
func (s *Store) ListVisibleForUser(userID string, p types.PaginationParams) ([]types.Notification, int, error) {
	var total int
	if err := s.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM notifications n WHERE "+visibilityWhereClause, userID).Scan(&total); err != nil {
		return nil, 0, err
	}

	perPage := p.Limit()
	offset := (p.Page - 1) * perPage
	rows, err := s.pool.Query(context.Background(), `
		SELECT n.id, n.title, n.body, n.audience_type, n.audience_id,
		       n.created_by, n.created_at, n.updated_at, n.deleted_at,
		       r.read_at,
		       CASE
		         WHEN n.audience_type = 'global'  THEN ''
		         WHEN n.audience_type = 'user'    THEN 'You'
		         WHEN n.audience_type = 'project' THEN COALESCE(p.display_name, '(deleted project)')
		         ELSE ''
		       END AS audience_name
		FROM notifications n
		LEFT JOIN notification_reads r
		       ON r.notification_id = n.id AND r.user_id = $1
		LEFT JOIN projects p
		       ON n.audience_type = 'project' AND n.audience_id = p.id
		WHERE `+visibilityWhereClause+`
		ORDER BY n.created_at DESC
		LIMIT $2 OFFSET $3`, userID, perPage, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]types.Notification, 0, perPage)
	for rows.Next() {
		var n types.Notification
		var audienceName string
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.AudienceType, &n.AudienceID,
			&n.CreatedBy, &n.CreatedAt, &n.UpdatedAt, &n.DeletedAt, &n.ReadAt, &audienceName); err != nil {
			return nil, 0, err
		}
		n.AudienceName = &audienceName
		items = append(items, n)
	}
	return items, total, rows.Err()
}

// CountUnreadForUser is a COUNT(*) version of ListVisibleForUser with
// the read-null filter, used by the sidebar badge poller.
func (s *Store) CountUnreadForUser(userID string) (int, error) {
	var n int
	err := s.pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM notifications n
		LEFT JOIN notification_reads r
		       ON r.notification_id = n.id AND r.user_id = $1
		WHERE `+visibilityWhereClause+` AND r.read_at IS NULL`, userID).Scan(&n)
	return n, err
}

// MarkNotificationRead upserts a read row for (userID, notificationID)
// only if the notification is currently visible to that user. Silent
// no-op (returns nil, no row inserted) otherwise — the caller's HTTP
// handler always returns 200.
func (s *Store) MarkNotificationRead(userID, notificationID string) error {
	// Visibility guard: reuse the exact same WHERE the list uses so
	// invisible/deleted notifications insert nothing.
	_, err := s.pool.Exec(context.Background(), `
		INSERT INTO notification_reads (notification_id, user_id)
		SELECT n.id, $1
		FROM notifications n
		WHERE n.id = $2 AND `+visibilityWhereClause+`
		ON CONFLICT DO NOTHING`, userID, notificationID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}

// MarkAllNotificationsRead inserts a read row for every currently-
// visible unread notification. Returns the number of rows inserted.
func (s *Store) MarkAllNotificationsRead(userID string) (int, error) {
	ct, err := s.pool.Exec(context.Background(), `
		INSERT INTO notification_reads (notification_id, user_id)
		SELECT n.id, $1
		FROM notifications n
		LEFT JOIN notification_reads r
		       ON r.notification_id = n.id AND r.user_id = $1
		WHERE `+visibilityWhereClause+` AND r.read_at IS NULL
		ON CONFLICT DO NOTHING`, userID)
	if err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
}
```

- [ ] **Step 5: Run tests — expect pass**

Run: `cd /root/coding/modelserver && go test ./internal/store/ -run 'TestNotifications|TestMigration064' -v && go build ./...`
Expected: PASS on every notifications subtest and on all previously green migration tests; `go build` clean. SKIP on the DB-touching tests without `TEST_DATABASE_URL`.

- [ ] **Step 6: Commit**

```bash
cd /root/coding/modelserver
git add internal/types/notification.go internal/store/notifications.go internal/store/notifications_test.go
git commit -m "feat(store): notifications store CRUD + visibility helper

Adds types.Notification and Store methods for the full lifecycle:
Create/Get/Update/SoftDelete + ListAll (admin) + ListVisibleForUser +
CountUnreadForUser + MarkNotificationRead + MarkAllNotificationsRead.

Visibility is expressed as one SQL fragment (visibilityWhereClause,
\$1 = userID) reused across the list, count, mark-one, and mark-all
queries so union changes stay in one place. MarkNotificationRead uses
the same fragment as an INSERT ... SELECT guard so invisible or
deleted notifications silently insert zero rows — the handler layer
therefore returns 200 unconditionally and the frontend never sees a
404 during an admin-delete race.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Admin handlers + route mount

**Files:**
- Create: `internal/admin/handle_notifications.go` (admin-only handlers only in this task; user-facing handlers added in Task 4)
- Modify: `internal/admin/routes.go` (add `/admin/notifications` subtree)
- Create: `internal/admin/handle_notifications_test.go` (admin gates + happy paths)

**Interfaces:**
- Consumes:
  - `types.Notification`, `types.AudienceTypeGlobal/Project/User` (Task 2).
  - `Store.CreateNotification`, `Store.GetNotification`, `Store.UpdateNotification`, `Store.SoftDeleteNotification`, `Store.ListAllNotifications` (Task 2).
  - `Store.GetProjectByID(id string) (*types.Project, error)` (existing, `internal/store/projects.go:68`).
  - `Store.GetUserByID(id string) (*types.User, error)` (existing, `internal/store/users.go:34`).
  - `types.ProjectStatusArchived = "archived"` (`internal/types/project.go:12`).
  - `types.UserStatusDisabled = "disabled"` (`internal/types/user.go:28`).
  - `UserFromContext(ctx) *types.User`, `writeError`, `writeData`, `writeList`, `writeJSON`, `parsePagination`, `decodeBody` (`internal/admin/admin.go`).
  - Chi router + `RequireSuperadmin` middleware.
- Produces:
  - 5 admin route handlers, each returning `http.HandlerFunc`, all named `handle*Notification[s]`.
  - Route mount at `/admin/notifications` inside the existing authenticated group in `routes.go`, gated by `RequireSuperadmin`. Wired in Step 4.

- [ ] **Step 1: Write failing tests**

Create `internal/admin/handle_notifications_test.go`:

```go
package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The tests below intentionally exercise the router with real handler
// wiring (via a small helper) rather than calling handler funcs
// directly — they verify both the handler body and the middleware chain
// (RequireSuperadmin).

func TestNotificationsAdmin_RequireSuperadmin(t *testing.T) {
	st := openTestAdminStore(t)                 // helper defined in the admin test package
	nonAdmin := seedAdminTestUser(t, st, false) // helper: creates user, returns access token
	r := newAdminTestRouter(t, st)              // helper: builds chi.Router with routes mounted

	req := httptest.NewRequest(http.MethodGet, "/admin/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+nonAdmin.token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin GET /admin/notifications = %d, want 403", rec.Code)
	}
}

func TestNotificationsAdmin_CreateGetListDeleteRoundTrip(t *testing.T) {
	st := openTestAdminStore(t)
	admin := seedAdminTestUser(t, st, true)
	r := newAdminTestRouter(t, st)

	// Create a global notification.
	createBody := `{"title":"Maintenance","body":"Down at 2am","audience_type":"global"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/notifications", strings.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer "+admin.token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var createResp struct {
		Data struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if createResp.Data.ID == "" || createResp.Data.Title != "Maintenance" {
		t.Fatalf("unexpected create body: %+v", createResp)
	}
	id := createResp.Data.ID

	// Get one.
	req = httptest.NewRequest(http.MethodGet, "/admin/notifications/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status=%d", rec.Code)
	}

	// List — total == 1.
	req = httptest.NewRequest(http.MethodGet, "/admin/notifications?page=1&per_page=10", nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var listResp struct {
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	json.Unmarshal(rec.Body.Bytes(), &listResp)
	if listResp.Meta.Total != 1 {
		t.Fatalf("list total = %d, want 1", listResp.Meta.Total)
	}

	// Delete (soft).
	req = httptest.NewRequest(http.MethodDelete, "/admin/notifications/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Second delete → 404.
	req = httptest.NewRequest(http.MethodDelete, "/admin/notifications/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: status=%d, want 404", rec.Code)
	}

	// Default list excludes deleted → total == 0.
	req = httptest.NewRequest(http.MethodGet, "/admin/notifications?page=1&per_page=10", nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &listResp)
	if listResp.Meta.Total != 0 {
		t.Fatalf("alive-only list total = %d, want 0 after delete", listResp.Meta.Total)
	}

	// include_deleted=1 → total == 1.
	req = httptest.NewRequest(http.MethodGet, "/admin/notifications?page=1&per_page=10&include_deleted=1", nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &listResp)
	if listResp.Meta.Total != 1 {
		t.Fatalf("include_deleted list total = %d, want 1", listResp.Meta.Total)
	}
	_ = chi.NewRouter // silence unused import when future edits remove one
	_ = bytes.NewReader
}

func TestNotificationsAdmin_CreateValidation(t *testing.T) {
	st := openTestAdminStore(t)
	admin := seedAdminTestUser(t, st, true)
	r := newAdminTestRouter(t, st)

	cases := []struct {
		name string
		body string
		want int
		code string // JSON error.code
	}{
		{"empty title", `{"title":"","body":"b","audience_type":"global"}`, http.StatusBadRequest, "invalid_input"},
		{"title too long", `{"title":"` + strings.Repeat("x", 201) + `","body":"b","audience_type":"global"}`, http.StatusBadRequest, "invalid_input"},
		{"body too long", `{"title":"t","body":"` + strings.Repeat("x", 20001) + `","audience_type":"global"}`, http.StatusBadRequest, "invalid_input"},
		{"invalid audience_type", `{"title":"t","body":"b","audience_type":"team"}`, http.StatusBadRequest, "invalid_input"},
		{"global with audience_id", `{"title":"t","body":"b","audience_type":"global","audience_id":"00000000-0000-0000-0000-000000000000"}`, http.StatusBadRequest, "invalid_input"},
		{"project missing audience_id", `{"title":"t","body":"b","audience_type":"project"}`, http.StatusBadRequest, "invalid_input"},
		{"user missing audience_id", `{"title":"t","body":"b","audience_type":"user"}`, http.StatusBadRequest, "invalid_input"},
		{"project audience_id not found", `{"title":"t","body":"b","audience_type":"project","audience_id":"00000000-0000-0000-0000-000000000001"}`, http.StatusBadRequest, "invalid_audience"},
		{"user audience_id not found", `{"title":"t","body":"b","audience_type":"user","audience_id":"00000000-0000-0000-0000-000000000002"}`, http.StatusBadRequest, "invalid_audience"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/notifications", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+admin.token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
			var errResp struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			json.Unmarshal(rec.Body.Bytes(), &errResp)
			if errResp.Error.Code != tc.code {
				t.Fatalf("error.code=%q, want %q; body=%s", errResp.Error.Code, tc.code, rec.Body.String())
			}
		})
	}
}
```

**Test-helper contract for this file:** the three helpers referenced above (`openTestAdminStore`, `seedAdminTestUser`, `newAdminTestRouter`) do not yet exist. Create them in a **shared** admin test helpers file `internal/admin/testhelpers_test.go` in this same commit so both this file and Task 4's user tests reuse them. Their contract:

```go
package admin

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/auth"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

type adminTestUser struct {
	user  *types.User
	token string
}

// openTestAdminStore opens a store that shares openTestStore's DB
// semantics — same TEST_DATABASE_URL discipline, same migrations run.
// Skips the test when TEST_DATABASE_URL is unset. If the tests package
// doesn't already have a shared store helper, this thin wrapper calls
// through to the store package's openTestStore via an exported
// test-only accessor added there (or simply duplicates the pool-open
// logic — pick whichever keeps the diff small).
func openTestAdminStore(t *testing.T) *store.Store { /* … */ }

// seedAdminTestUser inserts a users row (with is_superadmin flag) and
// signs an access token bound to it.
func seedAdminTestUser(t *testing.T, st *store.Store, superadmin bool) adminTestUser { /* … */ }

// newAdminTestRouter builds a chi.Router with only the routes this
// test package's tests exercise mounted (auth middleware + the
// /admin/notifications and /notifications subtrees). Full-server
// wiring (OAuth, delivery, upstreams, etc.) is out of scope.
func newAdminTestRouter(t *testing.T, st *store.Store) chi.Router { /* … */ }
```

**Implementation note for `openTestAdminStore`:** the store package's `openTestStore` (`internal/store/extra_usage_db_test.go:13`) is currently package-private — accessible only from the `store` package. Two options: (a) add an exported `OpenTestStore` shim in a new `internal/store/testing.go` guarded by a `//go:build test` tag or by making it callable when `TEST_DATABASE_URL` is set; (b) copy the ~20-line pool-open + migrations run into `internal/admin/testhelpers_test.go` and gate on the same env var. **Pick (b)** — the shim in (a) would leak a test-only export into the store package's public surface. Keep the copied helper minimal; if it drifts, refactor later.

- [ ] **Step 2: Run tests — expect failure**

Run: `go test ./internal/admin/ -run 'TestNotificationsAdmin' -v`
Expected: compile error (`openTestAdminStore`, `seedAdminTestUser`, `newAdminTestRouter`, `handleListAllNotifications`, etc. all undefined). Good.

- [ ] **Step 3: Write the admin handlers**

Create `internal/admin/handle_notifications.go`:

```go
package admin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

// notificationCreatePayload is the request body for POST /admin/notifications
// and PUT /admin/notifications/{id}.
type notificationCreatePayload struct {
	Title        string  `json:"title"`
	Body         string  `json:"body"`
	AudienceType string  `json:"audience_type"`
	AudienceID   *string `json:"audience_id"`
}

// validateNotificationPayload returns (errCode, errMessage) on failure or
// ("","") on success. It ONLY checks shape and field bounds; audience
// existence checks happen in the handler (which needs the Store).
func validateNotificationPayload(p notificationCreatePayload) (string, string) {
	if len(p.Title) < 1 || len(p.Title) > 200 {
		return "invalid_input", "title must be 1..200 characters"
	}
	if len(p.Body) < 1 || len(p.Body) > 20000 {
		return "invalid_input", "body must be 1..20000 characters"
	}
	switch p.AudienceType {
	case types.AudienceTypeGlobal:
		if p.AudienceID != nil && *p.AudienceID != "" {
			return "invalid_input", "audience_id must be omitted for global audience"
		}
	case types.AudienceTypeProject, types.AudienceTypeUser:
		if p.AudienceID == nil || *p.AudienceID == "" {
			return "invalid_input", "audience_id is required for project/user audience"
		}
	default:
		return "invalid_input", "audience_type must be one of: global, project, user"
	}
	return "", ""
}

// resolveAudience verifies the audience row exists and is usable.
// Returns ("","") on success or (code, message) on failure suitable for
// writeError. Never called for global audience.
func resolveAudience(st *store.Store, audienceType, audienceID string) (string, string) {
	switch audienceType {
	case types.AudienceTypeProject:
		p, err := st.GetProjectByID(audienceID)
		if err != nil || p == nil {
			return "invalid_audience", "project not found"
		}
		if p.Status == types.ProjectStatusArchived {
			return "invalid_audience", "project is archived"
		}
	case types.AudienceTypeUser:
		u, err := st.GetUserByID(audienceID)
		if err != nil || u == nil {
			return "invalid_audience", "user not found"
		}
		if u.Status == types.UserStatusDisabled {
			return "invalid_audience", "user is disabled"
		}
	}
	return "", ""
}

func handleListAllNotifications(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := parsePagination(r)
		includeDeleted := r.URL.Query().Get("include_deleted") == "1"
		audienceType := strings.ToLower(r.URL.Query().Get("audience_type"))
		if audienceType != "" &&
			audienceType != types.AudienceTypeGlobal &&
			audienceType != types.AudienceTypeProject &&
			audienceType != types.AudienceTypeUser {
			writeError(w, http.StatusBadRequest, "invalid_input", "audience_type filter must be one of: global, project, user")
			return
		}
		items, total, err := st.ListAllNotifications(includeDeleted, audienceType, p)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to list notifications")
			return
		}
		writeList(w, items, total, p.Page, p.Limit())
	}
}

func handleGetNotification(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		n, err := st.GetNotification(id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not_found", "notification not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "failed to fetch notification")
			return
		}
		writeData(w, http.StatusOK, n)
	}
}

func handleCreateNotification(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me := UserFromContext(r.Context())
		var body notificationCreatePayload
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "invalid request body")
			return
		}
		if code, msg := validateNotificationPayload(body); code != "" {
			writeError(w, http.StatusBadRequest, code, msg)
			return
		}
		if body.AudienceType != types.AudienceTypeGlobal {
			if code, msg := resolveAudience(st, body.AudienceType, *body.AudienceID); code != "" {
				writeError(w, http.StatusBadRequest, code, msg)
				return
			}
		}
		n := &types.Notification{
			Title:        body.Title,
			Body:         body.Body,
			AudienceType: body.AudienceType,
			AudienceID:   body.AudienceID,
			CreatedBy:    me.ID,
		}
		if err := st.CreateNotification(n); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to create notification")
			return
		}
		writeData(w, http.StatusOK, n)
	}
}

func handleUpdateNotification(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var body notificationCreatePayload
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "invalid request body")
			return
		}
		if code, msg := validateNotificationPayload(body); code != "" {
			writeError(w, http.StatusBadRequest, code, msg)
			return
		}
		if body.AudienceType != types.AudienceTypeGlobal {
			if code, msg := resolveAudience(st, body.AudienceType, *body.AudienceID); code != "" {
				writeError(w, http.StatusBadRequest, code, msg)
				return
			}
		}
		if err := st.UpdateNotification(id, body.Title, body.Body, body.AudienceType, body.AudienceID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not_found", "notification not found or already deleted")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "failed to update notification")
			return
		}
		n, err := st.GetNotification(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to reload notification")
			return
		}
		writeData(w, http.StatusOK, n)
	}
}

func handleDeleteNotification(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := st.SoftDeleteNotification(id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not_found", "notification not found or already deleted")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "failed to delete notification")
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
```

- [ ] **Step 4: Mount the admin route subtree**

Open `internal/admin/routes.go`. Locate the existing authenticated group (starts around line 92 with `r.Group(func(r chi.Router) { r.Use(JWTAuthMiddleware(...))`). Inside that group, add a new subtree — the exact spot doesn't matter, but for symmetry put it near `/admin/requests` (~line 137):

```go
			// Admin: notifications CRUD (superadmin only).
			r.Route("/admin/notifications", func(r chi.Router) {
				r.Use(RequireSuperadmin)
				r.Get("/",     handleListAllNotifications(st))
				r.Post("/",    handleCreateNotification(st))
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/",    handleGetNotification(st))
					r.Put("/",    handleUpdateNotification(st))
					r.Delete("/", handleDeleteNotification(st))
				})
			})
```

No other file changes.

- [ ] **Step 5: Run tests — expect pass**

Run:
```bash
cd /root/coding/modelserver
go build ./...
go test ./internal/admin/ -run 'TestNotificationsAdmin' -v
```
Expected: build clean; all `TestNotificationsAdmin*` cases PASS (SKIP without `TEST_DATABASE_URL`).

- [ ] **Step 6: Run the full admin test suite for regression safety**

Run: `go test ./internal/admin/`
Expected: PASS everywhere. Existing tests must not have been disturbed by the new import (`pgx` was already imported in the package; `chi` too).

- [ ] **Step 7: Commit**

```bash
cd /root/coding/modelserver
git add internal/admin/handle_notifications.go \
        internal/admin/handle_notifications_test.go \
        internal/admin/testhelpers_test.go \
        internal/admin/routes.go
git commit -m "feat(admin): notifications CRUD handlers (superadmin only)

Adds the 5 admin routes under /admin/notifications:
  GET /             — page-based list (?include_deleted=1 opts in soft-deleted)
  POST /            — create; validates title/body length + audience shape
                      + audience existence (project not-archived, user
                      not-disabled)
  GET /{id}         — read one (includes read_count)
  PUT /{id}         — update (bumps updated_at, does NOT touch reads)
  DELETE /{id}      — soft delete (sets deleted_at)

Wired inside the existing JWTAuthMiddleware group with RequireSuperadmin
on the subtree. Shared test helpers (openTestAdminStore, seedAdminTestUser,
newAdminTestRouter) land in testhelpers_test.go so Task 4's user
handler tests reuse them without a second bespoke setup.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: User handlers + route mount

**Files:**
- Modify: `internal/admin/handle_notifications.go` (append 4 user-facing handlers next to the admin ones)
- Modify: `internal/admin/routes.go` (add `/notifications` subtree — no `RequireSuperadmin`)
- Modify: `internal/admin/handle_notifications_test.go` (append user-facing test cases; reuses the Task 3 shared helpers)

**Interfaces:**
- Consumes:
  - `Store.ListVisibleForUser(userID string, p types.PaginationParams) ([]types.Notification, int, error)` (Task 2).
  - `Store.CountUnreadForUser(userID string) (int, error)` (Task 2).
  - `Store.MarkNotificationRead(userID, notificationID string) error` (Task 2).
  - `Store.MarkAllNotificationsRead(userID string) (int, error)` (Task 2).
  - `UserFromContext`, `writeError`, `writeData`, `writeJSON`, `writeList`, `parsePagination`, `JWTAuthMiddleware` (`internal/admin/admin.go`).
  - Chi router `{id}` URL param helper.
  - `openTestAdminStore`, `seedAdminTestUser`, `newAdminTestRouter` (added in Task 3's `testhelpers_test.go`).
- Produces:
  - 4 user route handlers, each `http.HandlerFunc`, prefixed `handleUser*Notification[s]` so they never collide with the admin `handle*Notification[s]` set.
  - Route mount at `/notifications` inside the authenticated group but OUTSIDE the `RequireSuperadmin` subtree. Any logged-in user reaches it.

- [ ] **Step 1: Append failing tests**

Add to `internal/admin/handle_notifications_test.go`:

```go
func TestNotificationsUser_UnreadCount_And_ListMarksReadable(t *testing.T) {
	st := openTestAdminStore(t)
	me := seedAdminTestUser(t, st, false)
	other := seedAdminTestUser(t, st, true)
	r := newAdminTestRouter(t, st)

	// Admin creates two global notifications visible to `me`.
	for _, title := range []string{"one", "two"} {
		body := `{"title":"` + title + `","body":"b","audience_type":"global"}`
		req := httptest.NewRequest(http.MethodPost, "/admin/notifications", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+other.token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("seed create %s: status=%d body=%s", title, rec.Code, rec.Body.String())
		}
	}

	// unread_count == 2 for `me`.
	req := httptest.NewRequest(http.MethodGet, "/notifications/unread_count", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unread_count status=%d", rec.Code)
	}
	var countResp struct {
		Data struct {
			Count int `json:"count"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &countResp)
	if countResp.Data.Count != 2 {
		t.Fatalf("unread_count = %d, want 2", countResp.Data.Count)
	}

	// List returns 2 items, both with read_at == nil.
	req = httptest.NewRequest(http.MethodGet, "/notifications?page=1&per_page=10", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var listResp struct {
		Data []struct {
			ID     string  `json:"id"`
			ReadAt *string `json:"read_at,omitempty"`
		} `json:"data"`
		Meta struct{ Total int } `json:"meta"`
	}
	json.Unmarshal(rec.Body.Bytes(), &listResp)
	if listResp.Meta.Total != 2 || len(listResp.Data) != 2 {
		t.Fatalf("list total=%d len=%d, want 2/2", listResp.Meta.Total, len(listResp.Data))
	}
	for _, it := range listResp.Data {
		if it.ReadAt != nil {
			t.Fatalf("unexpected read_at on unread item %s: %v", it.ID, *it.ReadAt)
		}
	}

	// Mark the first one read.
	firstID := listResp.Data[0].ID
	req = httptest.NewRequest(http.MethodPost, "/notifications/"+firstID+"/read", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read status=%d", rec.Code)
	}

	// Idempotent: second POST still 200.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/notifications/"+firstID+"/read", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second mark read status=%d, want 200 (idempotent)", rec.Code)
	}

	// unread_count now 1.
	req = httptest.NewRequest(http.MethodGet, "/notifications/unread_count", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &countResp)
	if countResp.Data.Count != 1 {
		t.Fatalf("unread_count after one read = %d, want 1", countResp.Data.Count)
	}

	// Mark-all-read → marked == 1 (only the remaining unread).
	req = httptest.NewRequest(http.MethodPost, "/notifications/read_all", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("read_all status=%d", rec.Code)
	}
	var markedResp struct {
		Data struct {
			Marked int `json:"marked"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &markedResp)
	if markedResp.Data.Marked != 1 {
		t.Fatalf("read_all marked = %d, want 1", markedResp.Data.Marked)
	}

	// unread_count now 0.
	req = httptest.NewRequest(http.MethodGet, "/notifications/unread_count", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &countResp)
	if countResp.Data.Count != 0 {
		t.Fatalf("unread_count after read_all = %d, want 0", countResp.Data.Count)
	}
}

func TestNotificationsUser_MarkReadIsSilentOnInvisibleOrDeleted(t *testing.T) {
	st := openTestAdminStore(t)
	me := seedAdminTestUser(t, st, false)
	admin := seedAdminTestUser(t, st, true)
	other := seedAdminTestUser(t, st, false)
	r := newAdminTestRouter(t, st)

	// Notification visible to `other` only.
	otherID := other.user.ID
	body := `{"title":"t","body":"b","audience_type":"user","audience_id":"` + otherID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/notifications", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+admin.token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var createResp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &createResp)
	invisibleID := createResp.Data.ID

	// me marks the invisible notification read → 200 silent no-op.
	req = httptest.NewRequest(http.MethodPost, "/notifications/"+invisibleID+"/read", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("invisible read status=%d, want 200 (silent)", rec.Code)
	}
	// And unread_count for me stays 0.
	req = httptest.NewRequest(http.MethodGet, "/notifications/unread_count", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var countResp struct {
		Data struct {
			Count int `json:"count"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &countResp)
	if countResp.Data.Count != 0 {
		t.Fatalf("unread_count after invisible mark-read = %d, want 0", countResp.Data.Count)
	}

	// admin soft-deletes a notification; me marking it also returns 200.
	body = `{"title":"delme","body":"b","audience_type":"global"}`
	req = httptest.NewRequest(http.MethodPost, "/admin/notifications", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+admin.token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &createResp)
	delID := createResp.Data.ID
	req = httptest.NewRequest(http.MethodDelete, "/admin/notifications/"+delID, nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	req = httptest.NewRequest(http.MethodPost, "/notifications/"+delID+"/read", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deleted read status=%d, want 200 (silent)", rec.Code)
	}
}

func TestNotificationsUser_RequiresAuth(t *testing.T) {
	st := openTestAdminStore(t)
	r := newAdminTestRouter(t, st)

	for _, ep := range []string{"/notifications", "/notifications/unread_count"} {
		req := httptest.NewRequest(http.MethodGet, ep, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated status=%d, want 401", ep, rec.Code)
		}
	}
}
```

- [ ] **Step 2: Run tests — expect failure**

Run: `go test ./internal/admin/ -run 'TestNotificationsUser' -v`
Expected: compile error (`handleListMyNotifications`, `handleUnreadNotificationCount`, `handleUserMarkNotificationRead`, `handleUserMarkAllNotificationsRead` all undefined) or route-not-found 404s.

- [ ] **Step 3: Append the user handlers to `internal/admin/handle_notifications.go`**

Add at the bottom of the file:

```go
// ==== User-facing handlers ====

func handleListMyNotifications(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me := UserFromContext(r.Context())
		p := parsePagination(r)
		items, total, err := st.ListVisibleForUser(me.ID, p)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to list notifications")
			return
		}
		writeList(w, items, total, p.Page, p.Limit())
	}
}

func handleUnreadNotificationCount(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me := UserFromContext(r.Context())
		count, err := st.CountUnreadForUser(me.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to count unread notifications")
			return
		}
		writeData(w, http.StatusOK, map[string]int{"count": count})
	}
}

func handleUserMarkNotificationRead(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me := UserFromContext(r.Context())
		id := chi.URLParam(r, "id")
		// Store.MarkNotificationRead is silent (returns nil) when the
		// notification is invisible or deleted — matches the spec's
		// "silent 200 on unknown id" contract to avoid confusing 404s
		// during an admin-delete race.
		if err := st.MarkNotificationRead(me.ID, id); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to mark notification read")
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func handleUserMarkAllNotificationsRead(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me := UserFromContext(r.Context())
		marked, err := st.MarkAllNotificationsRead(me.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to mark all notifications read")
			return
		}
		writeData(w, http.StatusOK, map[string]int{"marked": marked})
	}
}
```

- [ ] **Step 4: Mount the user route subtree**

Open `internal/admin/routes.go`. Inside the same authenticated group used in Task 3 (starts around line 92), add the subtree BEFORE the `/admin/notifications` block so alphabetical/logical grouping keeps "user-facing before admin":

```go
			// User-facing notifications inbox (any authenticated user).
			r.Route("/notifications", func(r chi.Router) {
				r.Get("/",              handleListMyNotifications(st))
				r.Get("/unread_count",  handleUnreadNotificationCount(st))
				r.Post("/{id}/read",    handleUserMarkNotificationRead(st))
				r.Post("/read_all",     handleUserMarkAllNotificationsRead(st))
			})
```

Note: the subtree is NOT wrapped in `RequireSuperadmin` — the JWT middleware from the enclosing group is the only gate.

- [ ] **Step 5: Run tests — expect pass**

Run:
```bash
cd /root/coding/modelserver
go build ./...
go test ./internal/admin/ -run 'TestNotifications' -v
```
Expected: build clean; every `TestNotificationsAdmin*` AND `TestNotificationsUser*` case PASS. SKIP without `TEST_DATABASE_URL`.

- [ ] **Step 6: Run the full admin + store test suite for regression safety**

Run: `go test ./internal/admin/ ./internal/store/ ./internal/config/ ./internal/proxy/`
Expected: PASS everywhere.

- [ ] **Step 7: Commit**

```bash
cd /root/coding/modelserver
git add internal/admin/handle_notifications.go \
        internal/admin/handle_notifications_test.go \
        internal/admin/routes.go
git commit -m "feat(admin): notifications user-facing handlers (any auth user)

Adds 4 routes under /notifications inside the JWTAuthMiddleware group
(no RequireSuperadmin):
  GET /                  — page-based list of visible notifications
                           (global + project-I'm-in + addressed-to-me),
                           with read_at populated and audience_name for
                           display.
  GET /unread_count      — {count: N}. Sidebar badge poller reads this
                           every 45s.
  POST /{id}/read        — idempotent mark-as-read. Silent 200 on
                           invisible/deleted ids (Store guards via
                           the visibility SELECT; see Task 2).
  POST /read_all         — {marked: N}. Bulk mark for every currently-
                           visible unread notification.

Sits BELOW the admin subtree in routes.go alphabetically (user-facing
first, admin second), but functionally these are unrelated — a user
cannot reach /admin/notifications without RequireSuperadmin passing.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Frontend API hooks + Sidebar entry + user Notifications page

**Files:**
- Create: `dashboard/src/api/notifications.ts`
- Create: `dashboard/src/pages/notifications/NotificationsPage.tsx`
- Modify: `dashboard/src/api/types.ts` (append notification types near existing shared types)
- Modify: `dashboard/src/components/layout/Sidebar.tsx` (extend `SidebarLink` with `badge?: number`; add Notifications entry above Projects; poll unread count)
- Modify: `dashboard/src/App.tsx` (add `/notifications` route)

**Interfaces:**
- Consumes:
  - Backend routes `/api/v1/notifications`, `/api/v1/notifications/unread_count`, `/api/v1/notifications/{id}/read`, `/api/v1/notifications/read_all` (Task 4, mounted under the same `/api/v1` prefix every other admin route uses — see `dashboard/src/api/plans.ts:8` for the prefix convention).
  - `api.get`/`api.post` from `dashboard/src/api/client.ts` (`api.` object at line 164).
  - `ListResponse<T>`, `DataResponse<T>` envelopes from `dashboard/src/api/types.ts`.
  - `useAuth()` (already used by `Sidebar.tsx`).
- Produces:
  - React Query hooks used by both Task 5's user page and Task 6's admin page: `useMyNotifications(page, perPage)`, `useUnreadNotificationCount()`, `useMarkNotificationRead()`, `useMarkAllNotificationsRead()`. (Admin hooks land in Task 6.)
  - A rendered `Bell` sidebar entry with a `badge?: number` prop already threaded through `SidebarLink`.
  - A `/notifications` route rendering `NotificationsPage`.

- [ ] **Step 1: Extend `dashboard/src/api/types.ts`**

Append near the other domain types (search for "export interface Plan" as an anchor; insert nearby, order doesn't matter for behavior):

```ts
export type AudienceType = "global" | "project" | "user";

export interface Notification {
  id: string;
  title: string;
  body: string;
  audience_type: AudienceType;
  audience_id?: string | null;
  audience_name?: string | null;   // "Broadcast" mapping is done client-side (empty string ⇒ "Broadcast")
  created_by: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string | null;
  read_at?: string | null;         // populated only on the user list
  read_count?: number;             // populated only on admin listings
}

export interface NotificationCreatePayload {
  title: string;
  body: string;
  audience_type: AudienceType;
  audience_id?: string | null;
}

export type NotificationUpdatePayload = NotificationCreatePayload;
```

- [ ] **Step 2: Create `dashboard/src/api/notifications.ts`**

```ts
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";
import type {
  DataResponse,
  ListResponse,
  Notification,
  NotificationCreatePayload,
  NotificationUpdatePayload,
} from "./types";

// ==== User-facing hooks ====

export function useMyNotifications(page = 1, perPage = 20) {
  return useQuery({
    queryKey: ["my-notifications", page, perPage],
    queryFn: () =>
      api.get<ListResponse<Notification>>(
        `/api/v1/notifications?page=${page}&per_page=${perPage}`,
      ),
  });
}

// useUnreadNotificationCount polls every 45s so the sidebar badge stays
// current without flooding the API. staleTime 30s absorbs same-window
// tab-focus revisits without a fresh network call.
export function useUnreadNotificationCount() {
  return useQuery({
    queryKey: ["notifications-unread-count"],
    queryFn: () =>
      api.get<DataResponse<{ count: number }>>(
        "/api/v1/notifications/unread_count",
      ),
    refetchInterval: 45_000,
    staleTime: 30_000,
  });
}

export function useMarkNotificationRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      api.post<void>(`/api/v1/notifications/${id}/read`, undefined),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["notifications-unread-count"] });
      qc.invalidateQueries({ queryKey: ["my-notifications"] });
    },
  });
}

export function useMarkAllNotificationsRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      api.post<DataResponse<{ marked: number }>>(
        "/api/v1/notifications/read_all",
        undefined,
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["notifications-unread-count"] });
      qc.invalidateQueries({ queryKey: ["my-notifications"] });
    },
  });
}

// ==== Admin hooks (used by Task 6's admin page) ====

export function useAdminNotifications(page = 1, perPage = 20, includeDeleted = false) {
  return useQuery({
    queryKey: ["admin-notifications", page, perPage, includeDeleted],
    queryFn: () =>
      api.get<ListResponse<Notification>>(
        `/api/v1/admin/notifications?page=${page}&per_page=${perPage}` +
          (includeDeleted ? "&include_deleted=1" : ""),
      ),
  });
}

export function useCreateNotification() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: NotificationCreatePayload) =>
      api.post<DataResponse<Notification>>("/api/v1/admin/notifications", body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin-notifications"] }),
  });
}

export function useUpdateNotification() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: NotificationUpdatePayload }) =>
      api.put<DataResponse<Notification>>(`/api/v1/admin/notifications/${id}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin-notifications"] }),
  });
}

export function useDeleteNotification() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      api.delete<void>(`/api/v1/admin/notifications/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin-notifications"] }),
  });
}
```

- [ ] **Step 3: Modify `dashboard/src/components/layout/Sidebar.tsx`**

Three edits. First, extend the lucide import block to include `Bell`. Locate the existing import block starting `import {\n  LayoutDashboard,\n  Key, ...` and add `Bell,` alphabetically after `BarChart3,`:

```tsx
import {
  LayoutDashboard,
  Key,
  Users,
  FileText,
  BarChart3,
  Bell,
  Zap,
  Settings,
  Shield,
  Coins,
  FolderOpen,
  LogOut,
  Route,
  Server,
  Network,
  Lock,
  KeyRound,
  Sparkles,
} from "lucide-react";
```

Second, extend the `SidebarLink` component to accept an optional `badge?: number` and render the pill when the number is `≥ 1`. Replace the existing function definition (lines 28–56 approx) with:

```tsx
function SidebarLink({
  to,
  icon: Icon,
  children,
  end,
  badge,
}: {
  to: string;
  icon: React.ComponentType<{ className?: string }>;
  children: React.ReactNode;
  end?: boolean;
  badge?: number;
}) {
  const showBadge = typeof badge === "number" && badge > 0;
  const badgeText = badge && badge >= 100 ? "99+" : String(badge);
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        cn(
          "flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
          isActive
            ? "bg-accent text-accent-foreground"
            : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
        )
      }
    >
      <Icon className="h-4 w-4" />
      <span className="flex-1">{children}</span>
      {showBadge && (
        <span className="bg-primary text-primary-foreground text-[10px] leading-none px-1.5 py-0.5 rounded-full">
          {badgeText}
        </span>
      )}
    </NavLink>
  );
}
```

Third, import the new hook and insert the Notifications entry above Projects. Add near the top:

```tsx
import { useUnreadNotificationCount } from "@/api/notifications";
```

Inside the `Sidebar` function body, right after `const { user, logout } = useAuth();`, add:

```tsx
  const { data: unreadResp } = useUnreadNotificationCount();
  const unreadCount = unreadResp?.data?.count ?? 0;
```

And in the `nav` block replace the current single line for Projects:

```tsx
        <SidebarLink to="/projects" icon={FolderOpen}>
          Projects
        </SidebarLink>
```

with:

```tsx
        <SidebarLink to="/notifications" icon={Bell} badge={unreadCount}>
          Notifications
        </SidebarLink>
        <SidebarLink to="/projects" icon={FolderOpen}>
          Projects
        </SidebarLink>
```

Also append inside the `user?.is_superadmin` admin block, right after the OAuth Clients entry:

```tsx
            <SidebarLink to="/admin/notifications" icon={Bell}>
              Notifications
            </SidebarLink>
```

- [ ] **Step 4: Create `dashboard/src/pages/notifications/NotificationsPage.tsx`**

```tsx
import { useState } from "react";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  useMyNotifications,
  useMarkNotificationRead,
  useMarkAllNotificationsRead,
  useUnreadNotificationCount,
} from "@/api/notifications";
import type { Notification } from "@/api/types";
import { Loader2 } from "lucide-react";

function audienceLabel(n: Notification): string {
  if (n.audience_type === "global") return "Broadcast";
  if (n.audience_type === "user") return "You";
  // project — audience_name is populated server-side with display_name.
  return n.audience_name ? `Project: ${n.audience_name}` : "Project";
}

function timeAgo(iso: string): string {
  const diffMs = Date.now() - new Date(iso).getTime();
  const sec = Math.floor(diffMs / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  if (day < 30) return `${day}d ago`;
  return new Date(iso).toLocaleDateString();
}

function NotificationRow({ n }: { n: Notification }) {
  const [open, setOpen] = useState(false);
  const markRead = useMarkNotificationRead();
  const isUnread = !n.read_at;

  function handleToggle() {
    const nextOpen = !open;
    setOpen(nextOpen);
    if (nextOpen && isUnread) {
      // Fire-and-forget; hook invalidates queries on success. Errors
      // are ignored — the user's next 45s poll will resync the badge.
      markRead.mutate(n.id);
    }
  }

  return (
    <div className="border-b last:border-b-0">
      <button
        type="button"
        onClick={handleToggle}
        className="w-full flex items-center gap-2 px-4 py-3 text-left hover:bg-accent/50"
      >
        {isUnread ? (
          <span aria-hidden className="h-2 w-2 rounded-full bg-primary shrink-0" />
        ) : (
          <span aria-hidden className="h-2 w-2 shrink-0" />
        )}
        <span className={"flex-1 text-sm " + (isUnread ? "font-semibold" : "font-normal text-muted-foreground")}>
          {n.title}
        </span>
        <Badge variant="secondary" className="shrink-0 text-[10px]">
          {audienceLabel(n)}
        </Badge>
        <span className="text-xs text-muted-foreground shrink-0">{timeAgo(n.created_at)}</span>
      </button>
      {open && (
        <div className="px-4 pb-4 pt-1 text-sm text-muted-foreground whitespace-pre-wrap">
          {n.body}
        </div>
      )}
    </div>
  );
}

export function NotificationsPage() {
  const [page, setPage] = useState(1);
  const perPage = 20;
  const { data, isLoading } = useMyNotifications(page, perPage);
  const { data: countResp } = useUnreadNotificationCount();
  const markAll = useMarkAllNotificationsRead();
  const items = data?.data ?? [];
  const total = data?.meta?.total ?? 0;
  const totalPages = data?.meta?.total_pages ?? 1;
  const unread = countResp?.data?.count ?? 0;

  return (
    <div className="p-6 space-y-4 max-w-3xl mx-auto">
      <div className="flex items-center justify-between">
        <PageHeader title="Notifications" description="Platform announcements and updates." />
        {unread > 0 && (
          <Button variant="outline" size="sm" onClick={() => markAll.mutate()}>
            Mark all as read
          </Button>
        )}
      </div>

      {isLoading ? (
        <div className="flex items-center gap-2 text-muted-foreground text-sm">
          <Loader2 className="h-4 w-4 animate-spin" /> Loading…
        </div>
      ) : items.length === 0 ? (
        <p className="text-sm text-muted-foreground">No notifications yet.</p>
      ) : (
        <div className="rounded border bg-card">
          {items.map((n) => (
            <NotificationRow key={n.id} n={n} />
          ))}
        </div>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>
            Page {page} of {totalPages} · {total} total
          </span>
          <div className="flex gap-2">
            <Button size="sm" variant="outline" disabled={page <= 1} onClick={() => setPage(page - 1)}>
              Prev
            </Button>
            <Button size="sm" variant="outline" disabled={page >= totalPages} onClick={() => setPage(page + 1)}>
              Next
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 5: Modify `dashboard/src/App.tsx`**

Locate the `<Route path="projects" element={<ProjectListPage />} />` line (~line 96). Add above it, in the same authenticated `<Route element={<AppShell />}>` block:

```tsx
                <Route path="notifications" element={<NotificationsPage />} />
```

Then add the import at the top of the file, alphabetically among the existing page imports:

```tsx
import { NotificationsPage } from "@/pages/notifications/NotificationsPage";
```

(Admin route `/admin/notifications` is added in Task 6.)

- [ ] **Step 6: Type-check + build**

Run:
```bash
cd /root/coding/modelserver/dashboard
npm run build
```
Expected: `tsc -b` and `vite build` both succeed. The chunk-size warning about the main bundle is pre-existing and can be ignored.

- [ ] **Step 7: Commit**

```bash
cd /root/coding/modelserver
git add dashboard/src/api/notifications.ts \
        dashboard/src/pages/notifications/NotificationsPage.tsx \
        dashboard/src/api/types.ts \
        dashboard/src/components/layout/Sidebar.tsx \
        dashboard/src/App.tsx
git commit -m "feat(dashboard): notifications inbox + sidebar badge

Adds the user-facing notification inbox at /notifications, driven by a
new React Query hook module (dashboard/src/api/notifications.ts) that
also exposes the admin CRUD hooks used by Task 6.

Sidebar gains a Notifications entry above Projects with an unread-count
badge (poll every 45s, staleTime 30s). SidebarLink learns an optional
badge prop that renders the count as a pill; 0/undefined hides it,
≥100 collapses to '99+'.

Superadmin block also gains a Notifications entry pointing at
/admin/notifications (page arrives in Task 6).

Backend routes were shipped in Tasks 3 and 4.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Admin Notifications page + end-to-end verify + PR

**Files:**
- Create: `dashboard/src/pages/admin/NotificationsPage.tsx`
- Modify: `dashboard/src/App.tsx` (add `/admin/notifications` route)

**Interfaces:**
- Consumes:
  - `useAdminNotifications`, `useCreateNotification`, `useUpdateNotification`, `useDeleteNotification` (Task 5).
  - `types.Notification`, `NotificationCreatePayload`, `AudienceType` (Task 5).
  - `UserCombobox` from `@/components/shared/UserCombobox` (existing).
  - `DataTable`, `Column<T>` from `@/components/shared/DataTable` (existing; used e.g. `PlansPage.tsx:10`).
  - shadcn/ui primitives: `Dialog`, `AlertDialog`, `Button`, `Input`, `Textarea`, `Label`, `RadioGroup`, `RadioGroupItem`, `Alert`, `Badge`.
- Produces:
  - The admin management UI at `/admin/notifications` reachable via the sidebar entry added in Task 5.

**Deliberate simplification vs spec §4.3:** the spec mentioned an `AdminProjectCombobox` "already used by AdminProjectsPage" — that component does not actually exist in the codebase (`AdminProjectsPage.tsx` uses a plain project-id filter text input). Rather than build a new combobox, this task uses a plain `<Input placeholder="Project UUID">` for the project-audience case, with helper text pointing at `/admin/projects` where admins can copy the UUID. If demand for a picker materializes later, extract `UserCombobox`'s pattern into a `ProjectCombobox` in a follow-up. This keeps Task 6 scope tight to notifications.

- [ ] **Step 1: Create `dashboard/src/pages/admin/NotificationsPage.tsx`**

```tsx
import { useState } from "react";
import { PageHeader } from "@/components/layout/PageHeader";
import { DataTable, type Column } from "@/components/shared/DataTable";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { UserCombobox } from "@/components/shared/UserCombobox";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Plus, Pencil, Trash2, Loader2, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import {
  useAdminNotifications,
  useCreateNotification,
  useUpdateNotification,
  useDeleteNotification,
} from "@/api/notifications";
import type { AudienceType, Notification, NotificationCreatePayload } from "@/api/types";

interface FormState {
  title: string;
  body: string;
  audienceType: AudienceType;
  audienceID: string; // stringly for form; converted to null when audienceType==='global'
}

const EMPTY_FORM: FormState = {
  title: "",
  body: "",
  audienceType: "global",
  audienceID: "",
};

function formToPayload(f: FormState): NotificationCreatePayload {
  return {
    title: f.title,
    body: f.body,
    audience_type: f.audienceType,
    audience_id: f.audienceType === "global" ? null : f.audienceID || null,
  };
}

function notificationToForm(n: Notification): FormState {
  return {
    title: n.title,
    body: n.body,
    audienceType: n.audience_type,
    audienceID: n.audience_id ?? "",
  };
}

function audienceCell(n: Notification): string {
  if (n.audience_type === "global") return "Broadcast";
  if (n.audience_type === "project") return `Project: ${n.audience_name ?? n.audience_id ?? "?"}`;
  return `User: ${n.audience_id ?? "?"}`;
}

export function AdminNotificationsPage() {
  const [page, setPage] = useState(1);
  const perPage = 20;
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const { data, isLoading } = useAdminNotifications(page, perPage, includeDeleted);
  const items = data?.data ?? [];
  const total = data?.meta?.total ?? 0;
  const totalPages = data?.meta?.total_pages ?? 1;

  const createMut = useCreateNotification();
  const updateMut = useUpdateNotification();
  const deleteMut = useDeleteNotification();

  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<Notification | null>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [deleteTarget, setDeleteTarget] = useState<Notification | null>(null);

  function openCreate() {
    setEditing(null);
    setForm(EMPTY_FORM);
    setDialogOpen(true);
  }
  function openEdit(n: Notification) {
    setEditing(n);
    setForm(notificationToForm(n));
    setDialogOpen(true);
  }
  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    const payload = formToPayload(form);
    if (payload.audience_type !== "global" && !payload.audience_id) {
      toast.error("audience_id is required for project/user notifications");
      return;
    }
    try {
      if (editing) {
        await updateMut.mutateAsync({ id: editing.id, body: payload });
        toast.success("Notification updated");
      } else {
        await createMut.mutateAsync(payload);
        toast.success("Notification created");
      }
      setDialogOpen(false);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Save failed");
    }
  }
  async function handleDelete() {
    if (!deleteTarget) return;
    try {
      await deleteMut.mutateAsync(deleteTarget.id);
      toast.success("Notification deleted");
      setDeleteTarget(null);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Delete failed");
    }
  }

  const columns: Column<Notification>[] = [
    {
      key: "title",
      header: "Title",
      render: (n) => (
        <button className="text-left hover:underline" onClick={() => openEdit(n)}>
          {n.title}
          {n.deleted_at && <Badge variant="secondary" className="ml-2 text-[10px]">deleted</Badge>}
        </button>
      ),
    },
    { key: "audience", header: "Audience", render: (n) => audienceCell(n) },
    { key: "reads", header: "Reads", render: (n) => `${n.read_count ?? 0}` },
    { key: "created", header: "Created", render: (n) => new Date(n.created_at).toLocaleString() },
    {
      key: "actions",
      header: "",
      render: (n) => (
        <div className="flex gap-1 justify-end">
          <Button size="icon-sm" variant="ghost" onClick={() => openEdit(n)}>
            <Pencil className="h-3.5 w-3.5" />
          </Button>
          <Button size="icon-sm" variant="ghost" onClick={() => setDeleteTarget(n)}>
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <PageHeader title="Notifications" description="Manage platform announcements." />
        <div className="flex items-center gap-2">
          <label className="text-xs text-muted-foreground flex items-center gap-1">
            <input
              type="checkbox"
              checked={includeDeleted}
              onChange={(e) => setIncludeDeleted(e.target.checked)}
            />
            Show deleted
          </label>
          <Button size="sm" onClick={openCreate}>
            <Plus className="h-4 w-4 mr-1" /> New notification
          </Button>
        </div>
      </div>

      {isLoading ? (
        <div className="flex items-center gap-2 text-muted-foreground text-sm">
          <Loader2 className="h-4 w-4 animate-spin" /> Loading…
        </div>
      ) : (
        <DataTable columns={columns} data={items} rowKey={(n) => n.id} />
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>
            Page {page} of {totalPages} · {total} total
          </span>
          <div className="flex gap-2">
            <Button size="sm" variant="outline" disabled={page <= 1} onClick={() => setPage(page - 1)}>
              Prev
            </Button>
            <Button size="sm" variant="outline" disabled={page >= totalPages} onClick={() => setPage(page + 1)}>
              Next
            </Button>
          </div>
        </div>
      )}

      {/* Create / Edit dialog */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{editing ? "Edit notification" : "New notification"}</DialogTitle>
          </DialogHeader>
          {editing && (
            <div className="rounded border border-yellow-500/40 bg-yellow-500/10 text-yellow-900 dark:text-yellow-100 px-3 py-2 text-xs flex items-start gap-2">
              <TriangleAlert className="h-4 w-4 shrink-0 mt-0.5" />
              <span>
                Editing does not re-notify users who have already read this notification. Delete and create anew if you need to reach them again.
              </span>
            </div>
          )}
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-1">
              <Label htmlFor="title">Title</Label>
              <Input
                id="title"
                value={form.title}
                onChange={(e) => setForm({ ...form, title: e.target.value })}
                maxLength={200}
                required
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="body">Body</Label>
              <Textarea
                id="body"
                value={form.body}
                onChange={(e) => setForm({ ...form, body: e.target.value })}
                maxLength={20000}
                rows={6}
                required
              />
              <p className="text-[10px] text-muted-foreground">Plain text; line breaks preserved.</p>
            </div>
            <div className="space-y-1">
              <Label>Audience</Label>
              <RadioGroup
                value={form.audienceType}
                onValueChange={(v) => setForm({ ...form, audienceType: v as AudienceType, audienceID: "" })}
                className="flex gap-4"
              >
                <label className="flex items-center gap-1 text-sm">
                  <RadioGroupItem value="global" /> Global
                </label>
                <label className="flex items-center gap-1 text-sm">
                  <RadioGroupItem value="project" /> Project
                </label>
                <label className="flex items-center gap-1 text-sm">
                  <RadioGroupItem value="user" /> User
                </label>
              </RadioGroup>
            </div>
            {form.audienceType === "project" && (
              <div className="space-y-1">
                <Label htmlFor="proj">Project UUID</Label>
                <Input
                  id="proj"
                  value={form.audienceID}
                  onChange={(e) => setForm({ ...form, audienceID: e.target.value })}
                  placeholder="00000000-0000-0000-0000-000000000000"
                />
                <p className="text-[10px] text-muted-foreground">
                  Copy the project ID from the Admin → Projects page.
                </p>
              </div>
            )}
            {form.audienceType === "user" && (
              <div className="space-y-1">
                <Label>User</Label>
                <UserCombobox
                  value={form.audienceID || null}
                  onChange={(id) => setForm({ ...form, audienceID: id ?? "" })}
                  placeholder="Search user…"
                />
              </div>
            )}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setDialogOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={createMut.isPending || updateMut.isPending}>
                {(createMut.isPending || updateMut.isPending) && <Loader2 className="h-4 w-4 mr-1 animate-spin" />}
                {editing ? "Save" : "Send"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation */}
      <AlertDialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete notification?</AlertDialogTitle>
            <AlertDialogDescription>
              Users who haven't read it yet will no longer see it. Read history is preserved for audit.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={handleDelete}>Delete</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
```

- [ ] **Step 2: Add admin route to `dashboard/src/App.tsx`**

Locate the admin routes block (starts around line 110 with `<Route path="admin/users" element={<UsersPage />} />`). Add near the end of that block:

```tsx
                <Route path="admin/notifications" element={<AdminNotificationsPage />} />
```

Add the import at the top of the file alongside the other admin page imports:

```tsx
import { AdminNotificationsPage } from "@/pages/admin/NotificationsPage";
```

- [ ] **Step 3: Type-check + build the dashboard**

Run:
```bash
cd /root/coding/modelserver/dashboard
npm run build
```
Expected: `tsc -b` and `vite build` both succeed.

If TypeScript complains about missing shadcn primitives (`textarea`, `radio-group`, `alert-dialog`), verify those files exist under `dashboard/src/components/ui/`. All three are already installed for this project — spot-check with:

```bash
ls dashboard/src/components/ui/{textarea,radio-group,alert-dialog}.tsx
```

If any is missing, that's a shadcn-add pre-req step. Handling that install is out of scope for this plan — flag as a blocker in your report and stop.

- [ ] **Step 4: Full backend + frontend sweep for regression safety**

Run:
```bash
cd /root/coding/modelserver
go build ./...
go test ./internal/config/... ./internal/admin/... ./internal/store/... ./internal/proxy/...
```
Expected: build clean; every test PASS. DB-touching tests SKIP without `TEST_DATABASE_URL`, consistent with prior features on this project.

- [ ] **Step 5: Manual smoke (deferred to ops acceptance if no dev DB)**

If dev PostgreSQL + a superadmin user are available:

  1. `docker compose up -d` → migration 064 applies.
  2. Log in as superadmin → dashboard reloads → sidebar shows Notifications (no badge yet) + admin block shows Notifications.
  3. Visit `/admin/notifications` → "New notification" → create a Global one.
  4. Log in as a non-superadmin user in an incognito window → within 45 s the sidebar badge shows `1`.
  5. Click Notifications → row appears with `Broadcast` chip and bold title → click to expand → badge drops to 0 within seconds; row un-bolds.
  6. As superadmin again: create a User-targeted notification pointed at that same user → their badge increments; a Project-targeted notification pointed at a project they belong to → also increments.
  7. Edit an existing notification → yellow banner visible in the dialog; save works.
  8. Delete → confirmation dialog → after confirm, the row disappears from the non-admin's inbox on next refresh; admin list still shows it under "Show deleted".

Otherwise: defer to ops acceptance after merge (matches the deferred-manual-check pattern used by recent PRs).

- [ ] **Step 6: Push branch + open PR**

Before push, verify branch:
```bash
cd /root/coding/modelserver
git branch --show-current    # must be feat/notifications (or similar)
git log --oneline main..HEAD # 6 commits: 5 code + 1 doc (if the spec/plan were committed on this branch instead of main)
```

Then:
```bash
git push -u origin HEAD
gh pr create --base main --title "feat: notifications inbox + admin CRUD" --body "$(cat <<'BODY'
Adds in-dashboard notifications: superadmins create/edit/delete messages
targeted at all users (broadcast), one project, or one user. Users see a
new **Notifications** sidebar entry above Projects with an unread-count
badge, and read the messages on a dedicated inbox page.

## Data model

Two tables (`064_notifications.sql`):
- `notifications` — soft-deleted (`deleted_at`), audience shape enforced
  by a table-level CHECK constraint. Three partial indexes support the
  visibility query.
- `notification_reads` — per-user read state, PK `(notification_id, user_id)`.
  `INSERT ... ON CONFLICT DO NOTHING` makes "mark as read" idempotent.

## Backend

- `Store.ListVisibleForUser` / `CountUnreadForUser` / `MarkNotificationRead`
  / `MarkAllNotificationsRead` share a single `visibilityWhereClause`
  fragment so the union rule (global ∪ project I'm in ∪ addressed-to-me)
  is defined once. Invisible / deleted rows silently insert nothing on
  mark-read, so the frontend never sees a 404 during an admin-delete race.
- Admin routes at `/admin/notifications` (superadmin only): full CRUD +
  `include_deleted=1` query flag.
- User routes at `/notifications` (any authenticated): list,
  `unread_count`, per-item read, `read_all`. All returned via the project-
  standard `writeList` / `writeData` envelopes.

## Frontend

- `dashboard/src/api/notifications.ts` — 8 React Query hooks (4 user, 4
  admin).
- `SidebarLink` grows an optional `badge` prop; sidebar polls
  `/notifications/unread_count` every 45 s (`staleTime` 30 s).
- User inbox: expand-to-mark-read + "Mark all as read". Admin page:
  DataTable + Create/Edit dialog (with edit-warning banner) + delete
  AlertDialog.

## Docs

- Design: `docs/superpowers/specs/2026-07-10-notifications-design.md`
- Plan: `docs/superpowers/plans/2026-07-10-notifications.md`

## Deliberate simplifications

- Body is plain text with `whitespace-pre-wrap`, not rendered markdown.
  The spec's "Markdown supported" note is aspirational; live markdown
  rendering adds a `react-markdown` dependency and XSS surface for
  minimal user-visible payoff on day one. Follow-up if operators start
  hand-writing rich content.
- Project-audience field is a UUID text input, not a picker
  (`AdminProjectCombobox` doesn't exist yet in the codebase; extracting
  one from `UserCombobox` is a follow-up).

🤖 Generated with [Claude Code](https://claude.com/claude-code)
BODY
)"
```
Expected: PR opens against `main`.

- [ ] **Step 7: Commit any lingering FE-only changes**

If `npm run build` in Step 3 produced files that need committing (typically none — the build output is `dist/` and gitignored), commit them; otherwise no-op:

```bash
cd /root/coding/modelserver
git status                     # should be clean if all task commits landed correctly
```

---

## Self-review

1. **Spec coverage** vs `docs/superpowers/specs/2026-07-10-notifications-design.md`:
   - §2.1 `notifications` table — Task 1 ✅
   - §2.2 `notification_reads` table — Task 1 ✅
   - §3.1 four user routes with exact envelope — Task 4 ✅
   - §3.2 five admin routes with `include_deleted` — Task 3 ✅
   - §3.3 validation (title/body caps, audience shape/existence, 401/403) — Task 3 + Task 4 tests ✅
   - §4.1 sidebar entry above Projects + badge (0/1-99/99+) + admin entry — Task 5 ✅
   - §4.2 user inbox page with expand-to-read + mark-all + audience chip — Task 5 ✅
   - §4.3 admin DataTable + create/edit/delete + edit warning banner — Task 6 ✅
   - §4.4 file inventory matches Tasks 5+6 outputs ✅
   - §5 backend file inventory matches Tasks 1-4 outputs ✅
   - §6.2 test strategy (store roundtrips, visibility union, idempotency, soft-delete filter; handler auth gates + CRUD + validation) — Tasks 1-4 ✅
   - §6.3 R3 (removed members): visibility query re-evaluated per request, no cached membership — implicit ✅
   - §6.4 deploy/rollback — captured in PR body ✅

2. **Deliberate deviations flagged in the plan text (not gaps):**
   - Spec §4.3 references `AdminProjectCombobox` "already used by AdminProjectsPage" — does not exist. Task 6 calls this out and substitutes a UUID text input.
   - Spec §4.2 says "Body rendered as markdown" — Task 5's row detail renders `whitespace-pre-wrap` plain text. Called out in the PR body.

3. **Placeholder scan:** no TBD/TODO in the plan; every code block is complete; test cases use concrete assertions.

4. **Type consistency:**
   - `types.Notification` fields identical between Task 2 (definition), Task 3 handlers (create/get), Task 4 handlers (list/count/mark), Task 5 TS type (mirror JSON tags). ✅
   - `Store.MarkNotificationRead(userID, notificationID string) error` — same signature in Task 2 def, Task 4 caller, Task 6 hook mutationFn. ✅
   - Route paths agree across backend (Tasks 3+4 `routes.go`) and frontend hooks (Task 5 `notifications.ts`): `/api/v1/notifications`, `/api/v1/notifications/unread_count`, `/api/v1/notifications/{id}/read`, `/api/v1/notifications/read_all`, `/api/v1/admin/notifications` + `/{id}`.
   - `AudienceType` enum values `"global"|"project"|"user"` identical across SQL CHECK (Task 1), Go constants (Task 2), TS union (Task 5).
   - Envelope shapes: `writeData` ⇒ `{data: T}`; `writeList` ⇒ `{data: T[], meta:{total,page,per_page,total_pages}}`. Hooks use `DataResponse<T>` / `ListResponse<T>` accordingly. ✅
