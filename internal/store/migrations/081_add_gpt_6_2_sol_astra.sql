-- Register GPT-6.2 Sol and Astra and seed their subscription rates.
-- Upstreams and routes remain operator-managed, as in migrations 071/077.
--
-- Official OpenAI API prices per 1M tokens (standard, short context):
--   gpt-6.2-sol   input=$2.00, cached=$0.10, cache-write=$2.50, output=$10.00
--   gpt-6.2-astra input=$8.00, cached=$0.40, cache-write=$10.00, output=$40.00
-- Cache writes use the standard 30-minute retention price.
-- Catalog rates = API USD prices / 7.5, rounded to 3 decimal places.
-- Plan/policy rates = rounded catalog rates * 0.4, as in migrations 069/077.
-- Both models have a 1,050,000-token context window. Prompts over 272K input
-- tokens apply 2x to input/cache classes and 1.5x to output for the full request.
--
-- Sources (fetched 2026-11-18):
--   https://developers.openai.com/api/docs/pricing
--   https://developers.openai.com/api/docs/models/gpt-6.2-sol
--   https://developers.openai.com/api/docs/models/gpt-6.2-astra

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
VALUES
(
    'gpt-6.2-sol',
    'GPT-6.2 Sol',
    'OpenAI GPT-6.2 Sol model for everyday work, coding, and agents at scale.',
    '{}',
    '{"input_rate":0.267,"output_rate":1.333,"cache_creation_rate":0.333,"cache_read_rate":0.013,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
    'active',
    'openai',
    '{"context_window":1050000,"category":"chat"}'::jsonb
),
(
    'gpt-6.2-astra',
    'GPT-6.2 Astra',
    'OpenAI GPT-6.2 Astra model for complex reasoning, coding, and end-to-end projects.',
    '{}',
    '{"input_rate":1.067,"output_rate":5.333,"cache_creation_rate":1.333,"cache_read_rate":0.053,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
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
        '{gpt-6.2-sol}',
        '{"input_rate":0.1068,"output_rate":0.5332,"cache_creation_rate":0.1332,"cache_read_rate":0.0052,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6.2-sol');

UPDATE plans
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{gpt-6.2-astra}',
        '{"input_rate":0.4268,"output_rate":2.1332,"cache_creation_rate":0.5332,"cache_read_rate":0.0212,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6.2-astra');

-- 3) Keep rate-limit policy defaults in lockstep with plan defaults.
UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{gpt-6.2-sol}',
        '{"input_rate":0.1068,"output_rate":0.5332,"cache_creation_rate":0.1332,"cache_read_rate":0.0052,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6.2-sol');

UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{gpt-6.2-astra}',
        '{"input_rate":0.4268,"output_rate":2.1332,"cache_creation_rate":0.5332,"cache_read_rate":0.0212,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6.2-astra');
