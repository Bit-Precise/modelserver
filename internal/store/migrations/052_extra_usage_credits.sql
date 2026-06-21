-- 052_extra_usage_credits.sql
--
-- Renames fen → credits throughout the extra-usage subsystem and adds the
-- supporting audit table + refund idempotency index. The actual data
-- conversion (multiply old fen × 1_000_000 / credit_price_cny_fen) is
-- performed by Store.convertExtraUsageDataToCredits AFTER this schema
-- migration commits — it needs a deploy-time env var that can't live in
-- a pure SQL file.

BEGIN;

ALTER TABLE extra_usage_settings
    RENAME COLUMN balance_fen TO balance_credits;
ALTER TABLE extra_usage_settings
    RENAME COLUMN monthly_limit_fen TO monthly_limit_credits;

ALTER TABLE extra_usage_transactions
    RENAME COLUMN amount_fen TO amount_credits;
ALTER TABLE extra_usage_transactions
    RENAME COLUMN balance_after_fen TO balance_after_credits;

ALTER TABLE requests
    RENAME COLUMN extra_usage_cost_fen TO extra_usage_cost_credits;

ALTER TABLE orders
    RENAME COLUMN extra_usage_amount_fen TO extra_usage_amount_credits;

CREATE TABLE IF NOT EXISTS extra_usage_credit_migration_audit (
    id                   SERIAL      PRIMARY KEY,
    credit_price_cny_fen BIGINT      NOT NULL,
    applied_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Symmetrical to uniq_eut_topup_order (migration 017). Prevents a duplicate
-- Stripe refund webhook from double-reversing the same order's credits.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_eut_refund_order
    ON extra_usage_transactions (order_id)
    WHERE type = 'refund' AND order_id IS NOT NULL;

COMMIT;
