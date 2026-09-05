-- 071_add_gpt_6_astra.sql
--
-- Register OpenAI's GPT-6 Astra model and seed its subscription rate into
-- every plan and rate-limit policy. Routes and upstreams are deliberately not
-- created here; operators can attach the catalog name to an OpenAI-compatible
-- upstream after the migration is deployed.
--
-- OpenAI API reference price (USD per 1M tokens): input=$10.00,
-- cached-input=$1.00, cache-write=$12.50, output=$50.00. The catalog stores
-- API prices in the project's USD credit units (price / 7.5), while the
-- subscription and policy maps use the current GPT convention of 0.4x the
-- catalog rate (the same multiplier used by migration 069).
-- Source (fetched 2026-09-05): https://developers.openai.com/api/docs/pricing and
-- https://developers.openai.com/api/docs/models/gpt-6-astra
--
-- GPT-6 Astra uses the existing OpenAI long-context representation: requests
-- over 272K input tokens apply 2x to input/cache classes and 1.5x to output.
-- The cache-write rate remains 1.25x the ordinary input rate at both catalog
-- and subscription tiers.

-- 1) Catalog row. Refreshing on conflict keeps a pre-seeded row's pricing in
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
VALUES (
    'gpt-6-astra',
    'GPT-6 Astra',
    'OpenAI GPT-6 Astra — frontier model for complex reasoning and coding.',
    '{}',
    '{"input_rate":1.333,"output_rate":6.667,"cache_creation_rate":1.667,"cache_read_rate":0.133,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
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

-- 2) Seed every plan without replacing a rate an operator has already set.
UPDATE plans
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{gpt-6-astra}',
        '{"input_rate":0.5332,"output_rate":2.6668,"cache_creation_rate":0.6668,"cache_read_rate":0.0532,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6-astra');

-- 3) Keep rate-limit policy defaults in lockstep with plan defaults.
UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{gpt-6-astra}',
        '{"input_rate":0.5332,"output_rate":2.6668,"cache_creation_rate":0.6668,"cache_read_rate":0.0532,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6-astra');
