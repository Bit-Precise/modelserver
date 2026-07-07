-- 061_add_max_140x_160x_180x_220x_plans.sql
--
-- Introduce four new Max tiers filling the gaps in the existing 20x /
-- 40x / 60x / 80x / 100x / 120x / 200x / 240x ladder:
--   max_140x, max_160x, max_180x, max_220x
--
-- All four use the same conventions established by prior max_Nx additions:
--   - tier_level        = N * 100        (max_140x = 14000, etc.)
--   - price_cny_fen     = N * 7000 - 1   (xxxx99 rounding, post-060 anchor)
--   - price_usd_cents   = N * 1000       (N * $10, per migration 049's anchor)
--   - 5h credits        = N * 550000     (per-unit rate from max_20x onward)
--   - 7d credits        = N * 4166665
--   - model_credit_rates        = cloned from pro at migration time
--   - client_model_credit_rates = cloned from pro at migration time
--
-- Migration 049 requires every new plan to populate BOTH price_cny_fen
-- and price_usd_cents directly, so both are set explicitly here.
--
-- Cloning rates from pro (rather than hardcoding) follows the pattern
-- established by 059. If pro's rates or client overlay are later tuned,
-- these four tiers do NOT auto-follow — operators must reapply the tuning.
--
-- These rows are born at post-060 prices. Migration 060 runs first and
-- bumps existing rows 7/6×; 061 then inserts new rows already at the
-- final anchor, so 060 has no effect on them (nor could it — they did
-- not exist when 060 ran).
--
-- Idempotent via ON CONFLICT (slug) DO NOTHING, matching the seed inserts
-- in 001_init.sql and 059. If pro is somehow absent when this runs, all
-- four SELECTs return no rows and the INSERTs are no-ops; every deployment
-- we ship seeds pro from 001_init.sql.

INSERT INTO plans (name, slug, display_name, description, tier_level,
                   price_cny_fen, price_usd_cents, period_months,
                   credit_rules, model_credit_rates, client_model_credit_rates)
SELECT 'Max 140x', 'max_140x', 'Max 140x',
       'Same usage limits as Claude Max (140x)', 14000,
       979999, 140000, 1,
       '[{"window":"5h","window_type":"sliding","max_credits":77000000,"scope":"project"},
         {"window":"7d","window_type":"sliding","max_credits":583333100,"scope":"project"}]'::jsonb,
       model_credit_rates, client_model_credit_rates
FROM plans WHERE slug = 'pro'
ON CONFLICT (slug) DO NOTHING;

INSERT INTO plans (name, slug, display_name, description, tier_level,
                   price_cny_fen, price_usd_cents, period_months,
                   credit_rules, model_credit_rates, client_model_credit_rates)
SELECT 'Max 160x', 'max_160x', 'Max 160x',
       'Same usage limits as Claude Max (160x)', 16000,
       1119999, 160000, 1,
       '[{"window":"5h","window_type":"sliding","max_credits":88000000,"scope":"project"},
         {"window":"7d","window_type":"sliding","max_credits":666666400,"scope":"project"}]'::jsonb,
       model_credit_rates, client_model_credit_rates
FROM plans WHERE slug = 'pro'
ON CONFLICT (slug) DO NOTHING;

INSERT INTO plans (name, slug, display_name, description, tier_level,
                   price_cny_fen, price_usd_cents, period_months,
                   credit_rules, model_credit_rates, client_model_credit_rates)
SELECT 'Max 180x', 'max_180x', 'Max 180x',
       'Same usage limits as Claude Max (180x)', 18000,
       1259999, 180000, 1,
       '[{"window":"5h","window_type":"sliding","max_credits":99000000,"scope":"project"},
         {"window":"7d","window_type":"sliding","max_credits":749999700,"scope":"project"}]'::jsonb,
       model_credit_rates, client_model_credit_rates
FROM plans WHERE slug = 'pro'
ON CONFLICT (slug) DO NOTHING;

INSERT INTO plans (name, slug, display_name, description, tier_level,
                   price_cny_fen, price_usd_cents, period_months,
                   credit_rules, model_credit_rates, client_model_credit_rates)
SELECT 'Max 220x', 'max_220x', 'Max 220x',
       'Same usage limits as Claude Max (220x)', 22000,
       1539999, 220000, 1,
       '[{"window":"5h","window_type":"sliding","max_credits":121000000,"scope":"project"},
         {"window":"7d","window_type":"sliding","max_credits":916666300,"scope":"project"}]'::jsonb,
       model_credit_rates, client_model_credit_rates
FROM plans WHERE slug = 'pro'
ON CONFLICT (slug) DO NOTHING;
