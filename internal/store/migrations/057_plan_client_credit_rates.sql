-- 057_plan_client_credit_rates.sql
--
-- Per-client per-model credit rate overlay for subscription consumption.
-- Shape (JSON object indexed by client bucket, then model name):
--
--   {
--     "claude-code-cli": {
--       "claude-sonnet-4": { "input_rate": 3, "output_rate": 15, ... },
--       "claude-opus-4":   { "input_rate": 15, "output_rate": 75, ... }
--     },
--     "codex-cli": {
--       "gpt-5":           { "input_rate": 0.5, "output_rate": 4 }
--     }
--   }
--
-- Resolution order at runtime (Policy.ComputeCreditsForClient):
--   1. client_model_credit_rates[client][model]   (this column)
--   2. model_credit_rates[model]                  (existing column)
--   3. catalog model.default_credit_rate           (catalog truth)
--   4. model_credit_rates["_default"]              (plan-wide safety net)
--   5. zero (no billing)
--
-- Extra-usage requests do NOT consult this column — they bill at the
-- catalog default rate via computeExtraUsageCostCredits.
--
-- Default NULL on existing rows. Idempotent via IF NOT EXISTS. No down step.

ALTER TABLE plans
    ADD COLUMN IF NOT EXISTS client_model_credit_rates JSONB;
