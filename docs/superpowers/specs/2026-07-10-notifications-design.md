# Notifications — in-dashboard broadcast + per-project + per-user messages

Status: **draft — awaiting review**
Author: (assistant, drafted with user 2026-07-10)

## 1. Goal & scope

Add a **notifications** entity so operators can send in-dashboard messages
to users. Administrators create/edit/delete notifications in the admin
area; users see a new `Notifications` entry in the sidebar directly above
`Projects`, with an **unread-count badge** next to it. Opening a
notification's card auto-marks it read; the list page has a "Mark all as
read" button.

- **Three audience shapes**: global broadcast, per-project, per-user.
- **Admin permission**: `is_superadmin` (existing gate).
- **No billing / usage / quota interaction.** Pure metadata entity.
- **Channel**: in-dashboard only. No email, no webhook (design leaves
  room for it — see §6.1).

## 2. Data model

### 2.1 `notifications`

```sql
CREATE TABLE notifications (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title         TEXT NOT NULL,
    body          TEXT NOT NULL,            -- markdown
    audience_type TEXT NOT NULL,            -- 'global' | 'project' | 'user'
    audience_id   UUID,                     -- NULL when audience_type='global'
    created_by    UUID NOT NULL REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ,              -- NULL = alive; soft delete
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
```

- **Soft delete** (`deleted_at`): admin "Delete" sets `deleted_at`; every
  user-facing query filters `WHERE deleted_at IS NULL`. History of
  `notification_reads` is preserved for audit / potential undelete.
- **Edit semantics**: `PUT /admin/notifications/{id}` may change title,
  body, `audience_type`, `audience_id`. Editing does NOT clear any
  user's read row. `updated_at` is bumped so the admin UI can display
  "last edited at". A yellow warning banner on the edit dialog makes
  this explicit: "Users who have already read this notification will
  not see the update — delete and recreate if you need to reach them."

### 2.2 `notification_reads`

```sql
CREATE TABLE notification_reads (
    notification_id UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (notification_id, user_id)
);
CREATE INDEX idx_notification_reads_user ON notification_reads (user_id);
```

- Row present = read; row absent = unread.
- `INSERT ... ON CONFLICT DO NOTHING` makes "mark as read" idempotent.
- Soft-deleting a notification does NOT cascade (no rows change);
  hard-delete would (kept out of MVP — see §5 route table).

## 3. Backend API

All routes live inside the existing admin-server chi router
(`internal/admin/routes.go`), inside the group protected by
`JWTAuthMiddleware`. Distinguishing user-facing vs admin-only is done by
route prefix (`/notifications` vs `/admin/notifications`) and by
`RequireSuperadmin` middleware on the admin subgroup — mirroring the
existing pattern for `/projects` (any authenticated) vs `/admin/projects`
(superadmin only).

### 3.1 User-facing routes

| Method | Path | Purpose |
|---|---|---|
| GET  | `/notifications?cursor=<created_at>&limit=50` | Paginated (DESC) list of notifications visible to `$me`: broadcast ∪ project I'm in ∪ addressed to me. LEFT JOIN reads to attach `read_at`. |
| GET  | `/notifications/unread_count` | Returns `{count: N}`. Sidebar badge polls at 45 s. |
| POST | `/notifications/{id}/read` | Idempotent `INSERT ... ON CONFLICT DO NOTHING` into reads. Always 200 empty — a request against a soft-deleted or invisible notification is a silent no-op, avoiding a confusing 404 when the frontend races an admin delete. The FK on `notification_reads` still guards against inserting against a hard-deleted row. |
| POST | `/notifications/read_all` | Bulk `INSERT ... ON CONFLICT DO NOTHING` for every currently visible unread notification. Returns `{marked: N}`. |

**Visibility for `$me`** (used identically by the list, unread_count, and
read_all endpoints — factored into one Store helper):

```
    audience_type = 'global'
 OR (audience_type = 'project'
     AND audience_id IN (SELECT project_id FROM project_members
                         WHERE user_id = $me AND removed_at IS NULL))
 OR (audience_type = 'user' AND audience_id = $me)
```

Each returned item carries `id, title, body, audience_type, audience_id,
audience_name, created_at, updated_at, read_at` (`read_at = null` ⇒
unread). `audience_name` is the display string the frontend renders as a
per-item badge:

- `"Broadcast"` for global,
- the project's `display_name` for project,
- `"You"` for user (never the target user's email — the caller and target
  are the same person by definition).

Populated by a `LEFT JOIN projects ON …` on the query side, no extra
round trip.

### 3.2 Admin routes (`RequireSuperadmin`)

| Method | Path | Purpose |
|---|---|---|
| GET    | `/admin/notifications?cursor=&limit=&audience_type=&include_deleted=` | Paginated (DESC) list of every notification. `include_deleted=1` includes soft-deleted rows; default excludes them. Each item carries `read_count` (`SELECT COUNT(*) FROM notification_reads`). |
| GET    | `/admin/notifications/{id}` | Single row with `read_count`. |
| POST   | `/admin/notifications` | Body: `{title, body, audience_type, audience_id?}`. `created_by` = `$me`. |
| PUT    | `/admin/notifications/{id}` | Same body shape. Bumps `updated_at`. Does NOT touch reads. |
| DELETE | `/admin/notifications/{id}` | Sets `deleted_at = NOW()`. Returns 200 empty. |

Hard delete is deliberately not exposed in the MVP (guards against
oops-clicks; `notification_reads` audit trail survives soft delete).

### 3.3 Validation & errors

- `audience_type='project'`: `audience_id` must point to an existing
  non-archived project. Otherwise 400 with `code=invalid_audience`.
- `audience_type='user'`: `audience_id` must point to an existing
  non-deleted user. Otherwise 400 with `code=invalid_audience`.
- `title`: 1–200 chars. `body`: 1–20 000 chars.
- Unauthenticated calls → 401 (existing `JWTAuthMiddleware`).
- Non-superadmin hitting `/admin/notifications/*` → 403 (existing
  `RequireSuperadmin`).

## 4. Frontend UX

### 4.1 Sidebar entry

`dashboard/src/components/layout/Sidebar.tsx` gains a new `SidebarLink`
above the current `Projects` link:

```tsx
<SidebarLink to="/notifications" icon={Bell} badge={unreadCount}>
  Notifications
</SidebarLink>
<SidebarLink to="/projects" icon={FolderOpen}>Projects</SidebarLink>
```

`SidebarLink` grows an optional `badge?: number` prop. Render rules:

- `undefined` / `0` / `null` → no badge rendered.
- `1–99` → the number.
- `≥ 100` → `99+`.

Styling: right-aligned pill, `bg-primary text-primary-foreground
text-[10px] px-1.5 py-0.5 rounded-full`, consistent with existing
dashboard chip patterns.

**Data source:** `useUnreadNotificationCount()` React Query hook against
`GET /notifications/unread_count`, `refetchInterval: 45_000`, `staleTime:
30_000` (avoid a re-fetch each time the tab regains focus mid-window).
The hook is invoked once at the top of `Sidebar.tsx`.

**Admin sidebar section** also gets a `SidebarLink to="/admin/notifications"
icon={Bell}` at the end of the existing admin block, gated by
`user?.is_superadmin`.

### 4.2 User page `/notifications`

New file `dashboard/src/pages/notifications/NotificationsPage.tsx` — a
single-column time-ordered list of expandable cards:

```
┌──────────────────────────────────────────────────────────┐
│                      [Mark all as read]  ← top-right,     │
│                                                 unread>0  │
├──────────────────────────────────────────────────────────┤
│ ● Title of notification #1              [Project: Acme]  │
│                                              2 days ago   │
│ ─── (expanded) ───                                        │
│  Body rendered as markdown here.                          │
├──────────────────────────────────────────────────────────┤
│   Title of notification #2               [Broadcast]      │
│                                              a week ago   │
└──────────────────────────────────────────────────────────┘
```

- Unread rows: leading `●` dot + slightly bolder title. Read rows: no
  dot, standard weight.
- Click a row → toggle expanded (local UI state). On first expand of an
  unread row → `POST /notifications/{id}/read`, then mutate the local
  React Query cache to set `read_at`, and invalidate the
  `unread_count` query so the sidebar badge decrements immediately.
- "Mark all as read" button → `POST /notifications/read_all` → single
  invalidation of the list + unread_count queries.
- Empty state: "No notifications yet".
- Pagination: cursor-based "Load more" button at page end, matching the
  idiom used by `RequestsPage.tsx` / `TracesPage.tsx`. No infinite
  scroll (avoids extra polling & badge/window interaction).

### 4.3 Admin page `/admin/notifications`

New file `dashboard/src/pages/admin/NotificationsPage.tsx`. Uses the
standard `DataTable` + `Dialog` combo already used by `PlansPage.tsx`
and `AdminProjectsPage.tsx`.

Columns:

- Title (click → edit dialog)
- Audience (`Broadcast` / `Project: <name>` / `User: <email>`)
- `read_count` (e.g. "42 reads")
- Created at
- Actions dropdown: Edit / Delete

Top of page: `[+ New Notification]` button.

Create/Edit dialog:

- Title input
- Body textarea (with helper text "Markdown supported")
- Audience type radio: `Global` / `Project` / `User`, wired to reveal:
  - Global → nothing
  - Project → existing `AdminProjectCombobox` (already used by
    `AdminProjectsPage`)
  - User → existing `UserCombobox`
- On edit dialog, a yellow alert at top: `⚠ Editing does not re-notify
  users who have already read this notification. Delete and create anew
  if you need to reach them again.`

Delete confirmation: standard `AlertDialog` with body "Delete this
notification? Users who haven't read it yet will no longer see it. Read
history is preserved for audit."

### 4.4 Component & file inventory

**Frontend new files**

- `dashboard/src/pages/notifications/NotificationsPage.tsx`
- `dashboard/src/pages/admin/NotificationsPage.tsx`
- `dashboard/src/api/notifications.ts` — React Query hooks:
  `useNotifications`, `useUnreadNotificationCount`, `useMarkAsRead`,
  `useMarkAllAsRead`, `useAdminNotifications`, `useCreateNotification`,
  `useUpdateNotification`, `useDeleteNotification`.

**Frontend modifications**

- `dashboard/src/api/types.ts` — add `Notification`, `AudienceType`,
  `NotificationListResponse`.
- `dashboard/src/App.tsx` — two new `<Route>` entries.
- `dashboard/src/components/layout/Sidebar.tsx` — new user entry above
  Projects, new admin entry, `SidebarLink` learns `badge?: number`.

## 5. Backend file inventory

**New files**

- `internal/store/migrations/064_notifications.sql`
- `internal/types/notification.go` — struct + audience constants
  (`AudienceTypeGlobal = "global"`, etc.)
- `internal/store/notifications.go` — CRUD + reads + the shared
  visibility query.
- `internal/store/notifications_test.go`
- `internal/admin/handle_notifications.go` — both user-facing and
  admin-only handlers (mirrors `handle_projects.go`, which houses both
  project-scoped and superadmin-only handlers).
- `internal/admin/handle_notifications_test.go`

**Modifications**

- `internal/admin/routes.go` — mount the two route subtrees inside the
  existing authenticated group; add `RequireSuperadmin` on the admin
  subtree.

Route mount (excerpt, matches §3.1/§3.2 exactly):

```go
// User-facing notifications inbox (any authenticated user).
r.Route("/notifications", func(r chi.Router) {
    r.Get("/",              handleListMyNotifications(st))
    r.Get("/unread_count",  handleUnreadNotificationCount(st))
    r.Post("/{id}/read",    handleMarkNotificationRead(st))
    r.Post("/read_all",     handleMarkAllNotificationsRead(st))
})

// Admin notifications (superadmin only).
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

## 6. Extensibility, testing, risks

### 6.1 Reserved for later — NOT built in this iteration

- **Email / webhook fan-out**: `notifications` already carries the
  metadata; a future `notification_channels` table + background job
  reads `SELECT ... WHERE created_at > last_dispatch_at` and pushes.
  No schema break.
- **Severity level** (`info` / `warning` / `critical`): future `level`
  column defaulting to `'info'`; frontend swaps badge color/icon by
  level. YAGNI for now.
- **Action URL**: future `action_url TEXT` column. Meanwhile the
  markdown body can contain links.
- **Project-archived filter**: current query does NOT filter out
  notifications whose target project has been archived. Deliberate — a
  removed-then-restored user or an archived-then-restored project still
  sees historical messages. Change if a real use case surfaces.

### 6.2 Testing strategy

**Store** (`notifications_test.go`)

- Create + Get + soft-delete round trip.
- Visibility resolver: user A in projects [P1, P2]; notifications
  spanning global, P1 (visible), P3 (not), addressed to A (visible),
  addressed to B (not). Assert list matches expected set for A.
- `mark_read` idempotency: two consecutive POSTs, only one row.
- `read_all`: leaves already-read rows untouched, inserts for all
  currently-visible unreads.
- Soft-deleted notification: absent from user list & unread_count;
  present in admin `include_deleted=1`.

**Handlers** (`handle_notifications_test.go`)

- 401 (no JWT), 403 (non-superadmin on `/admin/...`), 400 (audience_id
  missing or wrong shape), 404 (mark_read on invisible or deleted
  notification).
- Pagination cursor round-trips deterministically.

**Frontend**: no unit tests (dashboard has none as a convention);
manual verification + PR review.

### 6.3 Risks

- **R1 — Cost of `unread_count` on high-broadcast systems.** The SQL
  filters via the three partial indexes then LEFT JOINs `reads`. At
  <1000 alive notifications and <10 000 users, negligible. If either
  bound is breached, add a per-user materialized summary or a
  memcache/redis cache — outside this spec's scope.
- **R2 — Edit-not-re-notifying is a footgun.** Admin dialog's yellow
  banner is the only guard. Accepted — it matches how every real
  in-dashboard notification system behaves (Slack, GitHub, Linear).
- **R3 — Members removed from a project.** `project_members.removed_at
  IS NULL` filter in §3.1 keeps them from seeing project notifications
  once ex-members.
- **R4 — Badge flicker.** `staleTime: 30_000` on the polling hook
  suppresses duplicate fetches when the tab regains focus during a
  polling window.

### 6.4 Deploy & rollback

- **Deploy**: merge → `docker compose up -d`. `store.Migrate` runs 064
  on startup, table is empty, no user impact until an admin sends the
  first notification. Frontend assets ship with next dashboard build.
- **Rollback (soft)**: comment out the Notifications sidebar link;
  admin routes still exist but nothing surfaces to end users.
- **Rollback (hard)**: `065_drop_notifications.sql` DROPs both tables.
  Safe — no other table references them.
