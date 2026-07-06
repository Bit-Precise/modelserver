-- 058_add_mini_nano_plans.sql
--
-- Introduces two new paid tiers, Mini and Nano, positioned between Free and
-- Pro. Both reuse Pro's per-model credit rates verbatim — they differ from
-- Pro only in credit_rules (halved / quartered) and price (halved /
-- quartered).
--
-- Both model_credit_rates and client_model_credit_rates (added by migration
-- 057 as a per-client overlay resolved ahead of model_credit_rates) are
-- copied from the live pro row at migration time (rather than hardcoded)
-- because the pro rate map has been mutated by migrations 044 (×1.2 prices —
-- unrelated) and 047, 053 (×2 GPT rates), and future tuning is easier if we
-- do not fork a second source of truth here. If pro's rates are later tuned
-- or a client overlay is added, mini/nano do NOT auto-follow — operators
-- must reapply the tuning if they want parity.
--
-- Idempotent via ON CONFLICT (slug) DO NOTHING, matching the seed inserts
-- in 001_init.sql. If pro is somehow absent when this runs, the SELECT
-- returns no rows and both INSERTs become no-ops; that is acceptable
-- because every deployment we ship seeds pro from 001_init.sql.

INSERT INTO plans (name, slug, display_name, description, tier_level,
                   price_cny_fen, price_usd_cents, period_months,
                   credit_rules, model_credit_rates, client_model_credit_rates)
SELECT 'Mini', 'mini', 'Mini', 'Half of Pro''s usage limits', 50,
       5999, 1000, 1,
       '[{"window":"5h","window_type":"sliding","max_credits":275000,"scope":"project"},
         {"window":"7d","window_type":"sliding","max_credits":2500000,"scope":"project"}]'::jsonb,
       model_credit_rates,
       client_model_credit_rates
FROM plans WHERE slug = 'pro'
ON CONFLICT (slug) DO NOTHING;

INSERT INTO plans (name, slug, display_name, description, tier_level,
                   price_cny_fen, price_usd_cents, period_months,
                   credit_rules, model_credit_rates, client_model_credit_rates)
SELECT 'Nano', 'nano', 'Nano', 'Quarter of Pro''s usage limits', 25,
       2999, 500, 1,
       '[{"window":"5h","window_type":"sliding","max_credits":137500,"scope":"project"},
         {"window":"7d","window_type":"sliding","max_credits":1250000,"scope":"project"}]'::jsonb,
       model_credit_rates,
       client_model_credit_rates
FROM plans WHERE slug = 'pro'
ON CONFLICT (slug) DO NOTHING;
