# Huma Admin API — Batch 1: Contract Infrastructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the shared authz + contract primitives, resolver plumbing, per-subsystem store interface scaffolds, and CI invariants that Batches 2–14 all depend on. **No user-visible routes change in this batch.**

**Architecture:** Extend the existing `internal/authz` catalog with the permissions, policies, and DSL helper the design calls out; promote a couple of adapters into `internal/api/contract`; add an empty resolver registry hook and an empty per-subsystem-store-interfaces file inside `internal/api/admin/v1`; commit `.github/workflows/management-contract.yml` and four new spec-invariant Go tests. Every route continues to live where it currently lives after this batch.

**Tech Stack:** Go 1.24 (see `go.mod`), `github.com/danielgtaylor/huma/v2`, `github.com/go-chi/chi/v5`, `pnpm@10.32.0`, `openapi-typescript@7.13.0`, `openapi-fetch@0.17.0`.

## Global Constraints

- **Spec source of truth:** `docs/superpowers/specs/2026-07-11-huma-admin-api-and-rbac-design.md`. Every deviation must call itself out.
- **Wire compatibility:** existing `data` / `meta` / `error` envelopes, HTTP status codes, field names, and trailing-slash aliases stay unchanged. No route added or removed in this batch.
- **Single-router rule:** every `(method, path)` is owned by exactly one router. Because this batch does not migrate routes, the invariant test added in Task 12 must run **green against the current tree** (pre-migration). See Task 12 for how the test is scoped so it will not false-positive on the already-committed operations.
- **Default deny:** unknown permissions, unknown roles, missing resource resolvers, missing policies, and resolver errors all deny the request. Never treat missing membership as superadmin.
- **No behavior change:** this batch registers no new operations, no new routes, no new middleware. The `authorizationMiddleware` in `internal/api/admin/v1/server.go` reads the new registries lazily.
- **Frequent commits:** every task ends with `git add ... && git commit`. Commit messages match `feat(...):`, `test(...):`, `chore(...):`, `docs(...):`, `ci:` conventions used elsewhere in the repo (see `git log --oneline -20`). Every commit ends with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- **Test discipline:** each task adds the failing test **before** the implementation. Never edit implementation code without a test guarding it.

---

## File Structure

**Files added:**

- `internal/authz/system_on_project_path.go` — the `SystemOnProjectPath` DSL helper and the private flag it stamps on `AccessPolicy`.
- `internal/authz/system_on_project_path_test.go`
- `internal/authz/policy_key_owned.go` — `PolicyKeyOwnedByCallerForDeveloper` implementation.
- `internal/authz/policy_key_owned_test.go`
- `internal/authz/policy_member_self.go` — `PolicyMemberSelfOrElevated` implementation.
- `internal/authz/policy_member_self_test.go`
- `internal/api/contract/bytes_response.go` — `BytesResponse` output type + helper.
- `internal/api/contract/bytes_response_test.go`
- `internal/api/contract/legacy_alias.go` — promoted `RegisterWithLegacyTrailingSlash`.
- `internal/api/contract/legacy_alias_test.go`
- `internal/api/contract/invariants_test.go` — the four spec-invariant tests (dual registration, catalog permission, resource resolver, envelope stability). See Task 12.
- `internal/api/admin/v1/subsystem_stores.go` — empty per-subsystem store interface stubs (declared but not consumed).
- `internal/api/admin/v1/resolvers/registry.go` — a resolver registry map with a `Register` and `Default()` helper; empty defaults.
- `internal/api/admin/v1/resolvers/registry_test.go`
- `internal/api/admin/v1/policies_registry.go` — a policy registry map for admin runtime wiring; empty defaults; installs the three built-in policies + the two new ones from this batch (`PolicyKeyOwnedByCallerForDeveloper`, `PolicyMemberSelfOrElevated`) into the runtime `Server.Policies` map.

**Files modified:**

- `internal/authz/permission.go` — add 6 new permissions (2 system + 4 project) and register them in `permissionScopes`.
- `internal/authz/role.go` — grant the 4 new project permissions to the appropriate roles.
- `internal/authz/policy.go` — declare the 2 new `PolicyID` constants.
- `internal/authz/access.go` — add the private `systemOnProjectPath bool` field on `AccessPolicy`, and adjust `validateRBAC` to allow `ProjectIDPathParam` under system scope **only when** that flag is set. Add `MarshalJSON` guard for the flag so it never appears in JSON.
- `internal/api/admin/v1/operations.go` — reuse `contract.RegisterWithLegacyTrailingSlash` (delete the local copy) so the promotion is used from day one.
- `internal/api/admin/v1/server.go` — `Server` gains a `Resolvers map[string]authz.ResourceResolver` (already present) *and* now uses `resolvers.Default()` as its default when `nil`. Same for `Policies`. No behavior change: current admin `Register(api, nil)` still passes nil.
- `.github/workflows/management-contract.yml` — commit the file (currently untracked) as-is; no content change.

**Files unchanged in this batch:** `internal/admin/*`, `cmd/openapi/main.go`, `dashboard/**`, `api/openapi/admin.openapi.json`.

---

## Task 1: Add the 6 new permissions and their role grants

**Files:**
- Modify: `internal/authz/permission.go:9-103`
- Modify: `internal/authz/role.go:28-65`
- Test: `internal/authz/permission_test.go` (add new sub-test)
- Test: `internal/authz/role_test.go` (add new sub-test)

**Interfaces:**
- Consumes: nothing new.
- Produces: `authz.PermissionSystemNotificationsRead`, `authz.PermissionSystemNotificationsManage`, `authz.PermissionProjectMembersUsageRead`, `authz.PermissionProjectExtraUsageRead`, `authz.PermissionProjectExtraUsageWrite`, `authz.PermissionProjectExtraUsageTopup`. Grants: developer / maintainer / owner include the new project permissions per spec §3.1.

- [ ] **Step 1: Write failing permission-catalog test additions**

Append to `internal/authz/permission_test.go`:

```go
func TestNewPermissionsAppearInCatalog(t *testing.T) {
	t.Parallel()

	want := []Permission{
		PermissionSystemNotificationsRead,
		PermissionSystemNotificationsManage,
		PermissionProjectMembersUsageRead,
		PermissionProjectExtraUsageRead,
		PermissionProjectExtraUsageWrite,
		PermissionProjectExtraUsageTopup,
	}
	catalog := AllPermissions()
	for _, permission := range want {
		if !permission.Valid() {
			t.Errorf("new permission %q is not Valid()", permission)
		}
		found := false
		for _, catalogued := range catalog {
			if catalogued == permission {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("permission %q missing from AllPermissions()", permission)
		}
	}

	systemWant := []Permission{
		PermissionSystemNotificationsRead,
		PermissionSystemNotificationsManage,
	}
	for _, permission := range systemWant {
		scope, _ := permission.Scope()
		if scope != ScopeSystem {
			t.Errorf("permission %q scope = %q, want %q", permission, scope, ScopeSystem)
		}
	}
	projectWant := []Permission{
		PermissionProjectMembersUsageRead,
		PermissionProjectExtraUsageRead,
		PermissionProjectExtraUsageWrite,
		PermissionProjectExtraUsageTopup,
	}
	for _, permission := range projectWant {
		scope, _ := permission.Scope()
		if scope != ScopeProject {
			t.Errorf("permission %q scope = %q, want %q", permission, scope, ScopeProject)
		}
	}
}
```

- [ ] **Step 2: Write failing role-grant test additions**

Append to `internal/authz/role_test.go`:

```go
func TestNewProjectPermissionsRoleGrants(t *testing.T) {
	t.Parallel()

	developer, _ := PermissionsForRole(RoleDeveloper)
	maintainer, _ := PermissionsForRole(RoleMaintainer)
	owner, _ := PermissionsForRole(RoleOwner)

	allRoles := []Permission{
		PermissionProjectMembersUsageRead,
		PermissionProjectExtraUsageRead,
	}
	for _, permission := range allRoles {
		if !developer.Has(permission) {
			t.Errorf("developer missing %q", permission)
		}
		if !maintainer.Has(permission) {
			t.Errorf("maintainer missing %q", permission)
		}
		if !owner.Has(permission) {
			t.Errorf("owner missing %q", permission)
		}
	}

	maintainerAndUp := []Permission{
		PermissionProjectExtraUsageWrite,
		PermissionProjectExtraUsageTopup,
	}
	for _, permission := range maintainerAndUp {
		if developer.Has(permission) {
			t.Errorf("developer unexpectedly has %q", permission)
		}
		if !maintainer.Has(permission) {
			t.Errorf("maintainer missing %q", permission)
		}
		if !owner.Has(permission) {
			t.Errorf("owner missing %q", permission)
		}
	}
}
```

- [ ] **Step 3: Run tests and verify they fail**

```
cd /root/coding/modelserver
go test ./internal/authz/ -run 'TestNewPermissionsAppearInCatalog|TestNewProjectPermissionsRoleGrants' -v
```

Expected: both tests FAIL with undefined identifier errors on the new `Permission*` names.

- [ ] **Step 4: Add the 6 permissions in `internal/authz/permission.go`**

In the `const (...)` block after the existing system permissions (currently ending at `PermissionSystemSubscriptionOverride`), insert:

```go
	PermissionSystemNotificationsRead   Permission = "system.notifications.read"
	PermissionSystemNotificationsManage Permission = "system.notifications.manage"
```

In the same `const (...)` block after the last project permission (`PermissionProjectTracesRead`), insert:

```go
	PermissionProjectMembersUsageRead Permission = "project.members.usage.read"
	PermissionProjectExtraUsageRead   Permission = "project.extra_usage.read"
	PermissionProjectExtraUsageWrite  Permission = "project.extra_usage.write"
	PermissionProjectExtraUsageTopup  Permission = "project.extra_usage.topup"
```

Then extend `permissionScopes` (the `var permissionScopes = map[Permission]Scope{...}`) with the same six entries pointing to `ScopeSystem` or `ScopeProject` respectively.

- [ ] **Step 5: Grant the new project permissions to roles in `internal/authz/role.go`**

Modify `developerPermissions` to append `PermissionProjectMembersUsageRead` and `PermissionProjectExtraUsageRead` after `PermissionProjectTracesRead`:

```go
var developerPermissions = []Permission{
	PermissionProjectRead,
	PermissionProjectModelsRead,
	PermissionProjectMembersRead,
	PermissionProjectMembersUsageRead,
	PermissionProjectKeysCreate,
	PermissionProjectKeysRead,
	PermissionProjectKeysManage,
	PermissionProjectOAuthGrantsRead,
	PermissionProjectPoliciesRead,
	PermissionProjectSubscriptionsRead,
	PermissionProjectPlansRead,
	PermissionProjectOrdersRead,
	PermissionProjectBillingRead,
	PermissionProjectRequestsRead,
	PermissionProjectUsageRead,
	PermissionProjectTracesRead,
	PermissionProjectExtraUsageRead,
}
```

Modify `maintainerPermissions` to also grant the two write permissions:

```go
var maintainerPermissions = appendPermissions(developerPermissions,
	PermissionProjectSettingsWrite,
	PermissionProjectMembersManage,
	PermissionProjectOAuthGrantsManage,
	PermissionProjectPoliciesManage,
	PermissionProjectOrdersCreate,
	PermissionProjectOrdersManage,
	PermissionProjectBillingManage,
	PermissionProjectExtraUsageWrite,
	PermissionProjectExtraUsageTopup,
)
```

Do **not** touch `ownerPermissions`; it inherits from `maintainerPermissions` and stays unchanged.

- [ ] **Step 6: Run all authz tests and verify they pass**

```
go test ./internal/authz/ -v
```

Expected: PASS. In particular:
- `TestPermissionCatalog` still passes (new entries are valid + scoped + sorted).
- `TestRolePermissionHierarchy` still passes — the two owner-only project-level counters (`owner.Len() == len(permissionsInScope(ScopeProject))`) will remain accurate because both new permissions are granted to owner via the inheritance chain.
- `TestNewPermissionsAppearInCatalog` and `TestNewProjectPermissionsRoleGrants` pass.

- [ ] **Step 7: Commit**

```bash
git add internal/authz/permission.go internal/authz/permission_test.go internal/authz/role.go internal/authz/role_test.go
git commit -m "feat(authz): add notifications + extra-usage + members-usage permissions

Adds 2 system + 4 project permissions per Batch 1 of the huma-admin
migration and grants the new project permissions to the appropriate
built-in roles. No runtime routes consume these yet.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Declare the two new PolicyID constants

**Files:**
- Modify: `internal/authz/policy.go:9-13`
- Test: `internal/authz/policy_test.go` (append)

**Interfaces:**
- Produces: `authz.PolicyKeyOwnedByCallerForDeveloper`, `authz.PolicyMemberSelfOrElevated` — `PolicyID` constants only. Implementations arrive in Tasks 3 and 4.

- [ ] **Step 1: Write failing test**

Append to `internal/authz/policy_test.go`:

```go
func TestNewPolicyIDConstantsAreStable(t *testing.T) {
	t.Parallel()

	if PolicyKeyOwnedByCallerForDeveloper != PolicyID("key-owned-by-caller-for-developer") {
		t.Errorf("PolicyKeyOwnedByCallerForDeveloper = %q, want %q",
			PolicyKeyOwnedByCallerForDeveloper, "key-owned-by-caller-for-developer")
	}
	if PolicyMemberSelfOrElevated != PolicyID("member-self-or-elevated") {
		t.Errorf("PolicyMemberSelfOrElevated = %q, want %q",
			PolicyMemberSelfOrElevated, "member-self-or-elevated")
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

```
go test ./internal/authz/ -run TestNewPolicyIDConstantsAreStable -v
```

Expected: FAIL — undefined identifiers.

- [ ] **Step 3: Add the constants in `internal/authz/policy.go`**

Extend the existing `const (...)` block:

```go
const (
	PolicyOwnResource                PolicyID = "own-resource"
	PolicyResourceProjectContainment PolicyID = "resource-project-containment"
	PolicyMemberRoleHierarchy        PolicyID = "member-role-hierarchy"

	PolicyKeyOwnedByCallerForDeveloper PolicyID = "key-owned-by-caller-for-developer"
	PolicyMemberSelfOrElevated         PolicyID = "member-self-or-elevated"
)
```

- [ ] **Step 4: Run and verify test passes**

```
go test ./internal/authz/ -run TestNewPolicyIDConstantsAreStable -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/authz/policy.go internal/authz/policy_test.go
git commit -m "feat(authz): declare PolicyKeyOwnedByCallerForDeveloper + PolicyMemberSelfOrElevated

Two new PolicyID constants used by Keys and Members migrations. The
concrete Policy implementations follow in subsequent commits.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Implement PolicyKeyOwnedByCallerForDeveloper

**Files:**
- Create: `internal/authz/policy_key_owned.go`
- Create: `internal/authz/policy_key_owned_test.go`

**Interfaces:**
- Consumes: `authz.Policy`, `authz.PolicyInput`, `authz.PolicyID`, `authz.PolicyKeyOwnedByCallerForDeveloper`, `authz.ProjectRole`, `authz.RoleOwner`, `authz.RoleMaintainer`.
- Produces:
  - `authz.KeyOwnedByCallerForDeveloperPolicy` — zero-value struct implementing `authz.Policy`.
  - Semantics: `Evaluate` returns `true` when `input.Principal.Superadmin`, or when `input.Role == RoleOwner || input.Role == RoleMaintainer`. Otherwise requires `input.Resource != nil && input.Principal.UserID != "" && input.Principal.UserID == input.Resource.OwnerID`. Returns `(false, nil)` on mismatch — no error.

- [ ] **Step 1: Write failing tests**

Create `internal/authz/policy_key_owned_test.go`:

```go
package authz

import (
	"context"
	"testing"
)

func TestKeyOwnedByCallerForDeveloperPolicy(t *testing.T) {
	t.Parallel()

	policy := KeyOwnedByCallerForDeveloperPolicy{}
	if policy.ID() != PolicyKeyOwnedByCallerForDeveloper {
		t.Fatalf("ID() = %q", policy.ID())
	}

	callerA := Principal{UserID: "user-a"}
	callerB := Principal{UserID: "user-b"}
	superadmin := Principal{UserID: "admin-1", Superadmin: true}
	own := Resource{Type: "api-key", ID: "k1", ProjectID: "p1", OwnerID: "user-a"}

	tests := []struct {
		name    string
		input   PolicyInput
		want    bool
	}{
		{name: "superadmin without resource", input: PolicyInput{Principal: superadmin}, want: true},
		{name: "owner role", input: PolicyInput{Principal: callerB, Role: RoleOwner, Resource: &own}, want: true},
		{name: "maintainer role", input: PolicyInput{Principal: callerB, Role: RoleMaintainer, Resource: &own}, want: true},
		{name: "developer owning resource", input: PolicyInput{Principal: callerA, Role: RoleDeveloper, Resource: &own}, want: true},
		{name: "developer other's resource", input: PolicyInput{Principal: callerB, Role: RoleDeveloper, Resource: &own}, want: false},
		{name: "developer nil resource", input: PolicyInput{Principal: callerA, Role: RoleDeveloper}, want: false},
		{name: "anonymous with owner role", input: PolicyInput{Principal: Principal{}, Role: RoleOwner}, want: true},
		{name: "developer unknown owner", input: PolicyInput{Principal: callerA, Role: RoleDeveloper, Resource: &Resource{OwnerID: ""}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := policy.Evaluate(context.Background(), test.input)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("Evaluate() = %v, want %v", got, test.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

```
go test ./internal/authz/ -run TestKeyOwnedByCallerForDeveloperPolicy -v
```

Expected: FAIL — `KeyOwnedByCallerForDeveloperPolicy` undefined.

- [ ] **Step 3: Implement the policy**

Create `internal/authz/policy_key_owned.go`:

```go
package authz

import "context"

// KeyOwnedByCallerForDeveloperPolicy authorizes an API-key operation for
// callers who are project owners, maintainers, or superadmins; otherwise
// it requires the caller to own the resource. The policy encodes the
// legacy "developers can only touch keys they created" rule as one
// AccessPolicy condition so it appears in x-modelserver-authz metadata.
type KeyOwnedByCallerForDeveloperPolicy struct{}

// ID returns PolicyKeyOwnedByCallerForDeveloper.
func (KeyOwnedByCallerForDeveloperPolicy) ID() PolicyID { return PolicyKeyOwnedByCallerForDeveloper }

// Evaluate implements Policy. Superadmins and owner/maintainer roles
// short-circuit to allow; other callers must match the resource owner.
func (KeyOwnedByCallerForDeveloperPolicy) Evaluate(_ context.Context, input PolicyInput) (bool, error) {
	if input.Principal.Superadmin {
		return true, nil
	}
	if input.Role == RoleOwner || input.Role == RoleMaintainer {
		return true, nil
	}
	if input.Resource == nil {
		return false, nil
	}
	if input.Principal.UserID == "" || input.Resource.OwnerID == "" {
		return false, nil
	}
	return input.Principal.UserID == input.Resource.OwnerID, nil
}

var _ Policy = KeyOwnedByCallerForDeveloperPolicy{}
```

- [ ] **Step 4: Run test and verify it passes**

```
go test ./internal/authz/ -run TestKeyOwnedByCallerForDeveloperPolicy -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/authz/policy_key_owned.go internal/authz/policy_key_owned_test.go
git commit -m "feat(authz): add KeyOwnedByCallerForDeveloperPolicy

Superadmins and owner/maintainer roles bypass the ownership check;
developers must own the resource. Not yet wired to any operation.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Implement PolicyMemberSelfOrElevated

**Files:**
- Create: `internal/authz/policy_member_self.go`
- Create: `internal/authz/policy_member_self_test.go`

**Interfaces:**
- Consumes: same as Task 3.
- Produces:
  - `authz.MemberSelfOrElevatedPolicy` — zero-value struct implementing `authz.Policy`.
  - Semantics: allow when caller is superadmin, or holds `RoleOwner`/`RoleMaintainer`, or when `input.Resource != nil && input.Resource.ID == input.Principal.UserID` (developer reading their own member row).

- [ ] **Step 1: Write failing tests**

Create `internal/authz/policy_member_self_test.go`:

```go
package authz

import (
	"context"
	"testing"
)

func TestMemberSelfOrElevatedPolicy(t *testing.T) {
	t.Parallel()

	policy := MemberSelfOrElevatedPolicy{}
	if policy.ID() != PolicyMemberSelfOrElevated {
		t.Fatalf("ID() = %q", policy.ID())
	}

	developer := Principal{UserID: "user-a"}
	other := Principal{UserID: "user-b"}
	superadmin := Principal{UserID: "admin-1", Superadmin: true}
	selfRow := Resource{Type: "member", ID: "user-a", ProjectID: "p1"}
	otherRow := Resource{Type: "member", ID: "user-b", ProjectID: "p1"}

	tests := []struct {
		name  string
		input PolicyInput
		want  bool
	}{
		{name: "owner", input: PolicyInput{Principal: other, Role: RoleOwner, Resource: &otherRow}, want: true},
		{name: "maintainer", input: PolicyInput{Principal: other, Role: RoleMaintainer, Resource: &otherRow}, want: true},
		{name: "superadmin", input: PolicyInput{Principal: superadmin, Resource: &otherRow}, want: true},
		{name: "developer self", input: PolicyInput{Principal: developer, Role: RoleDeveloper, Resource: &selfRow}, want: true},
		{name: "developer other", input: PolicyInput{Principal: developer, Role: RoleDeveloper, Resource: &otherRow}, want: false},
		{name: "developer nil resource", input: PolicyInput{Principal: developer, Role: RoleDeveloper}, want: false},
		{name: "anonymous", input: PolicyInput{Principal: Principal{}, Resource: &selfRow}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := policy.Evaluate(context.Background(), test.input)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("Evaluate() = %v, want %v", got, test.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run and verify it fails**

```
go test ./internal/authz/ -run TestMemberSelfOrElevatedPolicy -v
```

Expected: FAIL — `MemberSelfOrElevatedPolicy` undefined.

- [ ] **Step 3: Implement the policy**

Create `internal/authz/policy_member_self.go`:

```go
package authz

import "context"

// MemberSelfOrElevatedPolicy authorizes read access to a per-member
// endpoint when the caller is either an elevated principal (superadmin,
// owner, maintainer) or the resource IS the caller's own member row.
type MemberSelfOrElevatedPolicy struct{}

// ID returns PolicyMemberSelfOrElevated.
func (MemberSelfOrElevatedPolicy) ID() PolicyID { return PolicyMemberSelfOrElevated }

// Evaluate implements Policy.
func (MemberSelfOrElevatedPolicy) Evaluate(_ context.Context, input PolicyInput) (bool, error) {
	if input.Principal.Superadmin {
		return true, nil
	}
	if input.Role == RoleOwner || input.Role == RoleMaintainer {
		return true, nil
	}
	if input.Resource == nil || input.Principal.UserID == "" {
		return false, nil
	}
	return input.Resource.ID == input.Principal.UserID, nil
}

var _ Policy = MemberSelfOrElevatedPolicy{}
```

- [ ] **Step 4: Run test and verify it passes**

```
go test ./internal/authz/ -run TestMemberSelfOrElevatedPolicy -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/authz/policy_member_self.go internal/authz/policy_member_self_test.go
git commit -m "feat(authz): add MemberSelfOrElevatedPolicy

Owners, maintainers, and superadmins bypass the self-check;
developers may only reach their own member row.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Add SystemOnProjectPath DSL helper

**Files:**
- Create: `internal/authz/system_on_project_path.go`
- Create: `internal/authz/system_on_project_path_test.go`
- Modify: `internal/authz/access.go` (add private flag, adjust validation, hide flag from JSON)

**Interfaces:**
- Consumes: `authz.System`, `authz.Permission`, `authz.AccessPolicy`, `authz.SuperadminRequired`, `authz.ScopeSystem`, `authz.PermissionSystemSubscriptionOverride` (for tests).
- Produces:
  - `authz.SystemOnProjectPath(permission Permission, projectIDPathParam string) AccessPolicy` — helper returning an `AccessPolicy` with `Mode=RBAC`, `Scope=System`, `Superadmin=SuperadminRequired`, `ProjectIDPathParam=projectIDPathParam`, `Permission=permission`, and a private flag set so `Validate()` accepts the combination.
  - `Validate()` continues to reject `ProjectIDPathParam` under system scope for policies **not** built via this helper.
  - `MarshalJSON` on `AccessPolicy` continues to emit `mode`, `scope`, `permission`, `projectIdPathParam`, `resource`, `conditions`, `superadmin` — no new fields.

- [ ] **Step 1: Write failing tests**

Create `internal/authz/system_on_project_path_test.go`:

```go
package authz

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemOnProjectPathBuildsValidPolicy(t *testing.T) {
	t.Parallel()

	access := SystemOnProjectPath(PermissionSystemSubscriptionOverride, "projectID")
	if err := access.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if access.Mode != AccessModeRBAC {
		t.Errorf("Mode = %q, want %q", access.Mode, AccessModeRBAC)
	}
	if access.Scope != ScopeSystem {
		t.Errorf("Scope = %q, want %q", access.Scope, ScopeSystem)
	}
	if access.Superadmin != SuperadminRequired {
		t.Errorf("Superadmin = %q, want %q", access.Superadmin, SuperadminRequired)
	}
	if access.Permission != PermissionSystemSubscriptionOverride {
		t.Errorf("Permission = %q, want %q", access.Permission, PermissionSystemSubscriptionOverride)
	}
	if access.ProjectIDPathParam != "projectID" {
		t.Errorf("ProjectIDPathParam = %q, want projectID", access.ProjectIDPathParam)
	}
}

func TestSystemOnProjectPathRejectsBlankParam(t *testing.T) {
	t.Parallel()

	access := SystemOnProjectPath(PermissionSystemSubscriptionOverride, "  ")
	if err := access.Validate(); err == nil || !strings.Contains(err.Error(), "project path parameter") {
		t.Fatalf("Validate() error = %v, want project-path-parameter error", err)
	}
}

func TestSystemOnProjectPathRejectsProjectPermission(t *testing.T) {
	t.Parallel()

	access := SystemOnProjectPath(PermissionProjectRead, "projectID")
	if err := access.Validate(); err == nil || !strings.Contains(err.Error(), "has scope") {
		t.Fatalf("Validate() error = %v, want scope error", err)
	}
}

// The already-existing System() helper must still reject a manually set
// ProjectIDPathParam — SystemOnProjectPath is the only opt-in.
func TestSystemWithoutOptInRejectsProjectParam(t *testing.T) {
	t.Parallel()

	access := System(PermissionSystemSubscriptionOverride, func(a *AccessPolicy) {
		a.ProjectIDPathParam = "projectID"
	})
	if err := access.Validate(); err == nil || !strings.Contains(err.Error(), "cannot declare a project") {
		t.Fatalf("Validate() error = %v, want cannot-declare error", err)
	}
}

func TestSystemOnProjectPathJSONDoesNotLeakOptInFlag(t *testing.T) {
	t.Parallel()

	access := SystemOnProjectPath(PermissionSystemSubscriptionOverride, "projectID")
	encoded, err := json.Marshal(access)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	got := string(encoded)
	for _, needle := range []string{
		"\"mode\":\"rbac\"",
		"\"scope\":\"system\"",
		"\"permission\":\"system.subscription.override\"",
		"\"projectIdPathParam\":\"projectID\"",
		"\"superadmin\":\"required\"",
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("JSON %s does not contain %s", got, needle)
		}
	}
	for _, forbidden := range []string{"systemOnProjectPath", "sysProjectPath", "optIn"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("JSON %s leaked internal flag %s", got, forbidden)
		}
	}
}
```

- [ ] **Step 2: Run and verify tests fail**

```
go test ./internal/authz/ -run 'TestSystemOnProjectPath|TestSystemWithoutOptInRejectsProjectParam' -v
```

Expected: FAIL — `SystemOnProjectPath` undefined; the `TestSystemWithoutOptInRejectsProjectParam` case may already pass because access.go rejects the combination today. That is fine; it becomes a regression guard once the helper lands.

- [ ] **Step 3: Add the private flag to `AccessPolicy`**

Modify `internal/authz/access.go`. Replace the `AccessPolicy` struct declaration (lines 68-76) with:

```go
// AccessPolicy is the single authorization descriptor attached to an API
// operation. It is suitable both for runtime authorization and for generating
// OpenAPI vendor-extension metadata.
type AccessPolicy struct {
	Mode               AccessMode       `json:"mode"`
	Scope              Scope            `json:"scope"`
	Permission         Permission       `json:"permission,omitempty"`
	ProjectIDPathParam string           `json:"projectIdPathParam,omitempty"`
	Resource           *ResourceBinding `json:"resource,omitempty"`
	Policies           []PolicyID       `json:"conditions,omitempty"`
	Superadmin         SuperadminRule   `json:"superadmin"`

	// systemOnProjectPath is set only by SystemOnProjectPath(). It permits a
	// non-empty ProjectIDPathParam under system scope for audit + resolver
	// hookup. Kept unexported so callers cannot forge the combination.
	systemOnProjectPath bool
}
```

Update `validateRBAC` (currently rejecting `ProjectIDPathParam` under system scope) to allow the combination when `a.systemOnProjectPath` is true, and to require a non-blank param when the flag is set:

Locate the system-scope branch inside `validateRBAC`:

```go
	case ScopeSystem:
		if a.Superadmin != SuperadminRequired {
			return fmt.Errorf("authz: system access must explicitly require superadmin")
		}
		if a.ProjectIDPathParam != "" {
			return fmt.Errorf("authz: system access cannot declare a project path parameter")
		}
```

Replace with:

```go
	case ScopeSystem:
		if a.Superadmin != SuperadminRequired {
			return fmt.Errorf("authz: system access must explicitly require superadmin")
		}
		if a.systemOnProjectPath {
			if strings.TrimSpace(a.ProjectIDPathParam) == "" {
				return fmt.Errorf("authz: SystemOnProjectPath requires a project path parameter")
			}
		} else if a.ProjectIDPathParam != "" {
			return fmt.Errorf("authz: system access cannot declare a project path parameter")
		}
```

- [ ] **Step 4: Confirm `strings` is imported**

The file already imports `strings` (see `internal/authz/access.go:6`). No change needed.

- [ ] **Step 5: Add `SystemOnProjectPath` helper**

Create `internal/authz/system_on_project_path.go`:

```go
package authz

// SystemOnProjectPath returns an AccessPolicy that requires an explicit
// superadmin (system scope) but is mounted at a route with a project path
// parameter. Runtime authorization is identical to System(); the project
// path parameter is used only for audit context and resource resolution
// on writes such as subscription overrides.
//
// The permission must be system-scoped. The project path parameter must
// be non-blank. Validate() enforces both.
func SystemOnProjectPath(permission Permission, projectIDPathParam string) AccessPolicy {
	access := System(permission)
	access.ProjectIDPathParam = projectIDPathParam
	access.systemOnProjectPath = true
	return access
}
```

- [ ] **Step 6: Run the new tests and verify they pass**

```
go test ./internal/authz/ -run 'TestSystemOnProjectPath|TestSystemWithoutOptInRejectsProjectParam' -v
```

Expected: PASS.

- [ ] **Step 7: Run the full authz suite to make sure nothing regressed**

```
go test ./internal/authz/ -v
```

Expected: PASS. In particular, `TestAccessPolicyValidateRejectsInvalidDescriptors`' "system with project parameter" case still fails validation because it uses `System(...)`, not `SystemOnProjectPath`. `TestAccessPolicyJSONMetadata` still matches its exact substrings because the new field is unexported.

- [ ] **Step 8: Commit**

```bash
git add internal/authz/access.go internal/authz/system_on_project_path.go internal/authz/system_on_project_path_test.go
git commit -m "feat(authz): SystemOnProjectPath opt-in DSL helper

Adds an opt-in flag on AccessPolicy so system-scoped policies can carry
a project path parameter for audit + resource resolution while still
requiring an explicit superadmin at runtime. The manual (System + set
field) route continues to fail Validate().

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Promote RegisterWithLegacyTrailingSlash into `contract`

**Files:**
- Create: `internal/api/contract/legacy_alias.go`
- Create: `internal/api/contract/legacy_alias_test.go`
- Modify: `internal/api/admin/v1/operations.go:151-168` (delete the local helper; call the promoted one)

**Interfaces:**
- Consumes: `contract.Operation`, `contract.Register`, `huma.API`.
- Produces:
  - `contract.RegisterWithLegacyTrailingSlash[I, O any](api huma.API, operation Operation, handler func(context.Context, *I) (*O, error))` — registers the operation as-is, then registers a hidden alias at `operation.Path + "/"` with `operation.ID + "LegacyTrailingSlash"`. Panics on `operation.ID == ""` (via `contract.Register`). The alias shares the exact same `AccessPolicy` and `Authorize` factory as the canonical operation.

- [ ] **Step 1: Write failing test**

Create `internal/api/contract/legacy_alias_test.go`:

```go
package contract

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/authz"
)

type aliasInput struct{}
type aliasOutput struct {
	Body map[string]string
}

func TestRegisterWithLegacyTrailingSlashRegistersBothSpellings(t *testing.T) {
	router := chi.NewRouter()
	api := NewAdminAPI(router, APIOptions{})

	RegisterWithLegacyTrailingSlash(api, Operation{
		ID:            "listExamples",
		Method:        http.MethodGet,
		Path:          "/api/v1/examples",
		DefaultStatus: http.StatusOK,
		Access:        authz.Public(),
	}, func(context.Context, *aliasInput) (*aliasOutput, error) {
		return &aliasOutput{Body: map[string]string{"ok": "yes"}}, nil
	})

	for _, target := range []string{"/api/v1/examples", "/api/v1/examples/"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, body = %s", target, recorder.Code, recorder.Body.String())
		}
	}

	// The canonical path is present in the OpenAPI document; the trailing-slash
	// alias is hidden.
	if api.OpenAPI().Paths["/api/v1/examples"] == nil {
		t.Fatal("canonical path missing from OpenAPI")
	}
	if api.OpenAPI().Paths["/api/v1/examples/"] != nil {
		t.Fatal("trailing-slash alias was emitted into OpenAPI")
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

```
go test ./internal/api/contract/ -run TestRegisterWithLegacyTrailingSlashRegistersBothSpellings -v
```

Expected: FAIL — `RegisterWithLegacyTrailingSlash` undefined.

- [ ] **Step 3: Add the helper**

Create `internal/api/contract/legacy_alias.go`:

```go
package contract

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterWithLegacyTrailingSlash installs a typed operation at the given
// canonical path, then registers a hidden alias whose path has a trailing
// slash. The alias runs the same handler under the same AccessPolicy so
// legacy Dashboard clients still work, but the emitted OpenAPI contract
// keeps a single canonical URL.
func RegisterWithLegacyTrailingSlash[I, O any](
	api huma.API,
	operation Operation,
	handler func(context.Context, *I) (*O, error),
) {
	Register(api, operation, handler)

	alias := operation
	alias.ID += "LegacyTrailingSlash"
	alias.Path += "/"
	alias.Hidden = true
	Register(api, alias, handler)
}
```

- [ ] **Step 4: Run test and verify it passes**

```
go test ./internal/api/contract/ -run TestRegisterWithLegacyTrailingSlashRegistersBothSpellings -v
```

Expected: PASS.

- [ ] **Step 5: Rewrite `internal/api/admin/v1/operations.go` to consume the promoted helper**

Delete the local helper at `internal/api/admin/v1/operations.go:151-168` (the `registerWithLegacyTrailingSlash[...]` function). Replace every existing call site in that same file (currently 4 call sites: `listProjectsOperation`, `getProjectOperation`, plus two calls inside `plans.go` and `users.go`) with `contract.RegisterWithLegacyTrailingSlash`.

Search-and-replace guide inside `internal/api/admin/v1/`:

```
old: registerWithLegacyTrailingSlash(
new: contract.RegisterWithLegacyTrailingSlash(
```

Then confirm `internal/api/admin/v1/plans.go` and `internal/api/admin/v1/users.go` also import `contract` (they already do, see the existing `import` blocks — `plans.go:8` and `users.go:8`).

Delete the now-unused `registerWithLegacyTrailingSlash` symbol from `operations.go`. Verify with:

```
grep -rn "registerWithLegacyTrailingSlash" internal/
```

Expected: zero matches.

- [ ] **Step 6: Run all Go tests**

```
go test ./...
```

Expected: PASS. Existing `TestTypedAdminRoutesCoexistWithLegacyMount` (which exercises the trailing-slash variants of `/users/`, `/plans/`, etc.) is the main regression guard here.

- [ ] **Step 7: Commit**

```bash
git add internal/api/contract/legacy_alias.go internal/api/contract/legacy_alias_test.go internal/api/admin/v1/operations.go
git commit -m "refactor(contract): promote RegisterWithLegacyTrailingSlash out of adminv1

Multiple upcoming subsystem migrations need this helper; moving it into
the contract package avoids per-subsystem re-implementations and keeps
the trailing-slash aliases hidden from OpenAPI.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Add `contract.BytesResponse` output type

**Files:**
- Create: `internal/api/contract/bytes_response.go`
- Create: `internal/api/contract/bytes_response_test.go`

**Interfaces:**
- Consumes: `huma.API`, `huma.Operation`, `huma.Register` (indirectly through `contract.Register`).
- Produces:
  - `contract.BytesResponse struct { ContentType string; ContentLength int64; Body io.Reader }` — a Huma output body type whose `application/octet-stream` payload is streamed as-is. `ContentType` empty → default `application/octet-stream`.
  - The struct's `Content-Length` and `Content-Type` headers appear via struct fields Huma recognizes (`ContentType string \`header:"Content-Type"\``, `ContentLength int64 \`header:"Content-Length,omitempty"\``).
  - Registers responds with a `responses.200.content.application/octet-stream: {schema: {type: string, format: binary}}` entry when returned from a Huma handler.

- [ ] **Step 1: Write failing test**

Create `internal/api/contract/bytes_response_test.go`:

```go
package contract

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/authz"
)

type bytesInput struct{}
type bytesOutput struct {
	ContentType   string `header:"Content-Type"`
	ContentLength int64  `header:"Content-Length,omitempty"`
	Body          BytesResponse
}

func TestBytesResponseStreamsBinaryPayload(t *testing.T) {
	router := chi.NewRouter()
	api := NewAdminAPI(router, APIOptions{})

	payload := []byte{0x00, 0x01, 0x02, 0x03}
	Register(api, Operation{
		ID:            "downloadBlob",
		Method:        http.MethodGet,
		Path:          "/blob",
		DefaultStatus: http.StatusOK,
		Access:        authz.Public(),
	}, func(context.Context, *bytesInput) (*bytesOutput, error) {
		return &bytesOutput{
			ContentType:   "application/octet-stream",
			ContentLength: int64(len(payload)),
			Body:          BytesResponse{Reader: bytes.NewReader(payload)},
		}, nil
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/blob", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q", got)
	}
	got, _ := io.ReadAll(recorder.Body)
	if !bytes.Equal(got, payload) {
		t.Fatalf("body = %v, want %v", got, payload)
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

```
go test ./internal/api/contract/ -run TestBytesResponseStreamsBinaryPayload -v
```

Expected: FAIL — `BytesResponse` undefined.

- [ ] **Step 3: Implement `BytesResponse`**

Create `internal/api/contract/bytes_response.go`:

```go
package contract

import (
	"io"

	"github.com/danielgtaylor/huma/v2"
)

// BytesResponse is an opaque binary body carried by an operation output
// struct. Huma streams it to the response verbatim when the field is
// named "Body" inside the operation's output struct and Content-Type
// resolves to application/octet-stream (or another explicit value set
// via a Content-Type header field on the same struct).
//
// The Reader is drained but never closed by Huma. Handlers that need
// to close a resource should wrap it in an io.NopCloser and close on
// their own after the handler returns.
type BytesResponse struct {
	Reader io.Reader
}

// Schema returns a raw binary payload schema so openapi-typescript
// emits it as `string` (Blob) instead of trying to type it structurally.
func (BytesResponse) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type:            "string",
		Format:          "binary",
		ContentEncoding: "binary",
	}
}

// ContentType returns the MIME type Huma advertises for this body when
// no Content-Type header field is set on the operation output.
func (BytesResponse) ContentType() string {
	return "application/octet-stream"
}
```

- [ ] **Step 4: Run test and verify it passes**

```
go test ./internal/api/contract/ -run TestBytesResponseStreamsBinaryPayload -v
```

Expected: PASS. If Huma refuses to stream the reader as-is (returns an unexpected 500 wrapping) the fix is to add `Reader io.Reader` as the untyped body field expected by Huma's raw-body reflection. The test enforces the correct wire behavior; adjust the implementation until the test passes.

- [ ] **Step 5: Commit**

```bash
git add internal/api/contract/bytes_response.go internal/api/contract/bytes_response_test.go
git commit -m "feat(contract): add BytesResponse output for binary payloads

Http-log downloads (batch 12) need to stream application/octet-stream
bodies without going through Huma's JSON schema pipeline. BytesResponse
provides a stable operation-output shape that both the runtime handler
and the emitted OpenAPI schema can use.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: Add resolver registry scaffold under adminv1

**Files:**
- Create: `internal/api/admin/v1/resolvers/registry.go`
- Create: `internal/api/admin/v1/resolvers/registry_test.go`

**Interfaces:**
- Consumes: `authz.ResourceResolver`.
- Produces:
  - Package path `github.com/modelserver/modelserver/internal/api/admin/v1/resolvers`.
  - `resolvers.Registry` — `type Registry map[string]authz.ResourceResolver`.
  - `resolvers.New() Registry` — returns an empty registry.
  - `(Registry) Register(resourceType string, resolver authz.ResourceResolver)` — panics on empty type or duplicate registration.
  - `resolvers.Default() Registry` — currently returns an empty registry (later batches will call `Default().Register(...)` from init functions colocated with each subsystem's resolver).

- [ ] **Step 1: Write failing test**

Create `internal/api/admin/v1/resolvers/registry_test.go`:

```go
package resolvers

import (
	"context"
	"testing"

	"github.com/modelserver/modelserver/internal/authz"
)

type stubResolver struct{ projectID string }

func (r stubResolver) Resolve(_ context.Context, ref authz.ResourceReference) (authz.Resource, error) {
	return authz.Resource{Type: ref.Type, ID: ref.ID, ProjectID: r.projectID}, nil
}

func TestRegistryRegisterAndLookup(t *testing.T) {
	t.Parallel()

	registry := New()
	registry.Register("api-key", stubResolver{projectID: "p1"})

	resolver, ok := registry["api-key"]
	if !ok {
		t.Fatal("api-key resolver missing after registration")
	}
	resource, err := resolver.Resolve(context.Background(), authz.ResourceReference{Type: "api-key", ID: "k1"})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if resource.ProjectID != "p1" {
		t.Fatalf("Resource.ProjectID = %q", resource.ProjectID)
	}
}

func TestRegistryPanicsOnEmptyType(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("Register with empty type did not panic")
		}
	}()
	New().Register("", stubResolver{})
}

func TestRegistryPanicsOnDuplicate(t *testing.T) {
	t.Parallel()

	registry := New()
	registry.Register("api-key", stubResolver{})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate Register did not panic")
		}
	}()
	registry.Register("api-key", stubResolver{})
}

func TestDefaultReturnsEmptyRegistryForNow(t *testing.T) {
	t.Parallel()

	if len(Default()) != 0 {
		t.Fatalf("Default() = %v; expected empty until subsystem batches register resolvers", Default())
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

```
go test ./internal/api/admin/v1/resolvers/ -v
```

Expected: FAIL — the package does not exist yet.

- [ ] **Step 3: Implement the registry**

Create `internal/api/admin/v1/resolvers/registry.go`:

```go
// Package resolvers wires resource resolvers used by admin operations.
// Later batches add resolvers here (api-key, order, subscription, ...).
package resolvers

import (
	"strings"

	"github.com/modelserver/modelserver/internal/authz"
)

// Registry maps a ResourceType (as used in AccessPolicy.Resource.Type) to the
// resolver that loads the resource for runtime authorization.
type Registry map[string]authz.ResourceResolver

// New returns an empty resolver registry.
func New() Registry {
	return Registry{}
}

// Register attaches a resolver to a resource type. It panics on empty type
// or duplicate registration so misconfigurations fail at startup.
func (r Registry) Register(resourceType string, resolver authz.ResourceResolver) {
	if strings.TrimSpace(resourceType) == "" {
		panic("resolvers: resource type must not be empty")
	}
	if resolver == nil {
		panic("resolvers: resolver must not be nil")
	}
	if _, exists := r[resourceType]; exists {
		panic("resolvers: duplicate registration for " + resourceType)
	}
	r[resourceType] = resolver
}

// Default returns the resolver set the admin server uses when no
// registry is supplied. Later batches populate this by mutating the
// returned map from init functions colocated with each subsystem's
// resolver implementation.
func Default() Registry {
	return defaultRegistry
}

var defaultRegistry = New()
```

- [ ] **Step 4: Run test and verify it passes**

```
go test ./internal/api/admin/v1/resolvers/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/admin/v1/resolvers/registry.go internal/api/admin/v1/resolvers/registry_test.go
git commit -m "feat(adminv1/resolvers): resolver registry scaffold

Provides a strongly-typed registration surface for per-subsystem
resource resolvers. Registration panics on empty type, duplicate,
or nil resolver so misconfiguration fails at process start.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Add policy registry that installs the built-ins + new policies

**Files:**
- Create: `internal/api/admin/v1/policies_registry.go`
- Create: `internal/api/admin/v1/policies_registry_test.go`

**Interfaces:**
- Consumes: `authz.Policy`, `authz.PolicyID`, plus concrete policy structs — three placeholder built-ins from `internal/authz` (`PolicyOwnResource`, `PolicyResourceProjectContainment`, `PolicyMemberRoleHierarchy`) and the two new policies from Tasks 3–4.
- Produces:
  - `adminv1.DefaultPolicies() map[authz.PolicyID]authz.Policy` — the map the runtime `Server.Policies` uses when the field is nil.
  - Includes the two new policies (`KeyOwnedByCallerForDeveloperPolicy{}`, `MemberSelfOrElevatedPolicy{}`) and the three built-in IDs pointing at empty stub `authz.Policy` implementations **only where they already exist**. If a built-in has no concrete `authz.Policy` struct in the repository yet, it stays absent from the map so the runtime middleware's "unknown policy → deny" branch keeps guarding it.

**Important precondition to check first.** The built-in `Policy` implementations (`own-resource`, `resource-project-containment`, `member-role-hierarchy`) may or may not have concrete types under `internal/authz/` today. Run:

```
grep -rn "func .* ID() PolicyID\|var _ Policy = " internal/authz/
```

If only `KeyOwnedByCallerForDeveloperPolicy` and `MemberSelfOrElevatedPolicy` appear (Tasks 3 and 4), then the three built-in IDs stay unwired in Task 9 — they will be wired one at a time as subsequent batches need them. That is the intended behavior; do not fabricate implementations here.

- [ ] **Step 1: Verify built-in Policy implementations status**

Run:

```
grep -rn "ID() PolicyID" internal/authz/
```

Record the output. Only the concrete `Policy` types found may be installed in the registry in Step 3.

- [ ] **Step 2: Write failing test**

Create `internal/api/admin/v1/policies_registry_test.go`:

```go
package adminv1

import (
	"testing"

	"github.com/modelserver/modelserver/internal/authz"
)

func TestDefaultPoliciesInstallsNewPolicies(t *testing.T) {
	t.Parallel()

	policies := DefaultPolicies()

	for _, id := range []authz.PolicyID{
		authz.PolicyKeyOwnedByCallerForDeveloper,
		authz.PolicyMemberSelfOrElevated,
	} {
		policy, ok := policies[id]
		if !ok || policy == nil {
			t.Errorf("DefaultPolicies() missing %q", id)
			continue
		}
		if policy.ID() != id {
			t.Errorf("policy for %q reports ID %q", id, policy.ID())
		}
	}
}
```

- [ ] **Step 3: Run test and verify it fails**

```
go test ./internal/api/admin/v1/ -run TestDefaultPoliciesInstallsNewPolicies -v
```

Expected: FAIL — `DefaultPolicies` undefined.

- [ ] **Step 4: Implement the registry**

Create `internal/api/admin/v1/policies_registry.go`:

```go
package adminv1

import "github.com/modelserver/modelserver/internal/authz"

// DefaultPolicies returns the admin runtime's policy set. Every policy
// referenced by an operation must appear here; missing policies are
// treated as a deny by the authorization middleware.
//
// Built-in policies from internal/authz that do not yet have concrete
// implementations in this repository are absent from this map on
// purpose: middleware defaults to deny, so unwired IDs behave safely.
// Each subsequent batch that starts using a built-in policy registers
// its implementation here alongside the migration.
func DefaultPolicies() map[authz.PolicyID]authz.Policy {
	return map[authz.PolicyID]authz.Policy{
		authz.PolicyKeyOwnedByCallerForDeveloper: authz.KeyOwnedByCallerForDeveloperPolicy{},
		authz.PolicyMemberSelfOrElevated:         authz.MemberSelfOrElevatedPolicy{},
	}
}
```

- [ ] **Step 5: Run test and verify it passes**

```
go test ./internal/api/admin/v1/ -run TestDefaultPoliciesInstallsNewPolicies -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/admin/v1/policies_registry.go internal/api/admin/v1/policies_registry_test.go
git commit -m "feat(adminv1): DefaultPolicies() wires the two new authz policies

Wires KeyOwnedByCallerForDeveloperPolicy and MemberSelfOrElevatedPolicy
into a lookup map the Server will use once the operations that reference
them arrive. Built-in policies stay absent for now; middleware treats
unknown IDs as deny.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: Add empty per-subsystem store interface stubs

**Files:**
- Create: `internal/api/admin/v1/subsystem_stores.go`

**Interfaces:**
- Consumes: nothing.
- Produces: 22 empty Go interface stubs, one per subsystem letter in spec §2. Purpose is documentation + collision-free growth surface for Batches 2–14.

- [ ] **Step 1: Create the stub file**

Create `internal/api/admin/v1/subsystem_stores.go`:

```go
package adminv1

// Per-subsystem store interfaces. Each subsystem migration in Batches
// 2..14 grows its own interface with the exact methods it needs. Keep
// the interfaces empty until the corresponding batch starts so unused
// methods do not accumulate on *Server.
//
// The subsystem letters correspond to the table in
// docs/superpowers/specs/2026-07-11-huma-admin-api-and-rbac-design.md,
// section §2.

type authStore interface{}          // A — Auth (public)
type usersWriteStore interface{}    // B — Users writes
type plansWriteStore interface{}    // C — Plans writes
type modelsStore interface{}        // D — Models catalog
type adminSuperStore interface{}    // E — Admin (superadmin)
type notificationsStore interface{} // F — Notifications user + admin
type extraUsageStore interface{}    // G — Extra usage user + admin
type projectsStore interface{}      // H — Projects CRUD
type membersStore interface{}       // I — Project members
type keysStore interface{}          // J — API Keys
type oauthGrantsStore interface{}   // K — OAuth grants
type policiesStore interface{}      // L — Project policies
type subscriptionsStore interface{} // M — Subscriptions
type ordersStore interface{}        // N — Available plans + Orders
type tracesStore interface{}        // O — Traces
type requestsStore interface{}      // P — Requests + http-log
type usageStore interface{}         // Q — Usage
type upstreamsStore interface{}     // R — Upstreams
type upstreamGroupsStore interface{} // S — Upstream groups
type oauthClientsStore interface{}  // T — OAuth clients
type routingStore interface{}       // U — Routing
type selfProjectStore interface{}   // V — My-quota / my-membership
```

- [ ] **Step 2: Verify the package still builds**

```
go build ./internal/api/admin/v1/
```

Expected: build succeeds (empty interfaces are legal Go and cause no lint noise beyond "unused type" — which `go build` does not warn about).

- [ ] **Step 3: Commit**

```bash
git add internal/api/admin/v1/subsystem_stores.go
git commit -m "chore(adminv1): scaffold per-subsystem store interface stubs

Empty interfaces per spec §2 subsystem letter. Later batches attach
methods and wire fields on *Server. No consumer today.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 11: Wire Server defaults for resolvers + policies (behavior-preserving)

**Files:**
- Modify: `internal/api/admin/v1/server.go` (extend the Server initialization path so `nil` fields fall back to the new defaults on first use, without changing today's tests)
- Create: `internal/api/admin/v1/server_defaults_test.go`

**Interfaces:**
- Consumes: `DefaultPolicies()` (Task 9), `resolvers.Default()` (Task 8), existing `Server.Resolvers`, `Server.Policies`.
- Produces:
  - `(*Server).effectivePolicies() map[authz.PolicyID]authz.Policy` — returns `s.Policies` if non-nil, else `DefaultPolicies()`.
  - `(*Server).effectiveResolvers() map[string]authz.ResourceResolver` — returns `s.Resolvers` if non-nil, else `resolvers.Default()`.
  - The `authorizationMiddleware` internal call sites (currently `s.Resolvers[...]` and `s.Policies[...]`) switch to the new helpers. Runtime behavior is unchanged for every existing test because the existing typed operations never trigger the resource / policy branches.

- [ ] **Step 1: Write failing test**

Create `internal/api/admin/v1/server_defaults_test.go`:

```go
package adminv1

import (
	"testing"

	"github.com/modelserver/modelserver/internal/authz"
)

func TestServerEffectivePoliciesFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	server := &Server{}
	got := server.effectivePolicies()
	for _, id := range []authz.PolicyID{
		authz.PolicyKeyOwnedByCallerForDeveloper,
		authz.PolicyMemberSelfOrElevated,
	} {
		if _, ok := got[id]; !ok {
			t.Errorf("default effective policies missing %q", id)
		}
	}
}

func TestServerEffectivePoliciesRespectsExplicit(t *testing.T) {
	t.Parallel()

	explicit := map[authz.PolicyID]authz.Policy{
		authz.PolicyOwnResource: nil, // marker only
	}
	server := &Server{Policies: explicit}
	got := server.effectivePolicies()
	if _, ok := got[authz.PolicyOwnResource]; !ok {
		t.Fatal("explicit policies were replaced by defaults")
	}
	if _, ok := got[authz.PolicyKeyOwnedByCallerForDeveloper]; ok {
		t.Fatal("defaults leaked into explicit policies")
	}
}

func TestServerEffectiveResolversFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	server := &Server{}
	got := server.effectiveResolvers()
	if got == nil {
		t.Fatal("effectiveResolvers() returned nil")
	}
	// Task 8's Default() is currently empty; verify shape only.
	if len(got) != 0 {
		t.Errorf("default resolver registry has %d entries; expected 0 until subsystem batches register", len(got))
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

```
go test ./internal/api/admin/v1/ -run 'TestServerEffective' -v
```

Expected: FAIL — helpers undefined.

- [ ] **Step 3: Add the helpers in `internal/api/admin/v1/server.go`**

Add these two methods immediately after the existing `Server` struct declaration in `internal/api/admin/v1/server.go`:

```go
// effectivePolicies returns the caller-supplied policies map when set,
// falling back to DefaultPolicies(). Callers must not mutate the result.
func (s *Server) effectivePolicies() map[authz.PolicyID]authz.Policy {
	if s.Policies != nil {
		return s.Policies
	}
	return DefaultPolicies()
}

// effectiveResolvers returns the caller-supplied resolver registry when
// set, falling back to the shared default registry. Callers must not
// mutate the result.
func (s *Server) effectiveResolvers() map[string]authz.ResourceResolver {
	if s.Resolvers != nil {
		return s.Resolvers
	}
	return resolvers.Default()
}
```

Add the necessary import: change the existing `internal/api/admin/v1/server.go` import block to include `"github.com/modelserver/modelserver/internal/api/admin/v1/resolvers"`.

- [ ] **Step 4: Route the middleware through the helpers**

In the same file, inside `resolveAndEvaluatePolicies`, replace:

```go
		resolver := s.Resolvers[access.Resource.ResourceType]
```

with:

```go
		resolver := s.effectiveResolvers()[access.Resource.ResourceType]
```

Replace:

```go
		policy := s.Policies[policyID]
```

with:

```go
		policy := s.effectivePolicies()[policyID]
```

- [ ] **Step 5: Run new tests and existing suite**

```
go test ./internal/api/admin/v1/ -v
```

Expected: PASS — including the existing `TestCommittedOpenAPIDocumentIsCurrent` (unchanged because Register() is unchanged) and the routing test in `cmd/modelserver/admin_routes_test.go`.

Also run:

```
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/admin/v1/server.go internal/api/admin/v1/server_defaults_test.go
git commit -m "feat(adminv1): Server falls back to default policies + resolvers

Explicit Server.Policies / Server.Resolvers still win. When left nil,
the middleware now consults DefaultPolicies() and resolvers.Default(),
so future subsystem batches can register resolvers via init() without
each caller having to build a bespoke map.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 12: Add spec-invariant tests in `internal/api/contract`

**Files:**
- Create: `internal/api/contract/invariants_test.go`
- Modify: `internal/api/admin/v1/openapi_test.go` (add one helper if invariants need to walk admin-registered routes)

**Interfaces:**
- Consumes: `contract.NewAdminAPI`, `contract.RegisterWithLegacyTrailingSlash`, `adminv1.Register`, `authz.AccessPolicy`, `authz.AllPermissions`.
- Produces: four Go tests in package `contract`:
  - `TestNoDualRegistrationInsideHuma` — proves no `(method, canonical path)` pair is registered more than once from Huma's side (the trailing-slash aliases are excluded because they use the alias path). This validates the contract layer directly; a broader chi-vs-huma dual-registration guard belongs to the specific batch that starts moving routes out of chi.
  - `TestEveryOperationHasCatalogPermission` — walks the admin API document and requires that every operation's `x-modelserver-authz.permission` is either empty (public / authenticated / hmac) or a member of `authz.AllPermissions()`.
  - `TestEveryResourceHasResolver` — walks the admin API document and requires every `resource.type` used by any operation to be present in `resolvers.Default()`. **This test is skipped with `t.Skip("no operations use resource resolvers yet")` when Default() is empty AND no operation declares a Resource** so Batch 1 lands green. The skip disappears automatically when Batch 8 wires the `member` resolver (Task 11 of that batch will remove the skip guard here).
  - `TestAuthzExtensionShape` — for each operation carrying `x-modelserver-authz`, round-trip the extension through `json.Marshal` → `json.Unmarshal` into `authz.AccessPolicy` and assert `Validate()` passes.

- [ ] **Step 1: Write the four invariant tests**

Create `internal/api/contract/invariants_test.go`:

```go
package contract

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	adminv1 "github.com/modelserver/modelserver/internal/api/admin/v1"
	"github.com/modelserver/modelserver/internal/api/admin/v1/resolvers"
	"github.com/modelserver/modelserver/internal/authz"
)

// buildAdminSpec returns the OpenAPI document produced by the current
// admin.v1.Register call. Kept local because the invariants test what
// contract exposes, not adminv1 internals.
func buildAdminSpec(t *testing.T) *huma.OpenAPI {
	t.Helper()
	router := chi.NewRouter()
	api := NewAdminAPI(router, APIOptions{})
	adminv1.Register(api, nil)
	return api.OpenAPI()
}

func TestNoDualRegistrationInsideHuma(t *testing.T) {
	t.Parallel()

	document := buildAdminSpec(t)
	seen := make(map[string]struct{})
	for path, item := range document.Paths {
		for method, operation := range map[string]*huma.Operation{
			"GET":    item.Get,
			"PUT":    item.Put,
			"POST":   item.Post,
			"DELETE": item.Delete,
			"PATCH":  item.Patch,
		} {
			if operation == nil {
				continue
			}
			key := method + " " + path
			if _, dup := seen[key]; dup {
				t.Errorf("duplicate operation for %s", key)
			}
			seen[key] = struct{}{}
		}
	}
}

func TestEveryOperationHasCatalogPermission(t *testing.T) {
	t.Parallel()

	catalog := make(map[authz.Permission]struct{}, len(authz.AllPermissions()))
	for _, permission := range authz.AllPermissions() {
		catalog[permission] = struct{}{}
	}

	forEachOperation(t, func(method, path string, operation *huma.Operation) {
		raw, ok := operation.Extensions["x-modelserver-authz"]
		if !ok {
			return
		}
		access, ok := decodeAccessExtension(t, raw)
		if !ok {
			return
		}
		if access.Permission == "" {
			return
		}
		if _, present := catalog[access.Permission]; !present {
			t.Errorf("%s %s: permission %q not in authz.AllPermissions()", method, path, access.Permission)
		}
	})
}

func TestEveryResourceHasResolver(t *testing.T) {
	t.Parallel()

	registry := resolvers.Default()
	sawResource := false
	forEachOperation(t, func(method, path string, operation *huma.Operation) {
		raw, ok := operation.Extensions["x-modelserver-authz"]
		if !ok {
			return
		}
		access, ok := decodeAccessExtension(t, raw)
		if !ok {
			return
		}
		if access.Resource == nil {
			return
		}
		sawResource = true
		if _, present := registry[access.Resource.ResourceType]; !present {
			t.Errorf("%s %s: resource type %q missing from resolvers.Default()",
				method, path, access.Resource.ResourceType)
		}
	})

	if !sawResource && len(registry) == 0 {
		t.Skip("no operations use resource resolvers yet; test becomes active once a subsystem batch declares Resource bindings")
	}
}

func TestAuthzExtensionShape(t *testing.T) {
	t.Parallel()

	forEachOperation(t, func(method, path string, operation *huma.Operation) {
		raw, ok := operation.Extensions["x-modelserver-authz"]
		if !ok {
			return
		}
		access, ok := decodeAccessExtension(t, raw)
		if !ok {
			return
		}
		if err := access.Validate(); err != nil {
			t.Errorf("%s %s: authz extension fails Validate(): %v", method, path, err)
		}
	})
}

func forEachOperation(t *testing.T, fn func(method, path string, op *huma.Operation)) {
	t.Helper()
	document := buildAdminSpec(t)
	for path, item := range document.Paths {
		for method, op := range map[string]*huma.Operation{
			"GET":    item.Get,
			"PUT":    item.Put,
			"POST":   item.Post,
			"DELETE": item.Delete,
			"PATCH":  item.Patch,
		} {
			if op == nil {
				continue
			}
			fn(method, path, op)
		}
	}
}

func decodeAccessExtension(t *testing.T, raw any) (authz.AccessPolicy, bool) {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Errorf("marshal authz extension: %v", err)
		return authz.AccessPolicy{}, false
	}
	var access authz.AccessPolicy
	if err := json.Unmarshal(encoded, &access); err != nil {
		if strings.Contains(err.Error(), "cannot unmarshal") {
			// A future non-object extension would land here; treat as skip.
			return authz.AccessPolicy{}, false
		}
		t.Errorf("unmarshal authz extension: %v", err)
		return authz.AccessPolicy{}, false
	}
	return access, true
}
```

- [ ] **Step 2: Run the invariants**

```
go test ./internal/api/contract/ -run 'TestNoDualRegistrationInsideHuma|TestEveryOperationHasCatalogPermission|TestEveryResourceHasResolver|TestAuthzExtensionShape' -v
```

Expected:
- `TestNoDualRegistrationInsideHuma` — PASS (no dup registrations today).
- `TestEveryOperationHasCatalogPermission` — PASS (all 11 already-migrated operations use catalog permissions).
- `TestEveryResourceHasResolver` — SKIP (no operations declare Resource bindings yet).
- `TestAuthzExtensionShape` — PASS (every existing operation's extension round-trips and validates).

If any of these fail on the current tree, do not "fix them forward" here — instead file a follow-up and abort this task; a failure means the pre-Batch-1 tree drifted from the spec.

- [ ] **Step 3: Run the full Go suite once more**

```
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/api/contract/invariants_test.go
git commit -m "test(contract): spec-invariant guards on the admin API document

Four package-local tests keep the emitted OpenAPI document honest:
no dual registration, permissions live in the catalog, every
resource has a resolver (skipped until batch 8), and every
x-modelserver-authz value round-trips into a valid AccessPolicy.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 13: Commit the management-contract CI workflow

**Files:**
- Modify: `.github/workflows/management-contract.yml` (currently untracked, no content change)

**Interfaces:**
- Consumes: existing `go test ./...`, `pnpm api:check`, `pnpm exec tsc --noEmit`.
- Produces: a CI workflow that gates every PR touching `api/openapi/**`, `cmd/**`, `internal/**`, `dashboard/**`, `go.{mod,sum}`, or the workflow itself.

- [ ] **Step 1: Confirm the file exists and matches spec**

```
git status --short .github/workflows/management-contract.yml
```

Expected: `??  .github/workflows/management-contract.yml`.

Read the file (already shown in this plan's research phase) and verify no changes are needed — content matches §8 of the spec.

- [ ] **Step 2: Verify the invariant tests will run in CI**

The workflow's `Test Go contracts and runtime routes` step runs `go test ./...`, which includes the four invariants added in Task 12. No workflow edit is needed.

- [ ] **Step 3: Stage and commit**

```bash
git add .github/workflows/management-contract.yml
git commit -m "ci: add management API contract workflow

Runs go test ./... plus dashboard api:check + tsc --noEmit on any PR
touching Go source, the OpenAPI document, or the dashboard client. The
four contract invariants added in the same batch execute inside
go test ./...

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 14: Regenerate + verify the OpenAPI document (no expected diff)

**Files:**
- Modify (only if drift): `api/openapi/admin.openapi.json`

**Interfaces:** none.

- [ ] **Step 1: Regenerate**

```
cd /root/coding/modelserver
go run ./cmd/openapi
```

- [ ] **Step 2: Diff**

```
git diff api/openapi/admin.openapi.json
```

Expected: **empty diff**. Batch 1 registers no new operations; a non-empty diff means one of the earlier tasks accidentally changed operation metadata or the emitted schema. Investigate before committing.

- [ ] **Step 3: Run the drift test**

```
go test ./internal/api/admin/v1/ -run TestCommittedOpenAPIDocumentIsCurrent -v
```

Expected: PASS.

- [ ] **Step 4: If Step 2 was empty, skip this commit**

If `git diff` returned nothing, no commit is needed for this task. Otherwise, investigate: identify which Task changed the emitted OpenAPI. Options: (a) revert the accidental behavior change, or (b) accept the diff and commit it under `chore(openapi): regenerate after Batch 1 …` — but only after confirming the change is desirable and re-running Tasks 1–13's tests.

- [ ] **Step 5: Final full-repo verification**

```
go test ./...
cd dashboard
pnpm install --frozen-lockfile
pnpm api:check
pnpm exec tsc --noEmit
cd ..
```

Expected: all four commands PASS. If `pnpm install` requires network access that is unavailable in the local dev environment, note it and rely on CI (the workflow committed in Task 13) as the authoritative check.

---

## Self-Review Notes

This plan was self-reviewed after writing. Notes intentionally left inline:

- Every task ends with an independently reviewable commit and gated by tests that fail before and pass after. Task 14's terminal check runs both Go and dashboard test surfaces.
- Types used across tasks are declared where they first appear: `KeyOwnedByCallerForDeveloperPolicy` (Task 3) and `MemberSelfOrElevatedPolicy` (Task 4) are consumed only from Task 9 (`DefaultPolicies()`) and Task 11 (`effectivePolicies()`). `SystemOnProjectPath` (Task 5) is defined here and referenced by Subscription operations in Batch 11; no cross-file signature drift.
- Placeholders were scanned: no "TBD", "later", or "similar to …". Task 12 documents its own skip condition and the automatic re-activation trigger.
- Spec coverage:
  - §3.1 permissions — Task 1.
  - §3.1 policy IDs + implementations — Tasks 2, 3, 4.
  - §3.1 `SystemOnProjectPath` — Task 5.
  - §3.2 `RegisterWithLegacyTrailingSlash` — Task 6.
  - §3.2 `BytesResponse` — Task 7.
  - §3.3 store interface fanning — Task 10 (empty stubs; each subsystem batch fills its own).
  - §3.4 `authz_middleware.go` split — **deferred**. Splitting `server.go` before any batch consumes body-aware policies is churn without benefit. If a later batch reintroduces body-aware guards, that batch's plan reopens the split. Design note updated implicitly by leaving the file structure intact; no code has been placed that assumes a separate file.
  - §6 guardrails — Task 12 covers all four invariants.
  - §8 CI — Task 13.
- No task references a symbol that does not appear elsewhere in this plan.
