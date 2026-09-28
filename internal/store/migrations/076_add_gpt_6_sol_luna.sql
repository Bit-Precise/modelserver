-- 076_add_gpt_6_sol_luna.sql
--
-- Register OpenAI's GPT-6 Sol and GPT-6 Luna models and seed their
-- subscription rates. Routes and upstreams are intentionally left to the
-- operator, matching the GPT-6 Astra migration.
--
-- OpenAI API reference prices per 1M tokens (standard, short context):
--   gpt-6-sol  input=$2.00, cached=$0.20, cache-write=$2.50, output=$10.00
--   gpt-6-luna input=$0.10, cached=$0.01, cache-write=$0.125, output=$0.50
--
-- The catalog stores API prices divided by 7.5. Subscription and policy
-- rates use the current GPT convention of 0.4x the rounded catalog rate.
-- Prompts over 272K input tokens apply 2x to input/cache classes and 1.5x to
-- output, as published on the model pages.
--
-- Source (fetched 2026-09-28):
--   https://developers.openai.com/api/docs/pricing
--   https://developers.openai.com/api/docs/models/gpt-6-sol
--   https://developers.openai.com/api/docs/models/gpt-6-luna

-- 1) Catalog rows. Refreshing on conflict keeps a pre-seeded row's pricing in
-- sync while leaving aliases and operator-managed routing configuration alone.
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
VALUES
(
    'gpt-6-sol',
    'GPT-6 Sol',
    'OpenAI GPT-6 Sol — balances intelligence and cost.',
    '{}',
    '{"input_rate":0.267,"output_rate":1.333,"cache_creation_rate":0.333,"cache_read_rate":0.027,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
    'active',
    'openai',
    '{"context_window":1050000,"category":"chat"}'::jsonb
),
(
    'gpt-6-luna',
    'GPT-6 Luna',
    'OpenAI GPT-6 Luna — cost-sensitive model for high-volume workloads.',
    '{}',
    '{"input_rate":0.013,"output_rate":0.067,"cache_creation_rate":0.017,"cache_read_rate":0.001,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
    'active',
    'openai',
    '{"context_window":1050000,"category":"chat"}'::jsonb
)
ON CONFLICT (name) DO UPDATE SET
    display_name        = EXCLUDED.display_name,
    description         = EXCLUDED.description,
    publisher           = EXCLUDED.publisher,
    default_credit_rate = EXCLUDED.default_credit_rate,
    metadata            = EXCLUDED.metadata,
    status              = EXCLUDED.status,
    updated_at          = NOW();

-- 2) Seed every plan without replacing an operator override.
UPDATE plans
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{gpt-6-sol}',
        '{"input_rate":0.1068,"output_rate":0.5332,"cache_creation_rate":0.1332,"cache_read_rate":0.0108,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6-sol');

UPDATE plans
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{gpt-6-luna}',
        '{"input_rate":0.0052,"output_rate":0.0268,"cache_creation_rate":0.0068,"cache_read_rate":0.0004,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6-luna');

-- 3) Keep rate-limit policy defaults in lockstep with plan defaults.
UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{gpt-6-sol}',
        '{"input_rate":0.1068,"output_rate":0.5332,"cache_creation_rate":0.1332,"cache_read_rate":0.0108,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6-sol');

UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{gpt-6-luna}',
        '{"input_rate":0.0052,"output_rate":0.0268,"cache_creation_rate":0.0068,"cache_read_rate":0.0004,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6-luna');
