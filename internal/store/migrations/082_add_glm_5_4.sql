-- 082_add_glm_5_4.sql
--
-- Register GLM-5.4 (Zhipu / Z.AI) in the model catalog and seed its
-- subscription rate into every plan and rate-limit policy. GLM-5.4 is the
-- multimodal successor to GLM-5.3 and keeps the same 1M-token context and
-- flagship pricing convention until a separate public pay-as-you-go price
-- is published.
--
-- Catalog rate = official GLM flagship API price (USD) / 7.5:
--   input=$1.40, cache_read=$0.26, output=$4.40
--   catalog: input=0.187, cache_read=0.035, output=0.587
--
-- Plan / policy rate = catalog * 0.1, matching glm-5.2 and glm-5.3:
--   input=0.0187, cache_read=0.0035, output=0.0587
--
-- Cached-input storage is not a separate billable event in CreditRate, so
-- cache_creation_rate remains zero. Rates are only filled when the model key
-- is absent, preserving operator-defined overrides on reruns.
--
-- Routes and upstreams remain operator-managed. GLM-5.4 can be served by the
-- Z.AI/BigModel OpenAI or Anthropic-compatible upstreams.

-- 1) Catalog row. Refresh model-owned metadata and pricing on conflict while
-- preserving aliases, which may be operator-managed.
INSERT INTO models (
    name,
    display_name,
    description,
    aliases,
    default_credit_rate,
    status,
    publisher,
    metadata
)
VALUES (
    'glm-5.4',
    'GLM-5.4',
    'Zhipu GLM-5.4 — multimodal flagship model for coding, agents, and complex project delivery with a 1M-token context window.',
    '{}',
    '{"input_rate":0.187,"output_rate":0.587,"cache_creation_rate":0,"cache_read_rate":0.035}'::jsonb,
    'active',
    'zhipu',
    '{"context_window":1000000,"capabilities":["vision","tools"],"category":"chat"}'::jsonb
)
ON CONFLICT (name) DO UPDATE SET
    display_name        = EXCLUDED.display_name,
    description         = EXCLUDED.description,
    publisher           = EXCLUDED.publisher,
    default_credit_rate = EXCLUDED.default_credit_rate,
    metadata            = EXCLUDED.metadata,
    status              = EXCLUDED.status,
    updated_at          = NOW();

-- 2) Seed every plan without replacing an operator-defined rate.
UPDATE plans
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{glm-5.4}',
        '{"input_rate":0.0187,"output_rate":0.0587,"cache_creation_rate":0,"cache_read_rate":0.0035}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'glm-5.4');

-- 3) Keep rate-limit policy defaults in lockstep with plan defaults.
UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{glm-5.4}',
        '{"input_rate":0.0187,"output_rate":0.0587,"cache_creation_rate":0,"cache_read_rate":0.0035}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'glm-5.4');
