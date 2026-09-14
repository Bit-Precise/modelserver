-- 074_add_deepseek_v4_1_flash.sql
--
-- Register DeepSeek V4.1 Flash under the canonical name deepseek-v4.1-flash.
-- Routes, upstreams and model mappings remain operator-managed. The direct
-- DeepSeek API uses deepseek-flash; operators using that endpoint can set
-- an upstream model_map explicitly.
-- Existing DeepSeek V4 catalog entries and their rates remain independent.
--
-- Official sources (fetched 2026-09-14):
--   https://api-docs.deepseek.com/zh-cn/quick_start/pricing
--   https://api-docs.deepseek.com/zh-cn/news/news260910
-- CNY per 1M tokens: busy input=2, output=8, cached input=0.04;
-- idle input=1, output=4, cached input=0.02. Busy hours are Mon-Fri
-- 09:00-12:00 and 14:00-18:00 Asia/Shanghai. Use fixed busy prices, matching
-- migration 070's convention for time-dependent DeepSeek pricing.
--
-- Catalog rates = official CNY price / 54.38 (1M credits = CNY 54.38).
-- Plan/policy rates = official CNY price / 54.38 * 0.066, following the
-- DeepSeek convention from migrations 033/070. Rates are rounded to six
-- decimals, with seven decimals for the small subscription cache-read rate.
-- Cache misses count as ordinary input; there is no separate cache-write
-- charge. Pricing is flat across the 1M context window (no long_context).

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
    'deepseek-v4.1-flash',
    'DeepSeek V4.1 Flash',
    'DeepSeek V4.1 Flash — native vision, thinking and non-thinking modes, 1M context.',
    '{}',
    '{"input_rate":0.036778,"output_rate":0.147113,"cache_creation_rate":0,"cache_read_rate":0.000736}'::jsonb,
    'active',
    'deepseek',
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

-- Seed all plans while preserving operator-defined rates.
UPDATE plans
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{deepseek-v4.1-flash}',
        '{"input_rate":0.002427,"output_rate":0.009709,"cache_creation_rate":0,"cache_read_rate":0.0000485}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'deepseek-v4.1-flash');

-- Keep existing rate-limit policies in sync with plan defaults.
UPDATE rate_limit_policies
SET model_credit_rates = jsonb_set(
        COALESCE(model_credit_rates, '{}'::jsonb),
        '{deepseek-v4.1-flash}',
        '{"input_rate":0.002427,"output_rate":0.009709,"cache_creation_rate":0,"cache_read_rate":0.0000485}'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'deepseek-v4.1-flash');
