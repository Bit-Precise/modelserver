-- 068_add_glm_5_3.sql
--
-- Register glm-5.3 (Zhipu / Z.AI) in the model catalog and seed its
-- subscription rate into every plan and rate-limit policy. Pattern lifted
-- from 046_add_glm_5_2.sql / 066_add_kimi_k3.sql (one catalog row, no
-- long_context, subscription-eligible with a flat compression multiplier).
--
-- Catalog rate = official Z.AI API price (USD) / 7.5 (project-wide
-- conversion, see 001_init.sql:240). This is what computeExtraUsageCostFen
-- bills against, so it MUST equal the official rate. GLM-5.3 is priced
-- identically to GLM-5.2 on docs.z.ai/guides/overview/pricing:
--
--   glm-5.3  API: input=$1.40,  cache_read=$0.26,  output=$4.40
--            cat: input=0.187,  cache_read=0.035,  output=0.587
--
-- GLM-5.3 has a flat 1M-context price (no long_context tier), same as
-- GLM-5.2. cache_creation_rate=0 because Z.AI does not bill cache writes as
-- a separate event — a cache miss is billed as ordinary input, matching the
-- GLM/DeepSeek/OpenAI convention already in the catalog.
--
-- Plan / policy rate = catalog * 0.1 (same glm-5.2 multiplier, chosen to
-- keep subscription burn-down comparable to other chat-tier models on a
-- per-prompt basis):
--
--   glm-5.3  plan: input=0.0187, output=0.0587, cache_read=0.0035
--
-- All plans use the same numbers (no per-tier compression — same convention
-- as glm-5.2, gpt-5.5). The NOT (rates ? 'glm-5.3') guards on the UPDATEs
-- preserve any operator-set custom rate set between deploy and re-run.
--
-- Routes and upstreams are intentionally not seeded — operators wire up a
-- zhipu upstream + group + route in the admin UI after deployment. Either
-- provider works (same precedent as deepseek-v4 / glm-5.2):
--   provider="anthropic" + base_url=<z.ai anthropic-compat endpoint>
--   provider="openai"    + base_url=<z.ai openai-compat endpoint>

-- 1) Catalog row. ON CONFLICT DO UPDATE matches 066 so re-runs refresh
--    display metadata and default_credit_rate together.
INSERT INTO models (name, display_name, description, aliases, default_credit_rate, status, publisher, metadata)
VALUES
    (
        'glm-5.3',
        'GLM-5.3',
        'Zhipu GLM-5.3 — 1M-context coding-focused model.',
        '{}',
        '{"input_rate":0.187,"output_rate":0.587,"cache_creation_rate":0,"cache_read_rate":0.035}'::jsonb,
        'active',
        'zhipu',
        '{"context_window":1000000,"category":"chat"}'::jsonb
    )
ON CONFLICT (name) DO UPDATE SET
    display_name        = EXCLUDED.display_name,
    description         = EXCLUDED.description,
    publisher           = EXCLUDED.publisher,
    default_credit_rate = EXCLUDED.default_credit_rate,
    metadata            = EXCLUDED.metadata,
    status              = EXCLUDED.status,
    updated_at          = NOW();

-- 2) Seed glm-5.3 into every plan that doesn't already define it.
UPDATE plans
SET model_credit_rates = jsonb_set(
        model_credit_rates,
        '{glm-5.3}',
        '{"input_rate":0.0187,"output_rate":0.0587,"cache_creation_rate":0,"cache_read_rate":0.0035}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (model_credit_rates ? 'glm-5.3');

-- 3) Same seed against rate_limit_policies so per-policy overrides pick the
--    new model up too.
UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        model_credit_rates,
        '{glm-5.3}',
        '{"input_rate":0.0187,"output_rate":0.0587,"cache_creation_rate":0,"cache_read_rate":0.0035}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (model_credit_rates ? 'glm-5.3');
