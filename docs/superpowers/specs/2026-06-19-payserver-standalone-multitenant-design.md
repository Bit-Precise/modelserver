# Payserver Standalone Multi-Tenant Design

## Overview

Decouple payserver from "modelserver is its only upstream" by turning it
into a true multi-tenant payment gateway. Any upstream product gets its own
`tenant_id + secret`, calls payserver with `Authorization: Bearer
<tenant_id>:<secret>`, and receives payment-success callbacks at a tenant-
specific URL signed with a tenant-specific HMAC secret. An admin-only
React UI handles tenant CRUD and payment inspection, authenticated via
OIDC against the company IdP.

Scope deliberately stops short of per-tenant Stripe / wechat / alipay
credentials — provider config stays global. A "default" tenant is created
during migration so the existing modelserver deployment keeps working with
a one-time env var update.

## Confirmed Decisions

| Topic | Decision |
|---|---|
| Tenant identity | `tenant_id UUID + secret`. Bearer header `<tenant_id>:<secret>` |
| Auth storage | `secret_hash` (bcrypt) in tenants table; cleartext shown only at create/rotate response |
| Callback location | `callback_url + callback_secret` columns on tenants table — payserver derives from tenant on payment success |
| Migration path | Migration 002 creates `default` tenant from legacy `cfg.APIKey + cfg.Callback.*`; backfills `payments.tenant_id` |
| Provider credentials | Global config, all tenants share (Stripe account / wechat MCH / alipay app are operator-level, not tenant-level) |
| Admin UI scope | Two pages: Tenants CRUD + Payments inspector. Admin-only — no tenant self-service |
| Admin frontend stack | React + TypeScript + Vite + Tailwind + shadcn (matches existing modelserver dashboard) |
| Admin auth | OIDC against company IdP (issuer/clientID/secret/redirect URL configured in payserver) |
| Frontend hosting | Built `dist/` embedded into payserver Go binary via `//go:embed`; served at `/admin/` |
| Term | "Payment" (matches Stripe / industry; existing `payments` table name unchanged) |

## §1 — File Changes (high level)

```
services/payserver/
├── internal/
│   ├── store/
│   │   ├── migrations/
│   │   │   └── 002_tenants.sql           # NEW: tenants table + default tenant + payments.tenant_id FK
│   │   ├── tenants.go                    # NEW: CRUD on Store
│   │   ├── payments.go                   # MODIFY: SELECT/INSERT carry tenant_id
│   │   └── migrations_002_test.go        # NEW
│   ├── tenant/
│   │   ├── tenant.go                     # NEW: types + GenerateSecret/HashSecret/VerifySecret
│   │   └── tenant_test.go                # NEW
│   ├── server/
│   │   ├── auth.go                       # NEW: tenantAuthMiddleware
│   │   ├── auth_test.go                  # NEW
│   │   ├── handler.go                    # MODIFY: handleCreatePayment reads tenant from ctx, writes tenant_id
│   │   ├── admin_handler.go              # NEW: CRUD + rotate-secret + payments list
│   │   ├── admin_handler_test.go         # NEW
│   │   ├── oidc.go                       # NEW: code grant + session cookie middleware
│   │   ├── oidc_test.go                  # NEW
│   │   └── routes.go                     # MODIFY: mount /admin/* with OIDC, /admin/static for embed
│   ├── notify/
│   │   ├── callback.go                   # MODIFY: Send(ctx, target, payload) — target is per-call
│   │   ├── wechat.go alipay.go stripe.go # MODIFY: each derives CallbackTarget from payment.tenant_id
│   │   └── *_test.go                     # MODIFY: tenant fixture
│   ├── compensate/
│   │   ├── compensate.go                 # MODIFY: per-row tenant lookup; inactive tenant → MarkFailed
│   │   └── compensate_test.go            # MODIFY
│   └── config/
│       └── config.go                     # MODIFY: deprecate cfg.APIKey/cfg.Callback.*; add OIDCConfig
├── cmd/payserver/
│   └── main.go                           # MODIFY: OIDC wire-up; migration bootstrap session settings; embed admin dist
├── admin/                                # NEW: React+Vite+TS admin frontend
│   ├── package.json
│   ├── pnpm-lock.yaml
│   ├── vite.config.ts
│   ├── tsconfig.json
│   ├── index.html
│   └── src/
│       ├── main.tsx, App.tsx
│       ├── api/{client,tenants,payments}.ts
│       ├── pages/{TenantsPage,TenantDetailPage,PaymentsPage}.tsx
│       └── components/{AppShell,SecretRevealOnce,ui/...}.tsx
├── admin_dist/                           # GENERATED at build time, embedded
└── go.mod / go.sum                       # +go-oidc, +golang.org/x/oauth2, +golang.org/x/crypto/bcrypt
```

## §2 — Tenant Model + Auth

### tenants table

```sql
CREATE TABLE IF NOT EXISTS tenants (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    secret_hash     TEXT NOT NULL,
    callback_url    TEXT NOT NULL DEFAULT '',
    callback_secret TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_name ON tenants(name);
```

### payments table changes

```sql
ALTER TABLE payments ADD COLUMN IF NOT EXISTS tenant_id UUID;
-- Backfill (see §3 migration 002):
UPDATE payments SET tenant_id = (SELECT id FROM tenants WHERE name = 'default')
WHERE tenant_id IS NULL;
ALTER TABLE payments ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE payments
    ADD CONSTRAINT fk_payments_tenant
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS idx_payments_tenant ON payments(tenant_id);
-- idx_payments_order_id UNIQUE is preserved as-is. Cross-tenant order_id
-- collision is prevented globally — operators are expected to use UUIDs.
```

### Tenant type + crypto

```go
// services/payserver/internal/tenant/tenant.go
package tenant

import (
    "crypto/rand"
    "encoding/base64"
    "time"
    "golang.org/x/crypto/bcrypt"
)

type Tenant struct {
    ID             string    `json:"id"`
    Name           string    `json:"name"`
    SecretHash     string    `json:"-"`              // never serialize
    CallbackURL    string    `json:"callback_url"`
    CallbackSecret string    `json:"-"`              // never serialize
    Description    string    `json:"description"`
    IsActive       bool      `json:"is_active"`
    CreatedAt      time.Time `json:"created_at"`
    UpdatedAt      time.Time `json:"updated_at"`
}

func GenerateSecret() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil { return "", err }
    return base64.RawURLEncoding.EncodeToString(b), nil
}

func HashSecret(secret string) (string, error) {
    h, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
    return string(h), err
}

func VerifySecret(hash, secret string) bool {
    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)) == nil
}
```

### Auth middleware

```go
// services/payserver/internal/server/auth.go
type ctxKey int
const ctxKeyTenant ctxKey = iota

func TenantFromContext(ctx context.Context) *tenant.Tenant {
    return ctx.Value(ctxKeyTenant).(*tenant.Tenant)
}

const dummyBcryptHash = "$2a$10$dummyhashthatwillneverpass......................"

func tenantAuthMiddleware(st *store.Store, logger *slog.Logger) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            auth := r.Header.Get("Authorization")
            if !strings.HasPrefix(auth, "Bearer ") {
                writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing bearer token"})
                return
            }
            id, secret, ok := strings.Cut(auth[7:], ":")
            if !ok {
                writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "malformed token; expected <tenant_id>:<secret>"})
                return
            }
            t, err := st.GetTenantByID(id)
            if err != nil || t == nil || !t.IsActive {
                _ = tenant.VerifySecret(dummyBcryptHash, secret) // mask timing
                writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
                return
            }
            if !tenant.VerifySecret(t.SecretHash, secret) {
                writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
                return
            }
            ctx := context.WithValue(r.Context(), ctxKeyTenant, t)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

Notes:
- bcrypt compare is constant-time; timing-mask call on the tenant-not-found
  branch is best-effort (latency parity, not provable equality).
- `is_active=false` takes effect immediately; no caching of tenant lookups.
- Header form `<uuid>:<secret>` survives standard HTTP clients without
  custom encoding.

## §3 — Migration 002 + Default-Tenant Bootstrap

### `002_tenants.sql`

```sql
BEGIN;

-- 1) tenants table
CREATE TABLE IF NOT EXISTS tenants (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    secret_hash     TEXT NOT NULL,
    callback_url    TEXT NOT NULL DEFAULT '',
    callback_secret TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_name ON tenants(name);

-- 2) Default tenant from legacy config values (session settings, see main.go)
INSERT INTO tenants (name, secret_hash, callback_url, callback_secret, description)
VALUES (
    'default',
    current_setting('payserver.default_tenant_secret_hash'),
    current_setting('payserver.default_callback_url'),
    current_setting('payserver.default_callback_secret'),
    'Auto-created during migration 002. Maps to the legacy cfg.APIKey / cfg.Callback.* fields. Rename via admin API once multi-tenancy is in use.'
)
ON CONFLICT (name) DO NOTHING;

-- 3) payments.tenant_id with FK
ALTER TABLE payments ADD COLUMN IF NOT EXISTS tenant_id UUID;
UPDATE payments SET tenant_id = (SELECT id FROM tenants WHERE name = 'default')
WHERE tenant_id IS NULL;
ALTER TABLE payments ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE payments
    ADD CONSTRAINT fk_payments_tenant
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS idx_payments_tenant ON payments(tenant_id);

COMMIT;
```

### main.go bootstrap

```go
// cmd/payserver/main.go (before store.New)
if !st_migrations.AlreadyApplied(cfg.DB.URL, "002_tenants.sql") {
    if cfg.DefaultTenantSecret == "" {
        log.Fatal("PAYSERVER_DEFAULT_TENANT_SECRET is required on the first deploy of the multi-tenant payserver (migration 002 needs it)")
    }
    hash, err := tenant.HashSecret(cfg.DefaultTenantSecret)
    if err != nil { log.Fatalf("hash default secret: %v", err) }
    store.SetMigrationBootstrap(store.MigrationBootstrap{
        DefaultTenantSecretHash: hash,
        DefaultCallbackURL:      cfg.Callback.ModelserverURL,
        DefaultCallbackSecret:   cfg.Callback.WebhookSecret,
    })
}
st, err := store.New(cfg.DB.URL, logger)
```

`store.New` reads `MigrationBootstrap` (in-memory, set before this call)
and, when about to apply 002, executes `SET LOCAL
payserver.default_tenant_secret_hash = '...'` (and two more) within the
migration transaction so `current_setting('...')` in the SQL works.

### Operator runbook

```bash
# Step 1: generate the default tenant's secret
openssl rand -base64 32   # e.g. "Xc2BkP9...=="

# Step 2: set on payserver, restart
export PAYSERVER_DEFAULT_TENANT_SECRET="Xc2BkP9...=="
# migration 002 runs once, prints: "default tenant id=<uuid>" to logs

# Step 3: take that <uuid> and update modelserver
# old: MODELSERVER_BILLING_PAYMENT_API_KEY=<old-payserver-api-key>
# new: MODELSERVER_BILLING_PAYMENT_API_KEY=<uuid>:Xc2BkP9...==

# Step 4: restart modelserver. The two services are coupled during the
# rolling window — see §7 Deployment Order.
```

### Legacy field handling

| Legacy cfg field | Disposition |
|---|---|
| `cfg.APIKey` | Deleted. `<tenant_id>:<secret>` replaces it |
| `cfg.Callback.ModelserverURL` | Read once by migration 002 → stored on default tenant. Ignored on subsequent boots (warn if still set) |
| `cfg.Callback.WebhookSecret` | Same |
| `cfg.Callback.Timeout` | Retained as global HTTP timeout |

## §4 — Per-Tenant Callback (handler + notify + compensate)

### Inbound: `POST /payments` drops `notify_url`

```go
type paymentAPIRequest struct {
    OrderID       string            `json:"order_id"`
    ProductName   string            `json:"product_name"`
    Channel       string            `json:"channel"`
    Currency      string            `json:"currency"`
    Amount        int64             `json:"amount"`
    ReturnURL     string            `json:"return_url"`
    CustomerEmail string            `json:"customer_email,omitempty"`
    Metadata      map[string]string `json:"metadata,omitempty"`
    // NotifyURL REMOVED — derived from authenticated tenant
}
```

`handleCreatePayment` writes `payment.TenantID = TenantFromContext(...).ID`.

### `CallbackClient` refactor

```go
// services/payserver/internal/notify/callback.go
type CallbackTarget struct {
    URL    string
    Secret string
}

type CallbackClient struct {
    httpClient *http.Client
}

func NewCallbackClient(timeout time.Duration) *CallbackClient {
    return &CallbackClient{httpClient: &http.Client{Timeout: timeout}}
}

// Send POSTs the payload to target.URL HMAC-signed with target.Secret.
// Empty target.URL is a no-op success (interpret as "this tenant does not
// want callbacks" — useful for read-only or test tenants).
func (c *CallbackClient) Send(ctx context.Context, target CallbackTarget, payload DeliveryPayload) error {
    if target.URL == "" { return nil }
    body, _ := json.Marshal(payload)
    mac := hmac.New(sha256.New, []byte(target.Secret))
    mac.Write(body)
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(body))
    if err != nil { return fmt.Errorf("create request: %w", err) }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-Webhook-Signature", hex.EncodeToString(mac.Sum(nil)))
    resp, err := c.httpClient.Do(req)
    if err != nil { return fmt.Errorf("send: %w", err) }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return fmt.Errorf("upstream returned %d", resp.StatusCode)
    }
    return nil
}
```

### Notify handler pattern (stripe/wechat/alipay)

```go
// after verify + GetPaymentByOrderID + MarkPaymentPaid (CAS) + provider ack:
t, err := h.store.GetTenantByID(payment.TenantID)
if err != nil || t == nil || !t.IsActive {
    h.logger.Warn("notify: tenant missing or inactive, skipping callback",
        "channel", "stripe", "order_id", orderID, "tenant_id", payment.TenantID)
    return // payment is paid, callback_status stays pending; compensate worker will also bail
}
target := notify.CallbackTarget{URL: t.CallbackURL, Secret: t.CallbackSecret}
if err := h.callback.Send(cbCtx, target, payload); err != nil {
    h.store.IncrCallbackRetries(orderID)
    return
}
h.store.MarkCallbackSuccess(orderID)
```

### Compensate worker

```go
// services/payserver/internal/compensate/compensate.go
for _, p := range rows {
    t, err := w.store.GetTenantByID(p.TenantID)
    if err != nil || t == nil || !t.IsActive {
        w.logger.Warn("compensate: tenant gone or inactive, marking failed",
            "payment_id", p.ID, "tenant_id", p.TenantID)
        w.store.MarkCallbackFailed(p.OrderID)
        continue
    }
    target := notify.CallbackTarget{URL: t.CallbackURL, Secret: t.CallbackSecret}
    if err := w.callback.Send(ctx, target, payload); err != nil {
        w.store.IncrCallbackRetries(p.OrderID)
        continue
    }
    w.store.MarkCallbackSuccess(p.OrderID)
}
```

### Notify cannot recover tenant from provider event alone

Stripe webhook payload doesn't carry tenant info. We look the payment up by
`order_id` (still globally UNIQUE), read `payment.tenant_id`, then resolve
the tenant. Operator convention: tenants use UUID order IDs so cross-tenant
collision is impossible in practice.

## §5 — Admin API + OIDC

### Routes

```
/healthz                              public
/payments                             tenant bearer middleware
/notify/{wechat,alipay,stripe}        provider native sig verification
/admin/login                          OIDC code-grant entrypoint (302)
/admin/callback                       OIDC redirect target → set cookie, 302 /admin/tenants
/admin/logout                         clear cookie → 302 /admin/login
/admin/whoami                         OIDC session cookie middleware
/admin/tenants                        GET list, POST create
/admin/tenants/{id}                   GET, PATCH, DELETE
/admin/tenants/{id}/rotate-secret     POST
/admin/payments                       GET list (?tenant_id, ?status, ?channel, page/per_page)
/admin/payments/{id}                  GET detail (includes raw_notify)
/admin/                               SPA index.html for any unmatched /admin/* path
/admin/static/*                       embedded JS/CSS/asset bundle
```

### OIDC config

```go
type OIDCConfig struct {
    IssuerURL     string   `yaml:"issuer_url"`
    ClientID      string   `yaml:"client_id"`
    ClientSecret  string   `yaml:"client_secret"`
    RedirectURL   string   `yaml:"redirect_url"`
    Scopes        []string `yaml:"scopes"`         // default ["openid","profile","email"]
    AllowedEmails []string `yaml:"allowed_emails"` // empty = allow any OIDC-validated user
    SessionSecret string   `yaml:"session_secret"` // 32+ random bytes for cookie HMAC
}
```

Env: `PAYSERVER_OIDC_ISSUER_URL`, `PAYSERVER_OIDC_CLIENT_ID`,
`PAYSERVER_OIDC_CLIENT_SECRET`, `PAYSERVER_OIDC_REDIRECT_URL`,
`PAYSERVER_OIDC_SESSION_SECRET`, `PAYSERVER_OIDC_ALLOWED_EMAILS` (comma-
separated).

### Session

Cookie name `payserver_admin_session`, HttpOnly + Secure + SameSite=Lax.
Body: HMAC-signed `{email, name, exp}` (JSON, base64). 24h expiry. No
refresh — re-login required after expiry. SessionSecret signs/verifies.

### Tenant CRUD handlers

```go
// POST /admin/tenants
// body: { name, callback_url, callback_secret, description }
// response 201: { tenant: {...minus secret_hash...}, secret: "<cleartext-once>" }
// 409 on duplicate name
func handleCreateTenant(st *store.Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var body struct {
            Name           string `json:"name"`
            CallbackURL    string `json:"callback_url"`
            CallbackSecret string `json:"callback_secret"`
            Description    string `json:"description"`
        }
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
            writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"}); return
        }
        if body.Name == "" {
            writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"}); return
        }
        if body.CallbackURL != "" {
            if err := validateReturnURL(body.CallbackURL); err != nil {
                writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()}); return
            }
        }
        secret, err := tenant.GenerateSecret()
        if err != nil { writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"}); return }
        hash, _ := tenant.HashSecret(secret)
        t := &tenant.Tenant{
            Name: body.Name, SecretHash: hash,
            CallbackURL: body.CallbackURL, CallbackSecret: body.CallbackSecret,
            Description: body.Description, IsActive: true,
        }
        if err := st.CreateTenant(t); err != nil {
            writeJSON(w, http.StatusConflict, map[string]string{"error": "name already exists"}); return
        }
        writeJSON(w, http.StatusCreated, map[string]any{"tenant": t, "secret": secret})
    }
}
```

Other handlers follow the same shape:
- `GET /admin/tenants` — list (paginated, sorted by created_at DESC)
- `GET /admin/tenants/{id}` — single
- `PATCH /admin/tenants/{id}` — allows `callback_url`, `callback_secret`,
  `description`, `is_active` only. `name` is immutable. `secret_hash` is
  immutable through PATCH (use rotate-secret).
- `POST /admin/tenants/{id}/rotate-secret` — generates fresh secret +
  hash, UPDATE, responds with `{secret: "<new-cleartext>"}`. Old secret
  fails immediately on the next request.
- `DELETE /admin/tenants/{id}` — FK is RESTRICT; on violation respond 409
  `{"error": "tenant has N payments; deactivate via PATCH is_active=false
  instead"}`.
- `validateReturnURL` (already in `services/payserver/internal/server/handler.go`
  from PR #44 — http/https scheme, no userinfo, ≤2048 chars) is reused to
  validate `callback_url`.

### Payments list

```
GET /admin/payments
  ?tenant_id=<uuid>     optional
  ?status=paid          optional (paid|pending|failed)
  ?channel=stripe       optional
  ?page=1 ?per_page=50  default 1/50, max 200
→ 200 { items: [...full payment rows including raw_notify...],
        meta: { total, page, per_page } }

GET /admin/payments/{id}
→ 200 { payment: {...} }
```

Ordered by `created_at DESC`. raw_notify is included verbatim for ops
debugging (Stripe webhook payloads etc.).

## §6 — Admin Frontend

### Workspace

```
services/payserver/admin/
├── package.json
├── pnpm-lock.yaml
├── vite.config.ts            # base "/admin/", dev port 5174
├── tsconfig.json
├── index.html
└── src/
    ├── main.tsx, App.tsx
    ├── api/
    │   ├── client.ts         # fetch wrapper. 401 → window.location = "/admin/login"
    │   ├── tenants.ts        # React Query hooks
    │   └── payments.ts
    ├── pages/
    │   ├── TenantsPage.tsx
    │   ├── TenantDetailPage.tsx
    │   └── PaymentsPage.tsx
    ├── components/
    │   ├── AppShell.tsx      # nav: Tenants | Payments; top-right user email + Logout
    │   ├── SecretRevealOnce.tsx
    │   └── ui/...            # shadcn copy-in
    └── lib/utils.ts
```

Standalone — no shared code with modelserver dashboard. Same stack, same
shadcn primitives, no cross-repo import.

### Routing

```tsx
<BrowserRouter basename="/admin">
  <Routes>
    <Route element={<AppShell />}>
      <Route index element={<Navigate to="/tenants" replace />} />
      <Route path="tenants" element={<TenantsPage />} />
      <Route path="tenants/:id" element={<TenantDetailPage />} />
      <Route path="payments" element={<PaymentsPage />} />
    </Route>
  </Routes>
</BrowserRouter>
```

App bootstrap calls `GET /admin/whoami` — on 200 mount AppShell, on 401
redirect to `/admin/login` (server handles OIDC).

### Pages

**TenantsPage**
- Header: `+ New Tenant` button.
- Table columns: Name · Callback URL · Status (Active/Inactive badge) ·
  Created · Actions menu (Edit / Rotate Secret / Deactivate / Delete).
- New Tenant Dialog: form (name, callback_url, callback_secret,
  description) → POST → on 201 show `SecretRevealOnce` with the returned
  secret.
- Rotate Secret: confirmation dialog → POST → `SecretRevealOnce`.
- Delete: try DELETE; on 409 show inline message with "Deactivate"
  shortcut (PATCH `is_active=false`).

**TenantDetailPage** (`/tenants/:id`)
- Top read-only card: id, name, created_at, status.
- Editable form: callback_url, callback_secret, description, is_active.
- "Save" → PATCH.
- Link "View this tenant's payments" → `/payments?tenant_id={id}`.

**PaymentsPage**
- Filter bar: Tenant dropdown (from cached tenants list) / Status select /
  Channel select / optional date range.
- Table: Created · Order ID · Tenant (joined from cache) · Channel ·
  Amount (channel-aware: USD/CNY by currency field) · Status · Callback
  Status · Retries.
- Click row → Detail Dialog: full payment fields + `raw_notify`
  pretty-printed JSON.
- No retry button in v1.

### SecretRevealOnce

`<Dialog>` with cleartext secret in a `<pre>` block, `Copy` button + `I've
saved it` button. The acknowledgment button is `disabled` until copy is
clicked, to prevent accidental dismissal.

### Hosting

```go
// services/payserver/cmd/payserver/main.go
//go:embed admin_dist
var adminDist embed.FS

// routes.go:
adminSubFS, _ := fs.Sub(adminDist, "admin_dist")
r.Get("/admin/static/*", http.StripPrefix("/admin/", http.FileServer(http.FS(adminSubFS))).ServeHTTP)
r.Get("/admin", func(w http.ResponseWriter, r *http.Request) {
    http.ServeFileFS(w, r, adminSubFS, "index.html")
})
r.Get("/admin/*", func(w http.ResponseWriter, r *http.Request) {
    // SPA fallback for client-side routes
    http.ServeFileFS(w, r, adminSubFS, "index.html")
})
```

Build pipeline (Makefile target `make all`):
```
cd services/payserver/admin && pnpm install --frozen-lockfile && pnpm build
cp -r services/payserver/admin/dist services/payserver/admin_dist
cd services/payserver && go build ./cmd/payserver
```

Dockerfile multi-stage: node:20 builds frontend, golang:1.26 builds the
binary with the dist copied in.

## §7 — Testing & Out of Scope

### Tests

| Layer | File | Scenarios |
|---|---|---|
| Migration | `store/migrations_002_test.go` | tenants table exists; default tenant present with matching secret_hash; all payments have non-null tenant_id pointing at default; idx_payments_order_id UNIQUE preserved; FK RESTRICT blocks default deletion |
| Tenant store | `store/tenants_test.go` | CreateTenant (unique violation); GetTenantByID hit + miss; GetTenantByName; ListTenants paginated; UpdateTenant whitelist; RotateSecret; DeleteTenant blocked by payments |
| Tenant crypto | `tenant/tenant_test.go` | GenerateSecret length / non-repeating; HashSecret roundtrip; VerifySecret rejects wrong secret |
| Auth middleware | `server/auth_test.go` | missing Bearer → 401; malformed token → 401; unknown tenant_id → 401 (still runs bcrypt for timing parity); inactive tenant → 401; correct token → ctx carries tenant |
| handleCreatePayment | `server/handler_test.go` | payment row carries tenant_id; same order_id across two tenants → 409 (global UNIQUE) |
| Callback flow | `notify/{stripe,wechat,alipay}_test.go` | webhook reads payment.tenant_id → resolves tenant → CallbackClient.Send receives correct URL+secret; inactive tenant → callback skipped + warn log |
| Compensate | `compensate/compensate_test.go` | pending rows resolved per-tenant; tenant deleted/inactive → MarkCallbackFailed immediately |
| Admin API | `server/admin_handler_test.go` | POST: server-generated secret; name required; name duplicate 409; secret in response only once. Rotate: new cleartext returned, old secret fails. DELETE blocked by payments. PATCH name field ignored. validateReturnURL applied to callback_url |
| OIDC | `server/oidc_test.go` | missing/expired session → 401 (JSON) or 302 (HTML) based on Accept header; AllowedEmails enforcement |
| Frontend | `pnpm build` (no React unit harness) | type-clean build; manual smoke checklist below |

### Manual smoke checklist (for PR runbook)

1. Set OIDC env + `PAYSERVER_DEFAULT_TENANT_SECRET`; deploy + restart.
2. Logs show `migration 002 applied` and `default tenant id=<uuid>`.
3. modelserver Bearer header updated to `<uuid>:<secret>`. Place a wechat
   order → callback succeeds (regression-free).
4. OIDC login at `/admin/login` → land on `/admin/tenants` → default
   tenant visible.
5. Create tenant "test" → secret displayed once → copy.
6. `curl -H "Authorization: Bearer test-id:test-secret" -X POST .../payments`
   with a Stripe order → callback delivered to test tenant's callback_url.
7. Rotate "test" secret → old fails, new works.
8. Deactivate "test" → new orders 401; in-flight webhook arrives → log
   `notify: tenant missing or inactive, skipping callback`; no callback
   sent.
9. Delete default → 409; delete an empty fresh tenant → 204.

### Deployment Order

1. Deploy new payserver (migration 002 runs with default tenant bootstrap).
2. Capture default tenant id from logs.
3. Update modelserver `MODELSERVER_BILLING_PAYMENT_API_KEY=<id>:<secret>`.
4. Restart modelserver.

During the window between step 1 and step 4, modelserver requests with the
old API-key format are rejected (401). Subscription ordering is a low-
frequency operation; the window is acceptable. If unacceptable, add a
short-lived shim that accepts the old key and remaps to default tenant
(not in this spec).

### Out of Scope (Future Work)

- **Per-tenant provider credentials** (Stripe/wechat/alipay per tenant).
  Requires per-call Stripe client init; defer until a second receiving
  entity is real.
- **Tenant self-service portal**. UI is admin-only.
- **Dashboard overview page** (today's volume, channel split, retry
  backlog) — third page, YAGNI for v1.
- **"Retry callback now" button** on PaymentsPage. compensate worker
  auto-retry is sufficient; manual retry is escape hatch only.
- **Audit log** (who changed which tenant's callback). OIDC + git logs
  suffice as cross-evidence at this scale.
- **Callback-secret graceful overlap** (accept old + new during rotation
  window). Current rotation is hard cut; failed callbacks are compensated.
- **Per-tenant rate limiting**. All tenants are trusted internal products
  in v1.
- **payserver split to its own repo**. Independent project.

### Risks

- **bcrypt cost per request**: every `POST /payments` runs bcrypt
  (cost=10, ~50ms). Payment creation is low-frequency, but if multi-tenant
  call volume rises, a small in-memory verified-token cache (LRU,
  60-second TTL) is the right next step. Out of scope for v1.
- **Order-id global uniqueness convention**: documented in operator
  runbook + handler error message. If a tenant accidentally reuses an
  order_id another tenant already used, they get 409 — confusing but
  detectable.
- **Default tenant bootstrap secret leakage**: the env value lands in
  process env + (one time) in a Postgres SET LOCAL statement inside the
  migration transaction. SET LOCAL values are not persisted; PG audit
  logs may capture them. Operator should rotate the secret via admin UI
  shortly after first deploy if logging is sensitive.
- **OIDC misconfiguration locks out admin**. Mitigation: keep a CLI
  rescue path that reads `PAYSERVER_OIDC_RESCUE_TOKEN` and grants admin
  access without OIDC. Marked as Future Work — for v1, document that the
  recovery path is "SSH to host, psql, edit tenants directly".
