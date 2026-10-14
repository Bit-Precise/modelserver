-- 077_add_gpt_6_1_sol.sql
--
-- Register GPT-6.1 Sol and seed its subscription rate, following GPT-6 Sol
-- in migration 076. Upstreams and routes remain operator-managed.
--
-- Official OpenAI API prices per 1M tokens (standard, short context):
--   input=$2.00, cached-input=$0.10, cache-write=$2.50, output=$10.00.
-- Cached input is 5% of input, down from GPT-6 Sol's 10%.
--
-- Catalog rates = API USD prices / 7.5, rounded to 3 decimal places.
-- Plan/policy rates = rounded catalog rates * 0.4, as in migrations 069/076.
-- Prompts over 272K input tokens apply 2x to input/cache classes and 1.5x to
-- output for the whole request. Context window: 1,050,000 tokens.
--
-- Sources (fetched 2026-10-14):
--   https://developers.openai.com/api/docs/pricing
--   https://developers.openai.com/api/docs/models/gpt-6.1-sol

-- 1) Refresh catalog pricing on conflict, preserving operator-set aliases.
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
    'gpt-6.1-sol',
    'GPT-6.1 Sol',
    'OpenAI GPT-6.1 Sol model for reasoning and coding.',
    '{}',
    '{"input_rate":0.267,"output_rate":1.333,"cache_creation_rate":0.333,"cache_read_rate":0.013,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
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
        '{gpt-6.1-sol}',
        '{"input_rate":0.1068,"output_rate":0.5332,"cache_creation_rate":0.1332,"cache_read_rate":0.0052,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6.1-sol');

-- 3) Keep rate-limit policy defaults in lockstep with plan defaults.
UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{gpt-6.1-sol}',
        '{"input_rate":0.1068,"output_rate":0.5332,"cache_creation_rate":0.1332,"cache_read_rate":0.0052,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6.1-sol');
