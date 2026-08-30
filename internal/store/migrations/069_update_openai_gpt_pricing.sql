-- 069_update_openai_gpt_pricing.sql
--
-- Refresh the GPT rates that changed on OpenAI's pricing page after the
-- original catalog seeds. The catalog stores USD API prices divided by 7.5
-- (the project's credit-unit convention); plans and rate-limit policies use
-- the same 0.4x subscription multiplier established by migration 067.
--
-- Source (fetched 2026-08-30):
--   https://developers.openai.com/api/docs/pricing
--   https://developers.openai.com/api/docs/models/gpt-5.4-mini
--   https://developers.openai.com/api/docs/models/gpt-5.4-nano
--   https://developers.openai.com/api/docs/models/gpt-5.6-sol
--
-- Current standard API prices per 1M tokens (short context):
--   gpt-5.6-sol    input=$4.00, cached=$0.40, cache_write=$5.00, output=$20.00
--   gpt-5.6-terra  input=$2.00, cached=$0.20, cache_write=$2.50, output=$12.00
--   gpt-5.6-luna   input=$0.20, cached=$0.02, cache_write=$0.25, output=$1.20
--   gpt-5.4-mini   input=$0.75, cached=$0.075, output=$4.50
--   gpt-5.4-nano   input=$0.20, cached=$0.02, output=$1.25
-- GPT-5.6 Sol's $4/$20 rate is promotional through at least 2026-11-21;
-- revisit this catalog entry when OpenAI's promotion expires.
--
-- GPT-5.6 keeps the published >272K long-context multipliers (input/cache
-- x2, output x1.5). GPT-5.4 Mini/Nano have no long-context pricing tier, so
-- this migration also removes the stale long_context blocks seeded by 045.
-- Existing usage rows are snapshots and are intentionally not rewritten.

-- 1) Catalog: official API rates (USD per 1M tokens / 7.5).
WITH official_rates(name, default_credit_rate) AS (
    VALUES
        (
            'gpt-5.6-sol',
            '{"input_rate":0.533,"output_rate":2.667,"cache_creation_rate":0.667,"cache_read_rate":0.053,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb
        ),
        (
            'gpt-5.6-terra',
            '{"input_rate":0.267,"output_rate":1.6,"cache_creation_rate":0.333,"cache_read_rate":0.027,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb
        ),
        (
            'gpt-5.6-luna',
            '{"input_rate":0.027,"output_rate":0.16,"cache_creation_rate":0.033,"cache_read_rate":0.003,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb
        ),
        (
            'gpt-5.4-mini',
            '{"input_rate":0.1,"output_rate":0.6,"cache_creation_rate":0,"cache_read_rate":0.01}'::jsonb
        ),
        (
            'gpt-5.4-nano',
            '{"input_rate":0.027,"output_rate":0.167,"cache_creation_rate":0,"cache_read_rate":0.003}'::jsonb
        )
)
UPDATE models AS m
SET default_credit_rate = r.default_credit_rate,
    updated_at = NOW()
FROM official_rates AS r
WHERE m.name = r.name;

-- 2) Subscription rates in every plan. Replacing the full object also drops
-- the obsolete Mini/Nano long_context block while preserving all other model
-- entries and any per-client overlay column.
WITH subscription_rates(model_name, rate) AS (
    VALUES
        (
            'gpt-5.6-sol',
            '{"input_rate":0.2132,"output_rate":1.0668,"cache_creation_rate":0.2668,"cache_read_rate":0.0212,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb
        ),
        (
            'gpt-5.6-terra',
            '{"input_rate":0.1068,"output_rate":0.64,"cache_creation_rate":0.1332,"cache_read_rate":0.0108,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb
        ),
        (
            'gpt-5.6-luna',
            '{"input_rate":0.0108,"output_rate":0.064,"cache_creation_rate":0.0132,"cache_read_rate":0.0012,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb
        ),
        (
            'gpt-5.4-mini',
            '{"input_rate":0.04,"output_rate":0.24,"cache_creation_rate":0,"cache_read_rate":0.004}'::jsonb
        ),
        (
            'gpt-5.4-nano',
            '{"input_rate":0.0108,"output_rate":0.0668,"cache_creation_rate":0,"cache_read_rate":0.0012}'::jsonb
        )
)
UPDATE plans AS p
SET model_credit_rates = COALESCE(p.model_credit_rates, '{}'::jsonb) ||
        (SELECT jsonb_object_agg(model_name, rate) FROM subscription_rates),
    updated_at = NOW();

-- 3) Keep rate-limit policy defaults in lockstep with plan defaults.
WITH subscription_rates(model_name, rate) AS (
    VALUES
        (
            'gpt-5.6-sol',
            '{"input_rate":0.2132,"output_rate":1.0668,"cache_creation_rate":0.2668,"cache_read_rate":0.0212,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb
        ),
        (
            'gpt-5.6-terra',
            '{"input_rate":0.1068,"output_rate":0.64,"cache_creation_rate":0.1332,"cache_read_rate":0.0108,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb
        ),
        (
            'gpt-5.6-luna',
            '{"input_rate":0.0108,"output_rate":0.064,"cache_creation_rate":0.0132,"cache_read_rate":0.0012,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb
        ),
        (
            'gpt-5.4-mini',
            '{"input_rate":0.04,"output_rate":0.24,"cache_creation_rate":0,"cache_read_rate":0.004}'::jsonb
        ),
        (
            'gpt-5.4-nano',
            '{"input_rate":0.0108,"output_rate":0.0668,"cache_creation_rate":0,"cache_read_rate":0.0012}'::jsonb
        )
)
UPDATE rate_limit_policies AS p
SET model_credit_rates = COALESCE(p.model_credit_rates, '{}'::jsonb) ||
        (SELECT jsonb_object_agg(model_name, rate) FROM subscription_rates),
    updated_at = NOW();
