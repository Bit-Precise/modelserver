-- 046_add_glm_5_2.sql
--
-- Register glm-5.2 (Zhipu / Z.AI) in the model catalog and seed its
-- subscription rate into every plan and rate-limit policy. Pattern lifted
-- from 045_add_gpt_5_4_mini_nano.sql with the long_context block removed
-- and one catalog row instead of two.
--
-- Catalog rate = official Z.AI API price (USD) / 7.5 (project-wide
-- conversion, see 001_init.sql:240). This is what computeExtraUsageCostFen
-- bills against, so it MUST equal the official rate:
--
--   glm-5.2  API: input=$1.40,  cache_read=$0.26,  output=$4.40
--            cat: input=0.187,  cache_read=0.035,  output=0.587
--
-- GLM-5.2 has a flat 1M-context price (no long_context tier), confirmed
-- against the Z.AI overview docs. cache_creation_rate=0 because Z.AI does
-- not bill cache writes as a separate event — cache misses are billed as
-- ordinary input.
--
-- Plan / policy rate = catalog * 0.1 (a glm-5.2-specific multiplier,
-- chosen to keep subscription burn-down comparable to other chat-tier
-- models on a per-prompt basis):
--
--   glm-5.2  plan: input=0.0187, output=0.0587, cache_read=0.0035
--
-- All plans use the same numbers (no per-tier compression — same
-- convention as gpt-5.5, gpt-5.4-mini/-nano in plans). The NOT (rates ?
-- 'glm-5.2') guards on the UPDATEs preserve any operator-set custom rate
-- set between deploy and re-run.
--
-- Note on stale comment in 035: migration 035's header says glm-* rows
-- are "intentionally left without a default rate". After 046 runs that
-- is no longer true for glm-5.2 specifically (other glm-* still are).
-- No edit to 035 is shipped; readers should treat the 035 comment as
-- accurate at the time it was written.
--
-- Routes and upstreams are intentionally not seeded — operators wire up
-- a zhipu upstream + group + route in the admin UI after deployment.
-- Either provider works (same precedent as deepseek-v4):
--   provider="anthropic" + base_url=<z.ai anthropic-compat endpoint>
--   provider="openai"    + base_url=<z.ai openai-compat endpoint>

-- 1) Catalog row. ON CONFLICT DO NOTHING so re-runs (or a manual seed
--    prior to deploy) are no-ops, mirroring 045.
INSERT INTO models (name, display_name, description, aliases, default_credit_rate, status, publisher, metadata)
VALUES
    (
        'glm-5.2',
        'GLM-5.2',
        'Zhipu GLM-5.2 — 1M-context coding-focused model.',
        '{}',
        '{"input_rate":0.187,"output_rate":0.587,"cache_creation_rate":0,"cache_read_rate":0.035}'::jsonb,
        'active',
        'zhipu',
        '{"context_window":1000000,"category":"chat"}'::jsonb
    )
ON CONFLICT (name) DO NOTHING;

-- 2) Seed glm-5.2 into every plan that doesn't already define it.
UPDATE plans
SET model_credit_rates = jsonb_set(
        model_credit_rates,
        '{glm-5.2}',
        '{"input_rate":0.0187,"output_rate":0.0587,"cache_creation_rate":0,"cache_read_rate":0.0035}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (model_credit_rates ? 'glm-5.2');

-- 3) Same seed against rate_limit_policies so per-policy overrides pick
--    the new model up too.
UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        model_credit_rates,
        '{glm-5.2}',
        '{"input_rate":0.0187,"output_rate":0.0587,"cache_creation_rate":0,"cache_read_rate":0.0035}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (model_credit_rates ? 'glm-5.2');
