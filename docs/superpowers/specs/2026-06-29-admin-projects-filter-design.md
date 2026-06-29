# Admin Projects List — Project-ID + Owner Filter — Design

## Problem

The `/admin/projects` page lists every project in the deployment, paginated
20 per page, with no filtering. Operators investigating a specific
customer or auditing one user's project portfolio have to scroll / page
through the entire list looking for the row. As the deployment grows
this becomes impractical.

## Goal

Add two filter controls at the top of the admin Projects List page:

1. **Project ID** — a text input accepting a project UUID. Exact match.
2. **Owner** — a `Combobox`-style dropdown showing all users with avatar
   + nickname + email; selecting one filters the list to projects whose
   `created_by` equals that user.

Both filters are optional, single-valued, and AND-combined when both are
set. Filter state is reflected in the URL search params
(`?project_id=…&owner=…`) so refresh, back-button, and shared links
preserve the view. The Clear button drops both filters.

`/projects` (the user-facing list) is **not** changed — it already
shows only the caller's own projects.

## Non-goals

- **Project-ID prefix matching.** Exact UUID match only. SQL hits the
  primary key index. If operators routinely have only a prefix, a
  follow-up spec can add an `ILIKE` mode behind a separate query
  param.
- **Multi-select Owner.** v1 single-select. Multi-owner queries
  (`WHERE created_by IN (…)`) are a future need; the JSON
  `?owner=` shape leaves room to evolve.
- **Server-side user search.** Reuse the existing
  `useAllUsersCompact()` hook that fetches every user in one shot.
  Adequate for small/mid deployments. If user counts cross
  ~thousands, swap in a `/users/search?q=` endpoint behind the same
  Combobox.
- **Filter user-facing `/projects`.** The user-facing list already
  shows only the caller's projects; an Owner filter is meaningless
  there.
- **Change the meaning of "owner".** This spec defines owner as
  `project.created_by` (immutable historic creator), not the current
  `role=owner` member from `project_members`. The two notions diverge
  after an ownership transfer; the existing admin overview surfaces
  the role-owner; this filter targets the creator. The mismatch is
  intentional — operators investigating provenance almost always want
  the creator. If a "current owner" filter is later needed, it's a
  separate filter (different JOIN) and a separate spec.

## Scope decisions (from brainstorming)

| Decision | Choice |
|---|---|
| Which list page | `/admin/projects` only |
| Owner semantics | `project.created_by` (creator) |
| Project-ID input | Text field, exact UUID match |
| Owner input | `Combobox` dropdown with avatar + nickname + email |
| Multi-select | Both filters single-valued |
| URL persistence | `?project_id=…&owner=…` survives refresh and back-button |
| Filter clear | One Clear button drops both filters and the URL params |
| User-facing list | Untouched |
| User data fetch | Reuse `useAllUsersCompact()` (single fetch, cached) |
| Extra user fields | `userCompact` JSON gains `email` + `picture` (today: `id` + `nickname` only) |

## Backend

### Store

`internal/store/projects.go` — extend `ListAllProjects`:

```go
// ProjectListFilters narrows ListAllProjects. Empty fields are ignored
// (= no filter on that dimension); both empty = no filter at all.
type ProjectListFilters struct {
    ProjectID string // exact match against projects.id (UUID)
    CreatedBy string // exact match against projects.created_by (UUID)
}

func (s *Store) ListAllProjects(p types.PaginationParams, filters ProjectListFilters) ([]types.Project, int, error)
```

Implementation: dynamically construct the `WHERE` clause:

- `WHERE 1=1` baseline.
- `AND id = $N` when `filters.ProjectID != ""`.
- `AND created_by = $N` when `filters.CreatedBy != ""`.

The `COUNT(*)` and the data SELECT share the same WHERE so the
paginator's total reflects the filtered set, not the global total.

`pgx` parses UUID-typed columns from string args natively. Pass the
values as strings; pgx handles the cast.

### Handler

`internal/admin/handle_projects.go` — extend `handleListAllProjects`:

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

Empty / absent query params skip the validator and the filter (= no
restriction). Malformed UUIDs return 400 + a descriptive error message
so the dashboard can surface it inline.

### Users-compact enrichment

`internal/admin/handle_auth.go` — extend `userCompact`:

```go
type userCompact struct {
    ID       string `json:"id"`
    Nickname string `json:"nickname,omitempty"`
    Email    string `json:"email,omitempty"`     // NEW
    Picture  string `json:"picture,omitempty"`   // NEW
}
```

And copy the new fields in `handleListUsersCompact`:

```go
out = append(out, userCompact{
    ID:       u.ID,
    Nickname: u.Nickname,
    Email:    u.Email,
    Picture:  u.Picture,
})
```

`internal/store/users.go` — `ListAllUsersCompact` SELECT must already
read `email` and `picture` columns (the User type has them). If the
SELECT lists columns explicitly, add the two. If it uses `SELECT *`,
no change needed.

The existing `userCompact` doc comment that says "Email is intentionally
excluded" is stale — update it to reflect the new use case (the project
filter needs email for human-readable identification).

## Frontend

### API hooks + types

`dashboard/src/api/users.ts` — extend `UserCompact`:

```ts
export interface UserCompact {
  id: string;
  nickname?: string;
  email?: string;      // NEW
  picture?: string;    // NEW
}
```

`dashboard/src/api/projects.ts` — extend `useAllProjects`:

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
    queryFn: () => api.get<ListResponse<Project>>(`/api/v1/admin/projects?${qs.toString()}`),
  });
}
```

Cache key includes both filters so React Query keeps separate slices
per filter combination. No new mutations need invalidation key updates
(admin project list isn't currently mutated from the UI).

### Components

`dashboard/src/components/shared/UserCombobox.tsx` (new) — mirrors the
existing `ModelCombobox` (`dashboard/src/components/shared/ModelCombobox.tsx`)
but for users:

```tsx
interface UserComboboxProps {
  value: string | null;             // selected user.id, or null
  onChange: (userId: string | null) => void;
  placeholder?: string;             // default: "Filter by owner"
}

export function UserCombobox({ value, onChange, placeholder }: UserComboboxProps) {
  const { data } = useAllUsersCompact();
  const users = data?.data ?? [];
  const selectedUser = value ? users.find((u) => u.id === value) : null;

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="outline" className="justify-between">
          {selectedUser ? <UserRow user={selectedUser} /> : <span className="text-muted-foreground">{placeholder ?? "Filter by owner"}</span>}
          <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent>
        <Command>
          <CommandInput placeholder="Search by name or email…" />
          <CommandList>
            <CommandEmpty>No users found.</CommandEmpty>
            <CommandGroup>
              {users.map((u) => (
                <CommandItem
                  key={u.id}
                  value={`${u.nickname ?? ""} ${u.email ?? ""} ${u.id}`}
                  onSelect={() => { onChange(u.id); }}
                >
                  <UserRow user={u} />
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}

function UserRow({ user }: { user: UserCompact }) {
  return (
    <div className="flex items-center gap-2">
      <Avatar className="h-5 w-5">
        {user.picture ? <AvatarImage src={user.picture} /> : null}
        <AvatarFallback>{(user.nickname ?? user.id).slice(0, 2).toUpperCase()}</AvatarFallback>
      </Avatar>
      <span className="text-sm">{user.nickname ?? user.id.slice(0, 8)}</span>
      {user.email ? <span className="text-xs text-muted-foreground">{user.email}</span> : null}
    </div>
  );
}
```

The `Command` primitives' `value` prop accepts a space-separated string;
the built-in fuzzy filter matches against it, so typing either nickname
or email substring narrows the list.

### Page

`dashboard/src/pages/admin/ProjectsPage.tsx` — add filter UI between
`PageHeader` and the table:

- `useSearchParams` hook (`react-router`) to read/write `project_id` and
  `owner`.
- An `Input` for project_id.
- A `UserCombobox` for owner.
- A `Button variant="ghost" size="sm">Clear</Button>` shown only when at
  least one filter is set.
- `useAllProjects(page, perPage, { projectId, ownerId })` consumes the
  filters.
- Pagination reset to page 1 whenever filters change (so the operator
  doesn't get stuck on a page that no longer exists in the filtered
  set).
- Error rendering: if `useAllProjects` throws (e.g. 400 from invalid
  UUID), display a small inline banner with the error message above the
  table, but keep the filter inputs populated so the operator can
  correct.

## Data flow

```
Operator types or pastes project_id, picks owner from dropdown
  └─► setSearchParams({ project_id, owner }, { replace: true })
  └─► useAllProjects(page, perPage, {projectId, ownerId})
       └─► GET /api/v1/admin/projects?page=…&per_page=…&project_id=…&owner=…
            └─► handleListAllProjects
                 ├─► validate UUIDs (400 if malformed)
                 └─► ListAllProjects(p, ProjectListFilters{ProjectID, CreatedBy})
                      └─► SQL: SELECT … WHERE 1=1 [AND id=$N] [AND created_by=$N]
                                 ORDER BY created_at DESC LIMIT … OFFSET …
                      └─► COUNT(*) with same WHERE
       ◄── { data: [projects…], meta: { total, page, per_page } }
  └─► Table renders filtered rows; pagination uses filtered total
  Operator clicks "Clear"
  └─► setSearchParams({}) → drops both filters
  └─► useAllProjects(page, perPage, {}) → unfiltered list
```

## Error handling

| Failure | Behavior |
|---|---|
| `?project_id=not-a-uuid` | BE 400 `"invalid project_id: not a UUID"`. FE inline banner above table; filter inputs stay populated. |
| `?owner=not-a-uuid` | BE 400 `"invalid owner: not a UUID"`. Same FE behavior. |
| `useAllUsersCompact` fetch fails | Owner Combobox shows "Failed to load users" + retry button. Project_id field still works. |
| Filter combination matches 0 rows | Standard empty state ("No projects match the current filter"). |
| Operator navigates to a page that doesn't exist in the filtered set (e.g. page=3 after a filter narrows results to 5 rows total) | Pagination component already clamps to last valid page. No special handling needed. |
| Empty filter (e.g. `?project_id=&owner=`) | BE treats empty strings as "no filter"; FE normalizes empty values out of the URL during the `setSearchParams` call. |

## Testing

### Backend

`internal/store/projects_test.go` (extend):

- `TestListAllProjects_NoFilters` — sanity, returns all projects ordered.
- `TestListAllProjects_FilterByProjectID` — exact UUID match returns 1 row; non-matching UUID returns 0.
- `TestListAllProjects_FilterByCreatedBy` — multi-project owner returns N rows; non-owner user returns 0.
- `TestListAllProjects_FilterByBoth_IntersectsAND` — both filters set; returns only rows matching BOTH.
- `TestListAllProjects_PaginationCountReflectsFilter` — filtered total ≠ global total when filter applied.

`internal/admin/handle_projects_test.go` (extend if scaffolding exists,
otherwise piggyback on the integration tests added in PR #67):

- `TestHandleListAllProjects_InvalidProjectIDReturns400`.
- `TestHandleListAllProjects_InvalidOwnerReturns400`.
- Happy-path filter passes through (covered by the store-level tests).

### Frontend

No frontend test framework. Verification = `pnpm exec tsc -b && pnpm
build` + manual smoke checklist:

1. Open `/admin/projects` — full list, no filter UI active.
2. Paste a project UUID into the project_id field — table narrows to 1
   row; URL shows `?project_id=…`.
3. Open Owner dropdown — Combobox shows avatar + nickname + email per
   user; search box filters by typing.
4. Pick an owner — table narrows; URL shows `?owner=…`.
5. Set both filters — table shows the intersection.
6. Click Clear — both filters reset; table returns to full list; URL
   params dropped.
7. Refresh page mid-filter — filter state restored from URL.
8. Bad input: paste a non-UUID string into project_id, submit. Inline
   banner shows error; input remains so operator can fix.

## Migration / deploy order

No schema changes. No migrations.

1. Backend deploy: extended `ListAllProjects` + handler validation +
   enriched `userCompact`. Old dashboards continue to work — they send
   no filter params, BE returns full list as today; the extra `email`
   and `picture` fields on `userCompact` are ignored by older
   consumers.
2. Dashboard deploy: filter UI exposed.

No rollback hazard: the change is purely additive on both sides.
