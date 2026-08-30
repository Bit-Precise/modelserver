-- 070_add_aliyun_opencode_models.sql
--
-- Register the models listed by Alibaba Cloud Model Studio's OpenCode Token
-- Plan (Team edition), and seed their subscription prices into every plan and
-- rate-limit policy. The migration is intentionally additive: model routes and
-- upstreams are not created here; operators can point the canonical names at a
-- DashScope/OpenCode-compatible upstream after deployment.
--
-- Sources (fetched 2026-08-30, Beijing region / CNY):
--   https://help.aliyun.com/zh/model-studio/opencode#oc-tp-section
--   https://help.aliyun.com/zh/model-studio/model-pricing
--   https://help.aliyun.com/zh/model-studio/context-cache
--
-- Catalog rates are Alibaba's published CNY price per 1M tokens divided by
-- 54.38, the project's CNY credit-unit price (1M credits = ¥54.38). Plan and
-- policy rates are the internal subscription rates: 0.1x catalog for Qwen,
-- Kimi, GLM and MiniMax, and 0.066x catalog for the DeepSeek variants (the
-- existing DeepSeek V4 convention).
--
-- Official prices below are the standard, non-promotional prices. The current
-- Qwen 3.7 promotion is not encoded. DeepSeek 0813/0731 publish busy/idle
-- prices; the busy (higher) price is used as the fixed catalog rate so costs
-- are never underestimated when traffic is in the busy window.
--
-- CreditRate has one cache tier and one long-context threshold. For models in
-- Alibaba's context-cache support list, cache creation is represented as
-- 125% of standard input and cache reads as 10% (the explicit-cache rate).
-- Implicit-cache reads are documented at 20%, but cannot be represented at the
-- same time by the current schema. Models outside that support list keep cache
-- rates at zero. Long-context prices are represented by one threshold and the
-- corresponding multiplier for the higher tier; the multiplier applies to the
-- complete request, matching internal/types/policy.go.
--
-- deepseek-v4-pro, deepseek-v4-flash and glm-5.2 already exist in this global
-- (provider-agnostic) catalog with direct-upstream prices. They are deliberately
-- left untouched: models are currently keyed by name rather than provider, so
-- replacing those rows with DashScope prices would silently change billing for
-- existing DeepSeek/Z.AI upstreams. The remaining 15 OpenCode names are added
-- below.

-- 1) Catalog rows. The three pre-existing names mentioned above are omitted;
--    ON CONFLICT refreshes metadata/rates if an operator pre-seeded a row.
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
        'qwen3.8-max',
        'Qwen3.8-Max',
        'Alibaba Qwen3.8-Max — OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.220669,"output_rate":0.662008,"cache_creation_rate":0.275837,"cache_read_rate":0.022067}'::jsonb,
        'active',
        'qwen',
        '{"context_window":1000000,"category":"chat"}'::jsonb
    ),
    (
        'qwen3.8-flash',
        'Qwen3.8-Flash',
        'Alibaba Qwen3.8-Flash — OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.014711,"output_rate":0.049651,"cache_creation_rate":0.018389,"cache_read_rate":0.001471}'::jsonb,
        'active',
        'qwen',
        '{"context_window":1000000,"category":"chat"}'::jsonb
    ),
    (
        'qwen3.7-max',
        'Qwen3.7-Max',
        'Alibaba Qwen3.7-Max — OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.220669,"output_rate":0.662008,"cache_creation_rate":0.275837,"cache_read_rate":0.022067}'::jsonb,
        'active',
        'qwen',
        '{"context_window":1000000,"category":"chat"}'::jsonb
    ),
    (
        'qwen3.7-plus',
        'Qwen3.7-Plus',
        'Alibaba Qwen3.7-Plus — OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.036778,"output_rate":0.147113,"cache_creation_rate":0.045973,"cache_read_rate":0.003678,"long_context":{"threshold_input_tokens":256000,"input_multiplier":4.0,"output_multiplier":3.0}}'::jsonb,
        'active',
        'qwen',
        '{"context_window":1000000,"category":"chat"}'::jsonb
    ),
    (
        'qwen3.6-plus',
        'Qwen3.6-Plus',
        'Alibaba Qwen3.6-Plus — OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.036778,"output_rate":0.220669,"cache_creation_rate":0.045973,"cache_read_rate":0.003678,"long_context":{"threshold_input_tokens":256000,"input_multiplier":4.0,"output_multiplier":4.0}}'::jsonb,
        'active',
        'qwen',
        '{"context_window":1000000,"category":"chat"}'::jsonb
    ),
    (
        'qwen3.6-flash',
        'Qwen3.6-Flash',
        'Alibaba Qwen3.6-Flash — OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.022067,"output_rate":0.132402,"cache_creation_rate":0.027584,"cache_read_rate":0.002207,"long_context":{"threshold_input_tokens":256000,"input_multiplier":4.0,"output_multiplier":4.0}}'::jsonb,
        'active',
        'qwen',
        '{"context_window":1000000,"category":"chat"}'::jsonb
    ),
    (
        'deepseek-v4-pro-0813',
        'DeepSeek V4 Pro 0813',
        'DeepSeek V4 Pro 0813 — Alibaba OpenCode Token Plan variant.',
        '{}',
        '{"input_rate":0.165502,"output_rate":0.496506,"cache_creation_rate":0,"cache_read_rate":0}'::jsonb,
        'active',
        'deepseek',
        '{"context_window":1000000,"category":"chat","pricing_window":"busy"}'::jsonb
    ),
    (
        'deepseek-v4-flash-0731',
        'DeepSeek V4 Flash 0731',
        'DeepSeek V4 Flash 0731 — Alibaba OpenCode Token Plan variant.',
        '{}',
        '{"input_rate":0.055167,"output_rate":0.165502,"cache_creation_rate":0,"cache_read_rate":0}'::jsonb,
        'active',
        'deepseek',
        '{"context_window":1000000,"category":"chat","pricing_window":"busy"}'::jsonb
    ),
    (
        'deepseek-v3.2',
        'DeepSeek V3.2',
        'DeepSeek V3.2 — Alibaba OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.036778,"output_rate":0.055167,"cache_creation_rate":0.045973,"cache_read_rate":0.003678}'::jsonb,
        'active',
        'deepseek',
        '{"context_window":1000000,"category":"chat"}'::jsonb
    ),
    (
        'kimi-k2.7-code',
        'Kimi K2.7 Code',
        'Moonshot Kimi K2.7 Code — Alibaba OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.119529,"output_rate":0.496506,"cache_creation_rate":0.149412,"cache_read_rate":0.011953}'::jsonb,
        'active',
        'moonshot',
        '{"context_window":1000000,"category":"chat"}'::jsonb
    ),
    (
        'kimi-k2.6',
        'Kimi K2.6',
        'Moonshot Kimi K2.6 — Alibaba OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.119529,"output_rate":0.496506,"cache_creation_rate":0.149412,"cache_read_rate":0.011953}'::jsonb,
        'active',
        'moonshot',
        '{"context_window":1000000,"category":"chat"}'::jsonb
    ),
    (
        'kimi-k2.5',
        'Kimi K2.5',
        'Moonshot Kimi K2.5 — Alibaba OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.073556,"output_rate":0.386171,"cache_creation_rate":0.091946,"cache_read_rate":0.007356}'::jsonb,
        'active',
        'moonshot',
        '{"context_window":1000000,"category":"chat"}'::jsonb
    ),
    (
        'glm-5.1',
        'GLM-5.1',
        'Zhipu GLM-5.1 — Alibaba OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.110335,"output_rate":0.441339,"cache_creation_rate":0.137918,"cache_read_rate":0.011033,"long_context":{"threshold_input_tokens":32000,"input_multiplier":1.3333333333,"output_multiplier":1.1666666667}}'::jsonb,
        'active',
        'zhipu',
        '{"context_window":200000,"category":"chat"}'::jsonb
    ),
    (
        'glm-5',
        'GLM-5',
        'Zhipu GLM-5 — Alibaba OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.073556,"output_rate":0.331004,"cache_creation_rate":0,"cache_read_rate":0,"long_context":{"threshold_input_tokens":32000,"input_multiplier":1.5,"output_multiplier":1.2222222222}}'::jsonb,
        'active',
        'zhipu',
        '{"context_window":198000,"category":"chat"}'::jsonb
    ),
    (
        'minimax-m2.5',
        'MiniMax-M2.5',
        'MiniMax M2.5 — Alibaba OpenCode Token Plan model.',
        '{}',
        '{"input_rate":0.038617,"output_rate":0.154469,"cache_creation_rate":0,"cache_read_rate":0}'::jsonb,
        'active',
        'minimax',
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

-- 2) Seed the internal subscription rates into every plan. Build a patch per
--    row so an operator-defined rate for one model is preserved while missing
--    models are still added on a partially migrated database.
WITH subscription_rates(model_name, rate) AS (
    VALUES
        ('qwen3.8-max', '{"input_rate":0.022067,"output_rate":0.066201,"cache_creation_rate":0.027584,"cache_read_rate":0.002207}'::jsonb),
        ('qwen3.8-flash', '{"input_rate":0.001471,"output_rate":0.004965,"cache_creation_rate":0.001839,"cache_read_rate":0.000147}'::jsonb),
        ('qwen3.7-max', '{"input_rate":0.022067,"output_rate":0.066201,"cache_creation_rate":0.027584,"cache_read_rate":0.002207}'::jsonb),
        ('qwen3.7-plus', '{"input_rate":0.003678,"output_rate":0.014711,"cache_creation_rate":0.004597,"cache_read_rate":0.000368,"long_context":{"threshold_input_tokens":256000,"input_multiplier":4.0,"output_multiplier":3.0}}'::jsonb),
        ('qwen3.6-plus', '{"input_rate":0.003678,"output_rate":0.022067,"cache_creation_rate":0.004597,"cache_read_rate":0.000368,"long_context":{"threshold_input_tokens":256000,"input_multiplier":4.0,"output_multiplier":4.0}}'::jsonb),
        ('qwen3.6-flash', '{"input_rate":0.002207,"output_rate":0.013240,"cache_creation_rate":0.002758,"cache_read_rate":0.000221,"long_context":{"threshold_input_tokens":256000,"input_multiplier":4.0,"output_multiplier":4.0}}'::jsonb),
        ('deepseek-v4-pro-0813', '{"input_rate":0.010923,"output_rate":0.032769,"cache_creation_rate":0,"cache_read_rate":0}'::jsonb),
        ('deepseek-v4-flash-0731', '{"input_rate":0.003641,"output_rate":0.010923,"cache_creation_rate":0,"cache_read_rate":0}'::jsonb),
        ('deepseek-v3.2', '{"input_rate":0.002427,"output_rate":0.003641,"cache_creation_rate":0.003034,"cache_read_rate":0.000243}'::jsonb),
        ('kimi-k2.7-code', '{"input_rate":0.011953,"output_rate":0.049651,"cache_creation_rate":0.014941,"cache_read_rate":0.001195}'::jsonb),
        ('kimi-k2.6', '{"input_rate":0.011953,"output_rate":0.049651,"cache_creation_rate":0.014941,"cache_read_rate":0.001195}'::jsonb),
        ('kimi-k2.5', '{"input_rate":0.007356,"output_rate":0.038617,"cache_creation_rate":0.009195,"cache_read_rate":0.000736}'::jsonb),
        ('glm-5.1', '{"input_rate":0.011033,"output_rate":0.044134,"cache_creation_rate":0.013792,"cache_read_rate":0.001103,"long_context":{"threshold_input_tokens":32000,"input_multiplier":1.3333333333,"output_multiplier":1.1666666667}}'::jsonb),
        ('glm-5', '{"input_rate":0.007356,"output_rate":0.033100,"cache_creation_rate":0,"cache_read_rate":0,"long_context":{"threshold_input_tokens":32000,"input_multiplier":1.5,"output_multiplier":1.2222222222}}'::jsonb),
        ('minimax-m2.5', '{"input_rate":0.003862,"output_rate":0.015447,"cache_creation_rate":0,"cache_read_rate":0}'::jsonb)
), missing AS (
    SELECT p.id, jsonb_object_agg(r.model_name, r.rate) AS patch
    FROM plans AS p
    CROSS JOIN subscription_rates AS r
    WHERE NOT (COALESCE(p.model_credit_rates, '{}'::jsonb) ? r.model_name)
    GROUP BY p.id
)
UPDATE plans AS p
SET model_credit_rates = COALESCE(p.model_credit_rates, '{}'::jsonb) || missing.patch,
    updated_at = NOW()
FROM missing
WHERE p.id = missing.id;

-- 3) Keep rate-limit policy defaults in lockstep with plan defaults.
WITH subscription_rates(model_name, rate) AS (
    VALUES
        ('qwen3.8-max', '{"input_rate":0.022067,"output_rate":0.066201,"cache_creation_rate":0.027584,"cache_read_rate":0.002207}'::jsonb),
        ('qwen3.8-flash', '{"input_rate":0.001471,"output_rate":0.004965,"cache_creation_rate":0.001839,"cache_read_rate":0.000147}'::jsonb),
        ('qwen3.7-max', '{"input_rate":0.022067,"output_rate":0.066201,"cache_creation_rate":0.027584,"cache_read_rate":0.002207}'::jsonb),
        ('qwen3.7-plus', '{"input_rate":0.003678,"output_rate":0.014711,"cache_creation_rate":0.004597,"cache_read_rate":0.000368,"long_context":{"threshold_input_tokens":256000,"input_multiplier":4.0,"output_multiplier":3.0}}'::jsonb),
        ('qwen3.6-plus', '{"input_rate":0.003678,"output_rate":0.022067,"cache_creation_rate":0.004597,"cache_read_rate":0.000368,"long_context":{"threshold_input_tokens":256000,"input_multiplier":4.0,"output_multiplier":4.0}}'::jsonb),
        ('qwen3.6-flash', '{"input_rate":0.002207,"output_rate":0.013240,"cache_creation_rate":0.002758,"cache_read_rate":0.000221,"long_context":{"threshold_input_tokens":256000,"input_multiplier":4.0,"output_multiplier":4.0}}'::jsonb),
        ('deepseek-v4-pro-0813', '{"input_rate":0.010923,"output_rate":0.032769,"cache_creation_rate":0,"cache_read_rate":0}'::jsonb),
        ('deepseek-v4-flash-0731', '{"input_rate":0.003641,"output_rate":0.010923,"cache_creation_rate":0,"cache_read_rate":0}'::jsonb),
        ('deepseek-v3.2', '{"input_rate":0.002427,"output_rate":0.003641,"cache_creation_rate":0.003034,"cache_read_rate":0.000243}'::jsonb),
        ('kimi-k2.7-code', '{"input_rate":0.011953,"output_rate":0.049651,"cache_creation_rate":0.014941,"cache_read_rate":0.001195}'::jsonb),
        ('kimi-k2.6', '{"input_rate":0.011953,"output_rate":0.049651,"cache_creation_rate":0.014941,"cache_read_rate":0.001195}'::jsonb),
        ('kimi-k2.5', '{"input_rate":0.007356,"output_rate":0.038617,"cache_creation_rate":0.009195,"cache_read_rate":0.000736}'::jsonb),
        ('glm-5.1', '{"input_rate":0.011033,"output_rate":0.044134,"cache_creation_rate":0.013792,"cache_read_rate":0.001103,"long_context":{"threshold_input_tokens":32000,"input_multiplier":1.3333333333,"output_multiplier":1.1666666667}}'::jsonb),
        ('glm-5', '{"input_rate":0.007356,"output_rate":0.033100,"cache_creation_rate":0,"cache_read_rate":0,"long_context":{"threshold_input_tokens":32000,"input_multiplier":1.5,"output_multiplier":1.2222222222}}'::jsonb),
        ('minimax-m2.5', '{"input_rate":0.003862,"output_rate":0.015447,"cache_creation_rate":0,"cache_read_rate":0}'::jsonb)
), missing AS (
    SELECT p.id, jsonb_object_agg(r.model_name, r.rate) AS patch
    FROM rate_limit_policies AS p
    CROSS JOIN subscription_rates AS r
    WHERE NOT (COALESCE(p.model_credit_rates, '{}'::jsonb) ? r.model_name)
    GROUP BY p.id
)
UPDATE rate_limit_policies AS p
SET model_credit_rates = COALESCE(p.model_credit_rates, '{}'::jsonb) || missing.patch,
    updated_at = NOW()
FROM missing
WHERE p.id = missing.id;
