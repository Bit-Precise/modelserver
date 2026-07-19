# Management API OpenAPI and RBAC

## Scope

This contract covers the Admin server's management API only. Proxy and LLM
compatibility endpoints are intentionally excluded, including `/v1/*`,
`/v1beta/*`, `/api/oauth/profile`, streaming, multipart, and provider proxy
behavior.

The migration is incremental. The committed contract currently contains these
migrated read operations:

- `GET /api/v1/auth/config`
- `GET /api/v1/me`
- `GET /api/v1/me/capabilities`
- `GET /api/v1/projects`
- `GET /api/v1/projects/{projectID}`
- `GET /api/v1/projects/{projectID}/capabilities`
- `GET /api/v1/users`
- `GET /api/v1/users/compact`
- `GET /api/v1/users/{userID}`
- `GET /api/v1/plans`
- `GET /api/v1/plans/{planID}`

And these migrated write / public operations (Batch 2):

- `POST /api/v1/auth/refresh`
- `POST /api/v1/auth/oauth/{provider}`
- `GET /api/v1/auth/oauth/{provider}/redirect`

And these migrated superadmin write operations (Batch 3):

- `PUT /api/v1/users/{userID}`
- `POST /api/v1/plans`
- `PUT /api/v1/plans/{planID}`
- `DELETE /api/v1/plans/{planID}`

And these migrated model-catalog operations (Batch 4):

- `GET /api/v1/models`
- `GET /api/v1/models/{name}`
- `POST /api/v1/models`
- `PATCH /api/v1/models/{name}`
- `PUT /api/v1/models/{name}`
- `DELETE /api/v1/models/{name}`

And these migrated admin-reads + notifications operations (Batch 5):

- `GET /api/v1/admin/projects`
- `GET /api/v1/admin/projects/subscriptions-overview`
- `GET /api/v1/admin/requests`
- `GET /api/v1/admin/requests/{requestID}/http-log`
- `GET /api/v1/notifications`
- `GET /api/v1/notifications/unread_count`
- `POST /api/v1/notifications/{id}/read`
- `POST /api/v1/notifications/read_all`
- `GET /api/v1/admin/notifications`
- `POST /api/v1/admin/notifications`
- `GET /api/v1/admin/notifications/{id}`
- `PUT /api/v1/admin/notifications/{id}`
- `DELETE /api/v1/admin/notifications/{id}`

And these migrated extra-usage operations (Batch 6):

- `GET /api/v1/admin/extra-usage/overview`
- `POST /api/v1/admin/extra-usage/projects/{projectID}/topup`
- `PUT /api/v1/admin/extra-usage/projects/{projectID}/bypass`
- `GET /api/v1/projects/{projectID}/extra-usage`
- `PUT /api/v1/projects/{projectID}/extra-usage`
- `GET /api/v1/projects/{projectID}/extra-usage/transactions`
- `POST /api/v1/projects/{projectID}/extra-usage/topup`
- `GET /api/v1/projects/{projectID}/extra-usage/topup/{orderID}`

All other management routes remain on their legacy Chi handlers until they are
individually migrated. A route must not be registered by both implementations.

## Compatibility policy

Migrated routes keep the existing successful response envelopes and fields.
Historical trailing-slash spellings for nested-root project, user-list, and
plan routes remain available as hidden aliases which execute the same typed
handler and `AccessPolicy`; only the canonical paths are emitted into OpenAPI.
Project settings remain raw JSON on the wire so large integers and legacy JSON
values are not changed by an intermediate `map[string]any` conversion.

The following changes are deliberate validation or security boundaries rather
than compatibility promises for valid Dashboard requests:

- empty typed collections are normalized to `data: []` instead of legacy nil
  slice representations such as `data: null`;
- malformed pagination and non-UUID project path parameters are rejected with
  a typed validation error instead of being silently normalized or reaching
  PostgreSQL;
- direct subscription create/update endpoints require an explicit
  superadmin; the normal Dashboard order and signed-webhook flow is unchanged;
- startup requires a JWT secret of at least 32 bytes, and externally issued
  non-HS256 admin tokens are rejected.

Before deploying migration 063, operators with direct database access should
confirm that only built-in roles exist:

```sql
SELECT role, count(*)
FROM project_members
GROUP BY role
ORDER BY role;
```

Unknown historical roles are intentionally downgraded to `developer` by the
migration. Rotating the JWT secret invalidates existing access and refresh
tokens, so secret rotation should be scheduled rather than performed as an
unannounced part of this API migration.

## Contract flow

```text
Go input/output DTOs + typed handler
                 |
                 v
       contract.Operation ---- AccessPolicy
                 |                  | \
                 |                  |  +--> x-modelserver-authz
                 |                  +-----> runtime authorization middleware
                 v
          Huma OpenAPI 3.1
                 |
                 v
        openapi-typescript
                 |
                 v
       openapi-fetch TS client
```

Huma reflects the generic Go handler input and output structs when an operation
is registered. This is how Go types generate OpenAPI without Swagger comment
annotations. The same `adminv1.Register` function is called by both the running
server and the offline generator, so the generated document describes the
actual registered operations.

## Go operation definition

New operations belong under `internal/api/admin/v1`. Each operation declares:

- a stable and unique operation ID;
- HTTP method and complete `/api/v1/...` path;
- typed input and output structs;
- documented error statuses;
- exactly one `authz.AccessPolicy`;
- the management authorization middleware for every non-public operation.

Example:

```go
contract.Register(api, contract.Operation{
    ID:        "getProject",
    Method:    http.MethodGet,
    Path:      "/api/v1/projects/{projectID}",
    Errors:    []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
    Access:    authz.Project(authz.PermissionProjectRead, "projectID"),
    Authorize: server.authorizationMiddleware,
}, server.getProject)
```

Registration fails fast when an access policy is invalid or a protected
operation has no authorizer. There is no implicit authorization default.

Existing v1 response envelopes are preserved:

- one resource: `{"data": ...}`;
- a collection: `{"data": [...], "meta": ...}`;
- an error: `{"error":{"code":"...","message":"...","details":...}}`;
- auth config remains an unwrapped public object.

## Authorization model

`internal/authz` is transport-independent. It defines:

- stable system and project permission IDs;
- `public`, `authenticated`, `rbac`, and `hmac` access modes;
- system and project scopes;
- explicit superadmin rules (`required`, `bypass`, or `none`);
- the owner, maintainer, and developer permission matrix;
- optional resource resolvers and conditional policies.

The same `AccessPolicy` value drives two outputs:

1. runtime JWT, principal, role, permission, resource-containment, and policy
   checks;
2. OpenAPI `security` and `x-modelserver-authz` metadata.

Superadmin access always comes from `Principal.Superadmin`. Missing membership
is never interpreted as superadmin access. Unknown roles and missing resource
resolvers or conditional policies deny by default.

Project resource operations should combine a permission with a resource
binding and, when needed, a condition:

```go
authz.Project(
    authz.PermissionProjectKeysManage,
    "projectID",
    authz.WithResource("api-key", "keyID"),
    authz.WithPolicies(authz.PolicyOwnResource),
)
```

The resource resolver must return the resource's actual project ID. The
authorizer compares it with the project ID asserted by the URL before running
conditions, preventing cross-project IDOR access.

## Generate and consume the contract

Generate OpenAPI from the repository root without a database or running server:

```bash
go run ./cmd/openapi
```

The output is committed at `api/openapi/admin.openapi.json`. Runtime docs are
available on the Admin server:

- Scalar UI: `/api-docs`
- OpenAPI JSON: `/api-docs/openapi.json`

Generate Dashboard types:

```bash
cd dashboard
pnpm api:generate
```

The generated `src/api/generated/schema.ts` must not be edited by hand.
`src/api/admin-client.ts` wraps `openapi-fetch` and shares bearer-token refresh
and `APIError` behavior with the legacy client. Existing hooks can therefore be
migrated one endpoint at a time.

## Migration sequence

For each remaining management route:

1. assign a permission from the central catalog, adding a new stable permission
   only when the existing catalog cannot express the action;
2. define request/response DTOs that preserve the current v1 wire shape;
3. add the typed operation and its explicit access policy;
4. add authorization matrix, envelope, and resource-containment tests;
5. remove only the matching legacy Chi method/path registration;
6. regenerate OpenAPI and TypeScript, then migrate the corresponding Dashboard
   hook to `adminApi`;
7. verify that no Proxy/LLM path entered the management document.

Recommended order is read-only routes first, then simple writes, then
resource-owned writes (keys, policies, orders, subscriptions), and finally
webhooks or other non-JWT management routes with their own explicit security
scheme.

## Verification

The Go test suite compares the committed OpenAPI file with a freshly generated
document. The Dashboard check compares generated TypeScript with the committed
schema.

```bash
go test ./...
cd dashboard
pnpm api:check
pnpm build
```

Any change to a Go DTO, operation, permission, security declaration, or
response schema must update both generated artifacts in the same change.
