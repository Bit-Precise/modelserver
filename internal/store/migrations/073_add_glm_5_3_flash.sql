-- 073_add_glm_5_3_flash.sql
--
-- Register GLM-5.3-Flash (Zhipu / Z.AI) in the model catalog and seed its
-- subscription rate into every plan and rate-limit policy. Routes and
-- upstreams remain operator-managed, matching the existing GLM model
-- migrations.
--
-- Official Z.AI API price (USD per 1M tokens, fetched 2026-09-11):
--   input=$0.15, cached input=$0.03, output=$0.50
-- Source: https://docs.z.ai/guides/overview/pricing
--
-- Catalog rates use the project-wide USD conversion of API price / 7.5:
--   input=0.020, cache_read=0.004, output=0.067
-- Subscription and policy rates use the GLM convention of catalog * 0.1:
--   input=0.002, cache_read=0.0004, output=0.0067
--
-- Cached-input storage is currently free and is not a separate token class
-- in CreditRate, so cache_creation_rate remains zero. The model has flat
-- pricing across its 1M-token context window and therefore has no
-- long_context block.

-- 1) Catalog row. Refresh model-owned metadata and pricing on conflict while
--    preserving aliases, which may be operator-managed.
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
    'glm-5.3-flash',
    'GLM-5.3-Flash',
    'Zhipu GLM-5.3-Flash — native multimodal model with a 1M-token context window.',
    '{}',
    '{"input_rate":0.02,"output_rate":0.067,"cache_creation_rate":0,"cache_read_rate":0.004}'::jsonb,
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

-- 2) Seed every plan without replacing an operator-defined rate.
UPDATE plans
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{glm-5.3-flash}',
        '{"input_rate":0.002,"output_rate":0.0067,"cache_creation_rate":0,"cache_read_rate":0.0004}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'glm-5.3-flash');

-- 3) Keep rate-limit policy defaults in lockstep with plan defaults.
UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{glm-5.3-flash}',
        '{"input_rate":0.002,"output_rate":0.0067,"cache_creation_rate":0,"cache_read_rate":0.0004}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'glm-5.3-flash');
