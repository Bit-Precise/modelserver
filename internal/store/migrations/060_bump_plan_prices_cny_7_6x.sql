-- 060_bump_plan_prices_cny_7_6x.sql
--
-- Across-the-board 7/6× bump on every priced plan's CNY price. The Free tier
-- (slug='free', price_cny_fen=0) is left alone — multiplying by 7/6 is a
-- no-op there anyway, but excluding it makes the intent explicit and
-- protects against accidentally activating a non-zero "free" tier in the
-- future. Custom operator-created plans are included by design (same
-- WHERE slug <> 'free' pattern as 044), so they stay in step with the
-- built-in tiers.
--
-- Only CNY is bumped. price_usd_cents (Stripe channel) is intentionally
-- left unchanged: USD tiers price in whole dollars, and 7/6 would produce
-- fractional-dollar amounts. Operators who want the USD side to move must
-- ship a separate migration.
--
-- extra_usage.credit_price_cny_fen (config.yml runtime setting) is also
-- unaffected — it lives outside the database. If it should move with
-- plans, operators update config at deploy time.
--
-- Pricing is stored as an integer number of fen (1/100 CNY). Multiplying
-- by 7/6 produces fractions (e.g. 11999 → 13998.833…); we ROUND() to the
-- nearest fen, matching the convention established by 044.
--
-- Already-issued orders snapshot unit_price/amount at checkout time (see
-- orders table in 001_init.sql), so this migration only affects future
-- purchases. Active subscriptions stay valid at their original purchase
-- price.
--
-- The schema_migrations table guarantees this migration runs exactly once
-- per database, so the bump is idempotent across redeploys.

UPDATE plans
SET price_cny_fen = ROUND(price_cny_fen * 7.0 / 6.0),
    updated_at    = NOW()
WHERE slug <> 'free';
