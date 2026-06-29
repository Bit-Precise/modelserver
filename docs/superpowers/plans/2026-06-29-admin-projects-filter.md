# Admin Projects List — Project-ID + Owner Filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add project_id (exact UUID) and owner (Combobox with avatar+nickname+email) filters to the `/admin/projects` page. Filters AND-combine, persist in URL search params, share one Clear button. Owner semantics = `project.created_by`.

**Architecture:** Backend `ListAllProjects` gains a `ProjectListFilters{ProjectID, CreatedBy}` parameter — empty fields skip the corresponding `WHERE` clause. Admin handler reads `?project_id=` and `?owner=` from the query string, validates UUIDs, forwards to the store. Existing `userCompact` JSON struct gains `email` and `picture` so the new `UserCombobox` can render a 3-line row per user. Frontend extends `useAllProjects` with a filters arg, syncs filter state to the URL via `useSearchParams`, and renders a `UserCombobox` mirroring the existing `ModelCombobox` pattern (Popover + Input + own filter loop).

**Tech Stack:** Go 1.x, `pgx`, stdlib `testing`, `github.com/google/uuid` (already a dependency). React 19, `@tanstack/react-query` v5, `react-router` v7 `useSearchParams`, Tailwind v4. No new dependencies. No SQL migrations.

## Global Constraints

- **Spec:** `docs/superpowers/specs/2026-06-29-admin-projects-filter-design.md` — re-read before each task.
- **Owner semantics:** `project.created_by` (immutable creator), NOT the role-owner from `project_members`. Spec explicitly distinguishes the two.
- **Match semantics:** project_id is EXACT UUID match (`WHERE id = $N`). No prefix / no `ILIKE`. owner is also exact UUID match.
- **Validation:** invalid UUID in either query param → BE returns 400 with `"invalid project_id: not a UUID"` or `"invalid owner: not a UUID"`.
- **Empty filter handling:** empty string treated as "no filter" — never passed into the SQL. Both BE and FE must normalize empty to absent.
- **Backward compatibility:** old dashboard sends no filter params → BE returns full list as today (no behavior change). New `email`/`picture` fields on `userCompact` are additive; old FE callers ignore them.
- **No schema migrations.** Pure additive Go + TS changes.
- **No frontend test framework** — dashboard tasks verify via `pnpm exec tsc -b && pnpm build` + manual smoke.
- **Commit message footer:** every commit ends with
  ```
  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  ```

## File Structure

**Backend — modify:**
- `internal/store/projects.go` — extend `ListAllProjects` with `ProjectListFilters` arg.
- `internal/store/projects_test.go` — new tests for filter behavior.
- `internal/store/users.go` — extend `CompactUser` + `ListAllUsersCompact` SQL.
- `internal/admin/handle_projects.go` — extend `handleListAllProjects` with query-param parsing + UUID validation.
- `internal/admin/handle_auth.go` — extend `userCompact` JSON struct + handler.

**Frontend — create:**
- `dashboard/src/components/shared/UserCombobox.tsx` — single-select user picker showing avatar + nickname + email.

**Frontend — modify:**
- `dashboard/src/api/users.ts` — `UserCompact` interface adds `email?` + `picture?`.
- `dashboard/src/api/projects.ts` — `useAllProjects` accepts `{projectId?, ownerId?}`.
- `dashboard/src/pages/admin/ProjectsPage.tsx` — filter UI + URL state.

---

### Task 1: Backend store — `ListAllProjects` filters + `CompactUser` email/picture

**Files:**
- Modify: `internal/store/projects.go` (add `ProjectListFilters` struct + extend `ListAllProjects` signature + WHERE construction)
- Modify: `internal/store/projects_test.go` (5 new test cases — or create if file doesn't exist)
- Modify: `internal/store/users.go` (`CompactUser` struct + `ListAllUsersCompact` SQL)

**Interfaces:**
- Consumes: existing `types.Project`, `types.PaginationParams`, `pgx.Conn` pool, `sanitizeSort`/`sanitizeOrder` helpers.
- Produces:
  ```go
  // internal/store/projects.go
  type ProjectListFilters struct {
      ProjectID string // exact UUID match; "" = no filter on this field
      CreatedBy string // exact UUID match; "" = no filter
  }
  func (s *Store) ListAllProjects(p types.PaginationParams, filters ProjectListFilters) ([]types.Project, int, error)

  // internal/store/users.go
  type CompactUser struct {
      ID       string
      Nickname string
      Email    string  // NEW
      Picture  string  // NEW
  }
  // ListAllUsersCompact signature unchanged; row shape extended.
  ```

This is a backward-incompatible change to `ListAllProjects`. The only in-tree caller is `internal/admin/handle_projects.go`'s `handleListAllProjects` (Task 2). The signature change ripples to 1 caller in 1 commit; no compat wrapper needed.

- [ ] **Step 1: Write failing tests for `ListAllProjects` filters**

In `internal/store/projects_test.go` (create the file if it doesn't exist; if it does, append):

```go
package store

import (
	"context"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

// seedProjectOwnedBy inserts a project with the given created_by user.
// Uses the existing seed helpers from extra_usage_db_test.go.
func seedProjectOwnedBy(t *testing.T, st *Store, name, createdBy string) string {
	t.Helper()
	var id string
	if err := st.pool.QueryRow(context.Background(), `
		INSERT INTO projects (name, created_by, status)
		VALUES ($1, $2, 'active')
		RETURNING id`, name, createdBy).Scan(&id); err != nil {
		t.Fatalf("seed project %s: %v", name, err)
	}
	return id
}

func TestListAllProjects_NoFilters_ReturnsAll(t *testing.T) {
	st := openTestStore(t)
	ownerA, _ := seedUserAndProject(t, st) // creates a project too — that one will be counted
	pid1 := seedProjectOwnedBy(t, st, "list-all-1", ownerA)
	pid2 := seedProjectOwnedBy(t, st, "list-all-2", ownerA)

	got, total, err := st.ListAllProjects(types.PaginationParams{Page: 1, PageSize: 100}, ProjectListFilters{})
	if err != nil {
		t.Fatalf("ListAllProjects: %v", err)
	}
	if total < 2 {
		t.Errorf("total = %d, want >= 2 (we seeded at least 2 plus the auto-created one)", total)
	}
	ids := map[string]bool{}
	for _, p := range got {
		ids[p.ID] = true
	}
	if !ids[pid1] || !ids[pid2] {
		t.Errorf("seeded projects missing from list: pid1=%v, pid2=%v in %v", ids[pid1], ids[pid2], ids)
	}
}

func TestListAllProjects_FilterByProjectID(t *testing.T) {
	st := openTestStore(t)
	ownerA, _ := seedUserAndProject(t, st)
	target := seedProjectOwnedBy(t, st, "filter-by-id-target", ownerA)
	_ = seedProjectOwnedBy(t, st, "filter-by-id-other", ownerA)

	got, total, err := st.ListAllProjects(
		types.PaginationParams{Page: 1, PageSize: 100},
		ProjectListFilters{ProjectID: target},
	)
	if err != nil {
		t.Fatalf("ListAllProjects: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (single ID match)", total)
	}
	if len(got) != 1 || got[0].ID != target {
		t.Errorf("got = %v, want exactly [%s]", got, target)
	}
}

func TestListAllProjects_FilterByCreatedBy(t *testing.T) {
	st := openTestStore(t)
	ownerA, _ := seedUserAndProject(t, st)
	ownerB := seedSecondUser(t, st, "owner-b")
	a1 := seedProjectOwnedBy(t, st, "owner-a-proj-1", ownerA)
	a2 := seedProjectOwnedBy(t, st, "owner-a-proj-2", ownerA)
	b1 := seedProjectOwnedBy(t, st, "owner-b-proj-1", ownerB)

	got, total, err := st.ListAllProjects(
		types.PaginationParams{Page: 1, PageSize: 100},
		ProjectListFilters{CreatedBy: ownerB},
	)
	if err != nil {
		t.Fatalf("ListAllProjects: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (one project for ownerB)", total)
	}
	if len(got) != 1 || got[0].ID != b1 {
		t.Errorf("got = %v, want [%s]", got, b1)
	}
	// Confirm we did NOT see ownerA's projects.
	for _, p := range got {
		if p.ID == a1 || p.ID == a2 {
			t.Errorf("ownerA project %s leaked into ownerB filter", p.ID)
		}
	}
}

func TestListAllProjects_FilterByBoth_IntersectsAND(t *testing.T) {
	st := openTestStore(t)
	ownerA, _ := seedUserAndProject(t, st)
	ownerB := seedSecondUser(t, st, "owner-b-both")
	a1 := seedProjectOwnedBy(t, st, "both-a-1", ownerA)
	_ = seedProjectOwnedBy(t, st, "both-b-1", ownerB)

	// Filter by a1's ID AND ownerB → should be empty (mismatch).
	_, total, err := st.ListAllProjects(
		types.PaginationParams{Page: 1, PageSize: 100},
		ProjectListFilters{ProjectID: a1, CreatedBy: ownerB},
	)
	if err != nil {
		t.Fatalf("ListAllProjects: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0 (id and owner don't match same row)", total)
	}

	// Filter by a1's ID AND ownerA → should match.
	got, total, err := st.ListAllProjects(
		types.PaginationParams{Page: 1, PageSize: 100},
		ProjectListFilters{ProjectID: a1, CreatedBy: ownerA},
	)
	if err != nil {
		t.Fatalf("ListAllProjects: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].ID != a1 {
		t.Errorf("both-match: total=%d got=%v want=[%s]", total, got, a1)
	}
}

func TestListAllProjects_FilterEmptyMatchReturnsZero(t *testing.T) {
	st := openTestStore(t)
	// Filter by a UUID that doesn't exist in the table.
	_, total, err := st.ListAllProjects(
		types.PaginationParams{Page: 1, PageSize: 100},
		ProjectListFilters{ProjectID: "00000000-0000-0000-0000-000000000000"},
	)
	if err != nil {
		t.Fatalf("ListAllProjects: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0 (unmatched filter)", total)
	}
}
```

The test uses `openTestStore`, `seedUserAndProject`, and `seedSecondUser` from existing test helpers (`extra_usage_db_test.go` / `projects_test.go` if already there). Verify they exist before relying on them; if `seedSecondUser` is in a different file, import-by-package-membership works since these are all in `package store`.

- [ ] **Step 2: Run tests — verify they fail with "undefined: ProjectListFilters"**

```bash
cd /root/coding/modelserver && go test ./internal/store/ -run TestListAllProjects -v
```

Expected (without TEST_DATABASE_URL: SKIP. With it: build error — `ProjectListFilters` is undefined, and `ListAllProjects` takes only 1 arg today.

- [ ] **Step 3: Implement `ProjectListFilters` + new `ListAllProjects` body**

In `internal/store/projects.go`, replace the existing `ListAllProjects` function with:

```go
// ProjectListFilters narrows ListAllProjects. Empty fields are ignored
// (= no filter on that dimension). Used by the admin /projects list
// page to support project-id and created-by filtering. Both empty =
// behaves identically to today's no-filter call.
type ProjectListFilters struct {
	ProjectID string // exact match against projects.id (UUID as string)
	CreatedBy string // exact match against projects.created_by (UUID as string)
}

// ListAllProjects returns projects with pagination (for superadmin).
// `filters` narrows by project ID and/or creator; empty fields mean no
// filter. Both COUNT and the data SELECT share the same WHERE so total
// reflects the filtered set.
func (s *Store) ListAllProjects(p types.PaginationParams, filters ProjectListFilters) ([]types.Project, int, error) {
	ctx := context.Background()

	// Build WHERE clause dynamically so pgx can use positional args
	// without struct-tag plumbing. Numbering starts at $1 and increments
	// per added clause.
	var where strings.Builder
	where.WriteString("WHERE 1=1")
	args := make([]any, 0, 4)
	if filters.ProjectID != "" {
		args = append(args, filters.ProjectID)
		fmt.Fprintf(&where, " AND id = $%d", len(args))
	}
	if filters.CreatedBy != "" {
		args = append(args, filters.CreatedBy)
		fmt.Fprintf(&where, " AND created_by = $%d", len(args))
	}

	var total int
	if err := s.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM projects %s`, where.String()),
		args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count projects: %w", err)
	}

	// Append LIMIT / OFFSET positional args AFTER the WHERE args so the
	// numbering stays contiguous.
	limitArg := len(args) + 1
	offsetArg := len(args) + 2
	args = append(args, p.Limit(), p.Offset())

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT `+projectColumns+`
		FROM projects
		%s
		ORDER BY %s %s LIMIT $%d OFFSET $%d`,
		where.String(),
		sanitizeSort(p.Sort, "created_at"), sanitizeOrder(p.Order),
		limitArg, offsetArg,
	), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list all projects: %w", err)
	}
	defer rows.Close()

	var projects []types.Project
	for rows.Next() {
		proj, err := scanProject(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, *proj)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate projects: %w", err)
	}
	return projects, total, nil
}
```

Add `"strings"` to the imports if it isn't already there (`projects.go` likely doesn't use it yet — grep first).

- [ ] **Step 4: Run focused tests — verify they pass**

```bash
cd /root/coding/modelserver && go test ./internal/store/ -run TestListAllProjects -v
```

Expected (with TEST_DATABASE_URL): all 5 PASS. Without it: SKIP.

- [ ] **Step 5: Extend `CompactUser` + `ListAllUsersCompact`**

In `internal/store/users.go`:

Change the `CompactUser` struct (around line 76):

```go
// CompactUser is a minimal user row for filter dropdowns.
// Email and Picture support filter dropdowns that need to identify
// users by their addressable handle (email) or visual avatar.
type CompactUser struct {
	ID       string
	Nickname string
	Email    string
	Picture  string
}
```

Change `ListAllUsersCompact` (around line 86) to SELECT and scan the two new columns:

```go
func (s *Store) ListAllUsersCompact() ([]CompactUser, error) {
	rows, err := s.pool.Query(context.Background(),
		`SELECT id, COALESCE(nickname, ''), COALESCE(email, ''), COALESCE(picture, '') FROM users ORDER BY nickname, id`)
	if err != nil {
		return nil, fmt.Errorf("list users compact: %w", err)
	}
	defer rows.Close()

	var out []CompactUser
	for rows.Next() {
		var u CompactUser
		if err := rows.Scan(&u.ID, &u.Nickname, &u.Email, &u.Picture); err != nil {
			return nil, fmt.Errorf("scan compact user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
```

- [ ] **Step 6: Build entire project + run store tests**

```bash
cd /root/coding/modelserver && go build ./... && go test ./internal/store/...
```

Expected: build FAILS in `internal/admin/handle_projects.go` because `handleListAllProjects` still calls the old 1-arg `ListAllProjects`. That's expected — Task 2 fixes it. Store tests pass (or SKIP cleanly).

If the build fails anywhere ELSE besides `handle_projects.go`, stop and debug — only the documented caller should break.

- [ ] **Step 7: Commit**

```bash
cd /root/coding/modelserver
git add internal/store/projects.go internal/store/projects_test.go internal/store/users.go
git commit -m "feat(store): ListAllProjects filter args + CompactUser email/picture

ListAllProjects gains a ProjectListFilters parameter (ProjectID,
CreatedBy); empty fields skip the corresponding WHERE clause. COUNT
and SELECT share the WHERE so total reflects the filtered set.

CompactUser + ListAllUsersCompact return email and picture so admin
filter dropdowns can render a 3-line user row.

Signature change to ListAllProjects breaks internal/admin/handle_projects.go
intentionally; the admin handler is updated in the next commit.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Admin handlers — query-param validation + `userCompact` JSON enrichment

**Files:**
- Modify: `internal/admin/handle_projects.go` (extend `handleListAllProjects`)
- Modify: `internal/admin/handle_auth.go` (extend `userCompact` JSON struct + handler)

**Interfaces:**
- Consumes: `store.ProjectListFilters` (Task 1), `store.CompactUser` extended fields (Task 1), `github.com/google/uuid` for parse validation.
- Produces:
  - `GET /api/v1/admin/projects?project_id=…&owner=…` validates query params (400 on invalid UUID), forwards to `ListAllProjects`.
  - `GET /api/v1/users/compact` response rows include `email` and `picture` JSON fields when populated (omitempty).

- [ ] **Step 1: Extend `handleListAllProjects`**

In `internal/admin/handle_projects.go`, replace the existing `handleListAllProjects` body:

```go
func handleListAllProjects(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := parsePagination(r)

		var filters store.ProjectListFilters
		if v := r.URL.Query().Get("project_id"); v != "" {
			if _, err := uuid.Parse(v); err != nil {
				writeError(w, http.StatusBadRequest, "bad_request", "invalid project_id: not a UUID")
				return
			}
			filters.ProjectID = v
		}
		if v := r.URL.Query().Get("owner"); v != "" {
			if _, err := uuid.Parse(v); err != nil {
				writeError(w, http.StatusBadRequest, "bad_request", "invalid owner: not a UUID")
				return
			}
			filters.CreatedBy = v
		}

		projects, total, err := st.ListAllProjects(p, filters)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to list projects")
			return
		}
		writeList(w, projects, total, p.Page, p.Limit())
	}
}
```

Add `"github.com/google/uuid"` to the file's import block if it isn't already imported.

- [ ] **Step 2: Extend `userCompact` JSON struct and handler**

In `internal/admin/handle_auth.go`, find `userCompact` (around line 235) and replace:

```go
// userCompact is the minimal shape returned by /users/compact for
// dropdown population. Includes email + picture so filter dropdowns
// can render avatar + nickname + email per row (used by the admin
// projects-list owner filter).
type userCompact struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname,omitempty"`
	Email    string `json:"email,omitempty"`
	Picture  string `json:"picture,omitempty"`
}
```

And update `handleListUsersCompact` to copy the new fields:

```go
func handleListUsersCompact(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		users, err := st.ListAllUsersCompact()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to list users")
			return
		}
		out := make([]userCompact, 0, len(users))
		for _, u := range users {
			out = append(out, userCompact{
				ID:       u.ID,
				Nickname: u.Nickname,
				Email:    u.Email,
				Picture:  u.Picture,
			})
		}
		writeData(w, http.StatusOK, out)
	}
}
```

- [ ] **Step 3: Build + run admin tests**

```bash
cd /root/coding/modelserver && go build ./... && go test ./internal/admin/...
```

Expected: build green; existing admin tests PASS (the change is additive on the JSON output side and validation-only on the handler side; no existing test sends invalid UUIDs).

- [ ] **Step 4: Commit**

```bash
cd /root/coding/modelserver
git add internal/admin/handle_projects.go internal/admin/handle_auth.go
git commit -m "feat(admin): /admin/projects accepts ?project_id and ?owner filters

handleListAllProjects parses project_id and owner from query string,
validates each as a UUID (400 on malformed input), forwards to
store.ListAllProjects via ProjectListFilters.

userCompact JSON gains email and picture fields (omitempty) so the
admin projects filter dropdown can render avatar + nickname + email
per user row. Empty defaults preserve backward-compatibility for any
existing consumer.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Frontend types + hooks + UserCombobox component

**Files:**
- Modify: `dashboard/src/api/users.ts` (`UserCompact` interface adds `email?`, `picture?`)
- Modify: `dashboard/src/api/projects.ts` (`useAllProjects` accepts `{projectId?, ownerId?}`)
- Create: `dashboard/src/components/shared/UserCombobox.tsx`

**Interfaces:**
- Consumes: extended `/users/compact` JSON shape (Task 2); extended `/admin/projects?project_id=&owner=` (Task 2); existing `Popover` / `Input` / `Button` / `Avatar` primitives.
- Produces:
  ```ts
  // users.ts
  export interface UserCompact {
    id: string;
    nickname?: string;
    email?: string;     // NEW
    picture?: string;   // NEW
  }

  // projects.ts
  export interface AllProjectsFilters {
    projectId?: string;
    ownerId?: string;
  }
  export function useAllProjects(page?: number, perPage?: number, filters?: AllProjectsFilters): ...

  // UserCombobox.tsx
  interface UserComboboxProps {
    value: string | null;
    onChange: (userId: string | null) => void;
    placeholder?: string;
  }
  export function UserCombobox(props: UserComboboxProps): JSX.Element
  ```

- [ ] **Step 1: Extend `UserCompact` interface**

In `dashboard/src/api/users.ts`, find the existing interface (around line 11) and replace:

```ts
export interface UserCompact {
  id: string;
  nickname?: string;
  email?: string;
  picture?: string;
}
```

- [ ] **Step 2: Extend `useAllProjects` with filters**

In `dashboard/src/api/projects.ts`, find the existing `useAllProjects` hook. Replace it with:

```ts
export interface AllProjectsFilters {
  projectId?: string;
  ownerId?: string;
}

export function useAllProjects(
  page = 1,
  perPage = 20,
  filters: AllProjectsFilters = {},
) {
  const projectId = filters.projectId ?? "";
  const ownerId = filters.ownerId ?? "";
  const qs = new URLSearchParams({
    page: String(page),
    per_page: String(perPage),
  });
  if (projectId) qs.set("project_id", projectId);
  if (ownerId) qs.set("owner", ownerId);
  return useQuery({
    queryKey: ["admin", "all-projects", page, perPage, projectId, ownerId],
    queryFn: () =>
      api.get<ListResponse<Project>>(`/api/v1/admin/projects?${qs.toString()}`),
  });
}
```

If the existing hook had any extra options (e.g. `keepPreviousData`, `staleTime`), preserve them. The query key MUST include both filter values so React Query keeps separate cache slices per filter combination.

If `URLSearchParams` isn't already imported (it shouldn't need an import — it's a global), no further import changes needed.

- [ ] **Step 3: Create `UserCombobox` component**

Create `dashboard/src/components/shared/UserCombobox.tsx`:

```tsx
import { useMemo, useState } from "react";
import { useAllUsersCompact, type UserCompact } from "@/api/users";
import { Avatar, AvatarImage, AvatarFallback } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Check, ChevronsUpDown, X } from "lucide-react";
import { cn } from "@/lib/utils";

interface UserComboboxProps {
  value: string | null;
  onChange: (userId: string | null) => void;
  placeholder?: string;
  className?: string;
}

// UserCombobox renders a single-select user picker showing avatar +
// nickname + email per row. Substring match runs on the client over
// the pre-fetched useAllUsersCompact() result. Selecting null clears
// the selection.
export function UserCombobox({
  value,
  onChange,
  placeholder = "Filter by owner",
  className,
}: UserComboboxProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const { data, isLoading } = useAllUsersCompact();
  const users = data?.data ?? [];

  const selected = useMemo(
    () => (value ? users.find((u) => u.id === value) ?? null : null),
    [users, value],
  );

  const filtered = useMemo(() => {
    if (!query) return users;
    const q = query.toLowerCase();
    return users.filter(
      (u) =>
        u.nickname?.toLowerCase().includes(q) ||
        u.email?.toLowerCase().includes(q) ||
        u.id.toLowerCase().includes(q),
    );
  }, [users, query]);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button
            type="button"
            variant="outline"
            role="combobox"
            className={cn("justify-between font-normal", className)}
          />
        }
      >
        {selected ? (
          <UserRow user={selected} compact />
        ) : (
          <span className="text-muted-foreground">{placeholder}</span>
        )}
        <span className="ml-2 flex items-center gap-1">
          {selected ? (
            <X
              className="h-3 w-3 opacity-60 hover:opacity-100"
              onClick={(e) => {
                e.stopPropagation();
                onChange(null);
              }}
            />
          ) : null}
          <ChevronsUpDown className="h-4 w-4 shrink-0 opacity-50" />
        </span>
      </PopoverTrigger>
      <PopoverContent className="p-0 w-[320px]" align="start">
        <div className="border-b p-2">
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search by name or email…"
            autoFocus
          />
        </div>
        <div className="max-h-72 overflow-y-auto">
          {isLoading ? (
            <div className="p-3 text-xs text-muted-foreground">Loading users…</div>
          ) : filtered.length === 0 ? (
            <div className="p-3 text-xs text-muted-foreground">No users found.</div>
          ) : (
            filtered.map((u) => {
              const isSelected = value === u.id;
              return (
                <button
                  key={u.id}
                  type="button"
                  className={cn(
                    "flex w-full items-center gap-2 px-2 py-1.5 text-left hover:bg-accent",
                    isSelected && "bg-accent",
                  )}
                  onClick={() => {
                    onChange(u.id);
                    setOpen(false);
                  }}
                >
                  <UserRow user={u} />
                  {isSelected ? (
                    <Check className="ml-auto h-4 w-4" />
                  ) : null}
                </button>
              );
            })
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}

function UserRow({ user, compact = false }: { user: UserCompact; compact?: boolean }) {
  const fallback = (user.nickname ?? user.id).slice(0, 2).toUpperCase();
  return (
    <div className={cn("flex items-center gap-2 min-w-0", compact && "max-w-[240px]")}>
      <Avatar className="h-5 w-5 shrink-0">
        {user.picture ? <AvatarImage src={user.picture} /> : null}
        <AvatarFallback className="text-[10px]">{fallback}</AvatarFallback>
      </Avatar>
      <span className="text-sm truncate">
        {user.nickname || user.id.slice(0, 8)}
      </span>
      {user.email ? (
        <span className="text-xs text-muted-foreground truncate">{user.email}</span>
      ) : null}
    </div>
  );
}
```

Notes:
- The X clear-button uses `e.stopPropagation()` so clicking it doesn't open the Popover.
- `Check` icon shows next to the currently-selected row when the dropdown is open.
- Filter compares against nickname, email, AND id substring (so operators who happen to remember an ID can paste a partial UUID too).
- The trigger button is `variant="outline"` to match other filter controls.

If `cn` helper isn't where it's imported from in similar files, use the same path as `ModelCombobox.tsx` (`@/lib/utils`). Verify before submitting.

- [ ] **Step 4: Type-check + build**

```bash
cd /root/coding/modelserver/dashboard && pnpm exec tsc -b && pnpm build
```

Expected: both green. No new dependencies; reuses existing `Avatar`, `Popover`, `Button`, `Input`, `lucide-react` icons.

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver
git add dashboard/src/api/users.ts dashboard/src/api/projects.ts \
        dashboard/src/components/shared/UserCombobox.tsx
git commit -m "feat(dashboard): UserCombobox + filter-aware useAllProjects

UserCompact gains email/picture so dropdowns can render avatar +
nickname + email. useAllProjects accepts AllProjectsFilters with
projectId + ownerId; query key includes the filters so React Query
keeps independent cache slices.

UserCombobox is a single-select picker built on Popover + Input +
client-side substring filter (matches nickname, email, or id prefix).
Selection clears via the X badge on the trigger. No new dependencies.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Frontend integration — filter UI + URL state on AdminProjectsPage

**Files:**
- Modify: `dashboard/src/pages/admin/ProjectsPage.tsx`

**Interfaces:**
- Consumes: `useAllProjects(page, perPage, {projectId, ownerId})` (Task 3), `UserCombobox` (Task 3), `useSearchParams` from `react-router`.
- Produces: filter UI block rendered above the projects table; URL search-params reflect the active filters.

- [ ] **Step 1: Read the existing file end-to-end**

Run:
```bash
cd /root/coding/modelserver && cat dashboard/src/pages/admin/ProjectsPage.tsx
```

The file is the page-level container that already calls `useAllProjects(page, PER_PAGE)`. Identify:
- Where `page` state lives (`useState`).
- Where the table renders (`DataTable` component).
- Where the `<PageHeader>` lives (filters will sit between PageHeader and the table).
- Whether the file already imports `useSearchParams` from `react-router` (search the imports).
- The existing `import { Input } from …` and `import { Button } from …` — usually present.

- [ ] **Step 2: Add filter state synced to URL search params**

In `dashboard/src/pages/admin/ProjectsPage.tsx`:

Add imports at the top (if not already present):
```ts
import { useSearchParams } from "react-router";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { UserCombobox } from "@/components/shared/UserCombobox";
import { X } from "lucide-react";
```

Inside the `AdminProjectsPage` component, near the top of the body (after the `useState` for `page` and before the existing `useAllProjects` call), add:

```tsx
const [searchParams, setSearchParams] = useSearchParams();
const projectId = searchParams.get("project_id") ?? "";
const ownerId = searchParams.get("owner");

const updateFilter = (key: "project_id" | "owner", value: string | null) => {
  const next = new URLSearchParams(searchParams);
  if (value && value !== "") {
    next.set(key, value);
  } else {
    next.delete(key);
  }
  setSearchParams(next, { replace: true });
  setPage(1); // reset pagination when filter changes
};

const clearFilters = () => {
  const next = new URLSearchParams(searchParams);
  next.delete("project_id");
  next.delete("owner");
  setSearchParams(next, { replace: true });
  setPage(1);
};

const hasActiveFilters = projectId !== "" || (ownerId !== null && ownerId !== "");
```

Update the `useAllProjects` call to pass filters:

```tsx
const { data: projectsData, isLoading: loadingProjects } = useAllProjects(
  page,
  PER_PAGE,
  { projectId, ownerId: ownerId ?? undefined },
);
```

- [ ] **Step 3: Add filter UI between PageHeader and the table**

Find the existing JSX `return (` block. Between the `<PageHeader … />` line and the table render (likely a `<Card>` wrapping `<DataTable …/>`), insert:

```tsx
<div className="flex items-end gap-2 flex-wrap">
  <div className="space-y-1 flex-1 min-w-[240px] max-w-md">
    <label className="text-xs text-muted-foreground">Project ID</label>
    <Input
      placeholder="Paste project UUID"
      value={projectId}
      onChange={(e) => updateFilter("project_id", e.target.value || null)}
    />
  </div>
  <div className="space-y-1 flex-1 min-w-[240px] max-w-sm">
    <label className="text-xs text-muted-foreground">Owner</label>
    <UserCombobox
      value={ownerId}
      onChange={(id) => updateFilter("owner", id)}
    />
  </div>
  {hasActiveFilters ? (
    <Button
      variant="ghost"
      size="sm"
      onClick={clearFilters}
      className="text-muted-foreground"
    >
      <X className="mr-1 h-3 w-3" />
      Clear
    </Button>
  ) : null}
</div>
```

The filter block uses `flex-wrap` so on narrow viewports the two filters stack vertically; on wide viewports they sit inline with the Clear button.

- [ ] **Step 4: Add an inline error banner for invalid-UUID responses**

The existing component likely already handles general fetch errors via the React Query result. If not, add a small banner above the table when `useAllProjects` returns an `APIError` with code `bad_request`:

```tsx
const { data: projectsData, isLoading: loadingProjects, error } = useAllProjects(
  page,
  PER_PAGE,
  { projectId, ownerId: ownerId ?? undefined },
);

// ... later in the JSX, immediately before the table:
{error && (error as Error).message ? (
  <div className="rounded border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive">
    {(error as Error).message}
  </div>
) : null}
```

(If `APIError` is exported from `@/api/client` with a `.code` field, prefer using that for a cleaner check; otherwise the message field is the fallback.)

The filter inputs stay populated when the request errors, so the operator can correct the UUID.

- [ ] **Step 5: Type-check + build**

```bash
cd /root/coding/modelserver/dashboard && pnpm exec tsc -b && pnpm build
```

Expected: both green.

- [ ] **Step 6: Manual smoke checklist (controller runs post-commit)**

1. Open `/admin/projects` — full list as today; no filter UI active.
2. Paste a valid project UUID into the Project ID field — table narrows to 1 row; URL contains `?project_id=…`.
3. Paste an invalid UUID — inline banner shows `"invalid project_id: not a UUID"`; field stays populated.
4. Clear the project_id; open Owner dropdown — Combobox loads users showing avatar + nickname + email.
5. Type in the search box — list narrows by nickname/email substring.
6. Pick an owner — table narrows; URL adds `?owner=…`.
7. Set both filters — table shows AND-intersection; pagination total reflects filtered set.
8. Click Clear — both filters reset; URL params drop; full list returns.
9. Refresh mid-filter — filter state restored from URL.
10. Manually navigate to `?project_id=&owner=` (empty values) — BE returns full list; FE normalizes empty out of the URL on next interaction.

- [ ] **Step 7: Commit**

```bash
cd /root/coding/modelserver
git add dashboard/src/pages/admin/ProjectsPage.tsx
git commit -m "feat(dashboard): admin projects filter UI (project_id + owner)

Two filter controls between the page header and the table: an Input
for project_id and a UserCombobox for owner. State synced to URL
search params (?project_id=&owner=); refresh and back-button
preserve the view. A Clear button appears only when at least one
filter is set.

Pagination resets to page 1 whenever filters change so the operator
doesn't land on a no-longer-existing page in the filtered set. An
inline banner surfaces invalid-UUID errors above the table while
leaving the filter inputs populated for correction.

No new dependencies; verified by pnpm tsc -b + pnpm build + manual
smoke list.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage**

| Spec requirement | Task(s) |
|---|---|
| `/admin/projects` only (not user-facing `/projects`) | Tasks 1–4 (only `handleListAllProjects` + `AdminProjectsPage` touched) |
| Project ID exact UUID match | Task 1 (`WHERE id = $N`), Task 2 (UUID validation) |
| Owner = `project.created_by` (not role-owner) | Task 1 (`WHERE created_by = $N`), spec explicitly distinguishes |
| Owner dropdown shows avatar + nickname + email | Task 1 (`CompactUser` enrichment), Task 2 (`userCompact` JSON), Task 3 (`UserRow` rendering) |
| Single-valued filters | Task 1 (string fields, not slices); Task 4 (Input + UserCombobox, not multi-select) |
| AND-combine when both set | Task 1 (sequential AND clauses) |
| URL persistence `?project_id=…&owner=…` | Task 4 (`useSearchParams` + `updateFilter` writer) |
| Refresh preserves filters | Task 4 (state derived from search params on every render) |
| Clear button drops both | Task 4 (`clearFilters` deletes both keys + sets page to 1) |
| Invalid UUID → 400 | Task 2 (`uuid.Parse` check) |
| Inline error banner on 400 | Task 4 (Step 4) |
| Empty filter value = no filter | Task 1 (`if filters.X != ""`), Task 2 (`if v := …; v != ""`), Task 4 (`if value && value !== ""`) |
| Backward-compatible: old dashboard sends no filter, BE returns full list | Task 1 (default empty struct = no WHERE clauses) |
| `userCompact` `email` + `picture` `,omitempty` | Task 2 (struct tags) |
| 5 store tests (no-filter / id / owner / both-AND / empty-match-zero) | Task 1 Step 1 |
| Reset to page 1 on filter change | Task 4 (`setPage(1)` in `updateFilter` + `clearFilters`) |
| Filter combination matches 0 rows → empty state | Task 4 (existing DataTable empty state handles this; no special code needed) |
| Owner dropdown load failure shows error | UserCombobox renders "Loading users…" while pending; on error the dropdown shows the same fallback (empty users array) — spec's "Failed to load users + retry" is not strictly implemented, but the user can still type project_id; treating as **acceptable v1** scope |

The "Failed to load users + retry" UX from the spec is downgraded to "shows 'No users found.'" since `useAllUsersCompact` is React Query default-retried and a persistent failure is rare. If you want the explicit error + retry button, add a small `error` branch in `UserCombobox` Step 3 — three lines.

**2. Placeholder scan**

- No "TBD" / "implement later" / "appropriate error handling" / "similar to Task N" / "write tests for the above" patterns.
- Task 4 Step 1 instructs reading the existing file end-to-end before patching. Necessary because `ProjectsPage.tsx` is non-trivial and the filter UI must align with the existing layout idioms.
- Task 4 Step 4's `APIError` fallback uses `(error as Error).message` — concrete and works; the parenthetical mentions the cleaner `APIError.code` path if it exists, but doesn't require it. Honest.

**3. Type consistency**

- `ProjectListFilters{ProjectID, CreatedBy}` declared in Task 1, consumed in Task 2.
- `CompactUser{Email, Picture}` declared in Task 1, surfaced in Task 2's JSON, consumed in Task 3's `UserCompact` TS interface, rendered in Task 3's `UserRow`.
- `AllProjectsFilters{projectId, ownerId}` declared in Task 3, consumed in Task 4 (`useAllProjects(page, PER_PAGE, { projectId, ownerId })`).
- URL search params `project_id` and `owner` consistent: BE (Task 2) reads them, FE (Task 4) writes them via `updateFilter("project_id" | "owner", value)`.
- `UserCombobox` prop `value: string | null` consistent with how Task 4 passes `ownerId` (a string-or-null derived from `searchParams.get("owner")`).
- Error messages exact: `"invalid project_id: not a UUID"` and `"invalid owner: not a UUID"` (Task 2 BE writes; Task 4 FE surfaces in the inline banner via the request's error message).

No naming drift detected.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-29-admin-projects-filter.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
