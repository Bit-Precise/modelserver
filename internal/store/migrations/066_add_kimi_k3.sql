-- 066_add_kimi_k3.sql
--
-- Register kimi-k3 (Moonshot AI) in the model catalog and seed its
-- subscription rate into every plan and rate-limit policy. Pattern lifted
-- from 046_add_glm_5_2.sql (one catalog row, no long_context, subscription-
-- eligible with a flat compression multiplier).
--
-- Kimi K3 is quoted in USD on platform.kimi.ai/docs/pricing/chat-k3, so it
-- uses the USD conversion (÷7.5, see 001_init.sql:240) — the same convention
-- as the Claude/OpenAI/GLM rows, NOT the CNY ÷54.38 convention DeepSeek uses.
-- This catalog rate is what computeExtraUsageCost bills against, so it MUST
-- equal the official rate:
--
--   kimi-k3  API: input=$3.00,  output=$15.00,  cache_hit=$0.30
--            cat: input=0.4,    output=2.0,     cache_read=0.04
--
-- Kimi K3 has a single flat price across its full 1,048,576-token window (no
-- length-tiered pricing), so there is no long_context block. cache_creation_
-- rate=0 because Moonshot does not bill cache writes as a separate event — a
-- cache miss is billed as ordinary input ($3.00), matching the GLM/DeepSeek/
-- OpenAI convention already in the catalog.
--
-- Plan / policy rate = catalog × 0.1 (matching glm-5.2, the closest analog —
-- a third-party 1M-context chat model — per the migration-time decision):
--
--   kimi-k3  plan: input=0.04, output=0.2, cache_read=0.004
--
-- All plans use the same numbers (no per-tier compression — same convention
-- as glm-5.2, gpt-5.5). The NOT (rates ? 'kimi-k3') guards on the UPDATEs
-- preserve any operator-set custom rate set between deploy and re-run.
--
-- Routes and upstreams are intentionally not seeded — operators wire up a
-- moonshot upstream + group + route in the admin UI after deployment. Either
-- provider works (same precedent as deepseek-v4 / glm-5.2):
--   provider="anthropic" + base_url=<Moonshot anthropic-compat endpoint>
--   provider="openai"    + base_url=<Moonshot openai-compat endpoint>

-- 1) Catalog row. ON CONFLICT DO UPDATE matches 042/058 so re-runs refresh
--    display metadata and default_credit_rate together.
INSERT INTO models (name, display_name, description, aliases, default_credit_rate, status, publisher, metadata)
VALUES
    (
        'kimi-k3',
        'Kimi K3',
        'Moonshot Kimi K3 — 1M-context flagship chat model.',
        '{}',
        '{"input_rate":0.4,"output_rate":2.0,"cache_creation_rate":0,"cache_read_rate":0.04}'::jsonb,
        'active',
        'moonshot',
        '{"context_window":1048576,"category":"chat"}'::jsonb
    )
ON CONFLICT (name) DO UPDATE SET
    display_name        = EXCLUDED.display_name,
    description         = EXCLUDED.description,
    publisher           = EXCLUDED.publisher,
    default_credit_rate = EXCLUDED.default_credit_rate,
    metadata            = EXCLUDED.metadata,
    status              = EXCLUDED.status,
    updated_at          = NOW();

-- 2) Seed kimi-k3 into every plan that doesn't already define it.
UPDATE plans
SET model_credit_rates = jsonb_set(
        model_credit_rates,
        '{kimi-k3}',
        '{"input_rate":0.04,"output_rate":0.2,"cache_creation_rate":0,"cache_read_rate":0.004}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (model_credit_rates ? 'kimi-k3');

-- 3) Same seed against rate_limit_policies so per-policy overrides pick the
--    new model up too.
UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        model_credit_rates,
        '{kimi-k3}',
        '{"input_rate":0.04,"output_rate":0.2,"cache_creation_rate":0,"cache_read_rate":0.004}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (model_credit_rates ? 'kimi-k3');
