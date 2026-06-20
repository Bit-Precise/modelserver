-- 002_tenants.sql
--
-- Multi-tenant migration: introduces the tenants table, creates a
-- "default" tenant from legacy config fields, and adds the tenant_id FK
-- on payments so every existing row stays accounted for under the
-- default tenant.
--
-- This SQL is wrapped by the migration runner in a tx; do NOT add
-- BEGIN/COMMIT here (nesting would close the outer tx and the
-- schema_migrations record would land outside the migration's scope).
--
-- current_setting() values are injected into this same tx by the runner
-- (see store.go's SET LOCAL block guarded by name == "002_tenants.sql").
-- Operators who forget PAYSERVER_DEFAULT_TENANT_SECRET will see a clean
-- error: 'unrecognized configuration parameter
-- "payserver.default_tenant_secret_hash"'.

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

INSERT INTO tenants (name, secret_hash, callback_url, callback_secret, description)
VALUES (
    'default',
    current_setting('payserver.default_tenant_secret_hash'),
    current_setting('payserver.default_callback_url'),
    current_setting('payserver.default_callback_secret'),
    'Auto-created during migration 002. Holds the legacy cfg.APIKey / cfg.Callback.* values. Rename via admin UI once multi-tenancy is in use.'
)
ON CONFLICT (name) DO NOTHING;

ALTER TABLE payments ADD COLUMN IF NOT EXISTS tenant_id UUID;

UPDATE payments
SET tenant_id = (SELECT id FROM tenants WHERE name = 'default')
WHERE tenant_id IS NULL;

ALTER TABLE payments ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE payments
    ADD CONSTRAINT fk_payments_tenant
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE RESTRICT;

CREATE INDEX IF NOT EXISTS idx_payments_tenant ON payments(tenant_id);
