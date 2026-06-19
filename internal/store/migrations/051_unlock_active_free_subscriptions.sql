-- 051_unlock_active_free_subscriptions.sql
--
-- Fix migration 050's overzealous backfill.
--
-- Background: 050 added `subscriptions.currency` and backfilled it from
-- "the latest paid/delivered order on the same project whose created_at
-- precedes this sub's starts_at". That UPDATE was meant to populate the
-- column for paid subscriptions that were active at deploy time, so the
-- cross-currency lock would survive across the schema change. It worked
-- correctly for paid rows.
--
-- But the same UPDATE also matched any project that had previously paid
-- in CNY, then expired back to Free before 050 ran. For those rows,
-- `ExpireAndFallbackToFree` had inserted a brand-new Free subscription
-- (`plan_name='free'`, `currency=''`), but 050's backfill found an old
-- paid order on the same project whose `created_at <= free.starts_at` and
-- copied its currency onto the Free row — silently relocking projects
-- that should be unlocked.
--
-- Symptom: dashboard renders the Stripe payment button as disabled with
-- "Locked to CNY" tooltip on projects that are currently on the Free
-- tier, blocking legitimate first-time USD purchases.
--
-- Fix scope: only `active` Free rows. Revoked / expired Free rows are
-- inert (no reader queries currency on them), so we leave them alone to
-- preserve the audit history. Paid subscriptions are unaffected — they
-- correctly carry their own currency from DeliverOrder, and 050's
-- backfill was correct for them.
--
-- Forward behavior is already correct: ExpireAndFallbackToFree (in
-- subscriptions.go) writes `currency=''` explicitly when creating a new
-- Free sub, so future expiry → Free transitions won't reintroduce this
-- state.

UPDATE subscriptions
SET currency = '', updated_at = NOW()
WHERE status = 'active'
  AND plan_name = 'free'
  AND currency <> '';
