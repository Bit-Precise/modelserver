-- 062_add_gpt_5_6.sql
--
-- Register the GPT-5.6 family (sol / terra / luna) in the model catalog and
-- seed subscription credit rates into every plan and rate-limit policy.
-- Structural twin of 045_add_gpt_5_4_mini_nano.sql; the only new wrinkles
-- vs. prior gpt-5.x families are that OpenAI now bills a separate
-- cache_writes charge (input * 1.25) and publishes an explicit Short/Long
-- context split (input/cached/cache_write x2, output x1.5).
--
-- Catalog rate = official OpenAI API price / 7.5 (project-wide convention,
-- see 001_init.sql:240 and 035_seed_catalog_default_credit_rates.sql).
-- The Short-tier price is the base; long_context block reproduces the
-- Long-tier multipliers. policy.ApplyLongContextCreditRate applies
-- InputMultiplier to CacheCreation/CacheRead too, matching OpenAI's
-- Long-tier semantics across all four token classes.
--
--   gpt-5.6-sol    API: input=$5.00, cached=$0.50, cache_write=$6.25,  output=$30.00
--                  cat: input=0.667, cache_read=0.067, cache_creation=0.833, output=4.000
--   gpt-5.6-terra  API: input=$2.50, cached=$0.25, cache_write=$3.125, output=$15.00
--                  cat: input=0.333, cache_read=0.033, cache_creation=0.417, output=2.000
--   gpt-5.6-luna   API: input=$1.00, cached=$0.10, cache_write=$1.25,  output=$6.00
--                  cat: input=0.133, cache_read=0.013, cache_creation=0.167, output=0.800
--
-- Plan/policy rate = catalog * 0.2 (gpt-5.x subscription multiplier from
-- 047_gpt_5_x_plan_rebase.sql * 2x from 053_gpt_plan_prices_2x.sql).
--
-- Long-context threshold: OpenAI does not publish the token cutoff for
-- 5.6's Short/Long tiers. We reuse 272000 (project convention from
-- 032_openai_long_context_pricing.sql and 045). A follow-up migration
-- can adjust if the official value differs.
--
-- Alias: only gpt-5.6-sol takes the family-level alias 'gpt-5.6' (matches
-- OpenAI's own alias table on the docs page).
--
-- Idempotency: ON CONFLICT DO NOTHING on catalog INSERT and NOT (rates ? key)
-- guards on plan/policy UPDATEs let re-runs be no-ops and preserve any
-- operator override applied between deploy and re-run.

-- 1) Catalog rows.
INSERT INTO models (name, display_name, description, aliases, default_credit_rate, status, publisher, metadata)
VALUES
    (
        'gpt-5.6-sol',
        'GPT-5.6 Sol',
        'Frontier GPT-5.6 model for complex professional work.',
        '{gpt-5.6}',
        '{"input_rate":0.667,"output_rate":4.0,"cache_creation_rate":0.833,"cache_read_rate":0.067,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        'active',
        'openai',
        '{"context_window":1050000,"category":"chat"}'::jsonb
    ),
    (
        'gpt-5.6-terra',
        'GPT-5.6 Terra',
        'GPT-5.6 model balancing intelligence and cost.',
        '{}',
        '{"input_rate":0.333,"output_rate":2.0,"cache_creation_rate":0.417,"cache_read_rate":0.033,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        'active',
        'openai',
        '{"context_window":1050000,"category":"chat"}'::jsonb
    ),
    (
        'gpt-5.6-luna',
        'GPT-5.6 Luna',
        'GPT-5.6 model optimized for cost-sensitive workloads.',
        '{}',
        '{"input_rate":0.133,"output_rate":0.8,"cache_creation_rate":0.167,"cache_read_rate":0.013,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        'active',
        'openai',
        '{"context_window":1050000,"category":"chat"}'::jsonb
    )
ON CONFLICT (name) DO NOTHING;

-- 2) Plan seeds.
UPDATE plans
SET model_credit_rates = jsonb_set(
        model_credit_rates,
        '{gpt-5.6-sol}',
        '{"input_rate":0.1334,"output_rate":0.8,"cache_creation_rate":0.1668,"cache_read_rate":0.0134,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (model_credit_rates ? 'gpt-5.6-sol');

UPDATE plans
SET model_credit_rates = jsonb_set(
        model_credit_rates,
        '{gpt-5.6-terra}',
        '{"input_rate":0.0666,"output_rate":0.4,"cache_creation_rate":0.0834,"cache_read_rate":0.0066,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (model_credit_rates ? 'gpt-5.6-terra');

UPDATE plans
SET model_credit_rates = jsonb_set(
        model_credit_rates,
        '{gpt-5.6-luna}',
        '{"input_rate":0.0266,"output_rate":0.16,"cache_creation_rate":0.0334,"cache_read_rate":0.0027,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (model_credit_rates ? 'gpt-5.6-luna');

-- 3) rate_limit_policies seeds.
UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        model_credit_rates,
        '{gpt-5.6-sol}',
        '{"input_rate":0.1334,"output_rate":0.8,"cache_creation_rate":0.1668,"cache_read_rate":0.0134,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (model_credit_rates ? 'gpt-5.6-sol');

UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        model_credit_rates,
        '{gpt-5.6-terra}',
        '{"input_rate":0.0666,"output_rate":0.4,"cache_creation_rate":0.0834,"cache_read_rate":0.0066,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (model_credit_rates ? 'gpt-5.6-terra');

UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        model_credit_rates,
        '{gpt-5.6-luna}',
        '{"input_rate":0.0266,"output_rate":0.16,"cache_creation_rate":0.0334,"cache_read_rate":0.0027,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (model_credit_rates ? 'gpt-5.6-luna');
