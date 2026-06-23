-- 053_gpt_plan_prices_2x.sql
--
-- Across-the-board 2× bump on every gpt-5.x and codex-auto-review
-- subscription rate. Catalog (default_credit_rate on models) is left
-- alone — that row represents real OpenAI API cost and is what
-- non-subscribers / extra-usage fall through to.
--
-- Same 13-key set as 047_gpt_5_x_plan_rebase.sql, same shallow-merge
-- semantics (`||`) — this is an authoritative re-anchor, not a
-- "preserve operator overrides" merge. Any operator who has set a
-- custom rate for one of these models must reapply it AFTER this
-- migration.
--
-- Long_context blocks on gpt-5.4-mini / gpt-5.4-nano are preserved
-- verbatim (multipliers do NOT change — only base rates double).
--
-- Already-recorded usage rows (request_usage etc.) snapshot the rate
-- at request time, so this migration only affects future requests.
--
-- The schema_migrations table guarantees this migration runs exactly
-- once per database.

-- ---------------------------------------------------------------------
-- Part 1: Plans
-- ---------------------------------------------------------------------

UPDATE plans
SET model_credit_rates = COALESCE(model_credit_rates, '{}'::jsonb) || '{
    "gpt-5.5":            {"input_rate":0.1334,"output_rate":0.8,"cache_creation_rate":0,"cache_read_rate":0.0134},
    "gpt-5.4":            {"input_rate":0.0666,"output_rate":0.4,"cache_creation_rate":0,"cache_read_rate":0.0066},
    "gpt-5.4-mini":       {"input_rate":0.0066,"output_rate":0.0534,"cache_creation_rate":0,"cache_read_rate":0.0006,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}},
    "gpt-5.4-nano":       {"input_rate":0.0014,"output_rate":0.0106,"cache_creation_rate":0,"cache_read_rate":0.0002,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}},
    "gpt-5.3-codex":      {"input_rate":0.0466,"output_rate":0.3734,"cache_creation_rate":0,"cache_read_rate":0.0046},
    "gpt-5.2":            {"input_rate":0.0466,"output_rate":0.3734,"cache_creation_rate":0,"cache_read_rate":0.0046},
    "gpt-5.2-codex":      {"input_rate":0.0466,"output_rate":0.3734,"cache_creation_rate":0,"cache_read_rate":0.0046},
    "gpt-5.1":            {"input_rate":0.0334,"output_rate":0.2666,"cache_creation_rate":0,"cache_read_rate":0.0034},
    "gpt-5.1-codex":      {"input_rate":0.0334,"output_rate":0.2666,"cache_creation_rate":0,"cache_read_rate":0.0034},
    "gpt-5.1-codex-max":  {"input_rate":0.0334,"output_rate":0.2666,"cache_creation_rate":0,"cache_read_rate":0.0034},
    "gpt-5.1-codex-mini": {"input_rate":0.0066,"output_rate":0.0534,"cache_creation_rate":0,"cache_read_rate":0.0006},
    "codex-auto-review":  {"input_rate":0.0466,"output_rate":0.3734,"cache_creation_rate":0,"cache_read_rate":0.0046}
}'::jsonb,
    updated_at = NOW();

-- ---------------------------------------------------------------------
-- Part 2: Same against rate_limit_policies
-- ---------------------------------------------------------------------

UPDATE rate_limit_policies
SET model_credit_rates = COALESCE(model_credit_rates, '{}'::jsonb) || '{
    "gpt-5.5":            {"input_rate":0.1334,"output_rate":0.8,"cache_creation_rate":0,"cache_read_rate":0.0134},
    "gpt-5.4":            {"input_rate":0.0666,"output_rate":0.4,"cache_creation_rate":0,"cache_read_rate":0.0066},
    "gpt-5.4-mini":       {"input_rate":0.0066,"output_rate":0.0534,"cache_creation_rate":0,"cache_read_rate":0.0006,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}},
    "gpt-5.4-nano":       {"input_rate":0.0014,"output_rate":0.0106,"cache_creation_rate":0,"cache_read_rate":0.0002,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}},
    "gpt-5.3-codex":      {"input_rate":0.0466,"output_rate":0.3734,"cache_creation_rate":0,"cache_read_rate":0.0046},
    "gpt-5.2":            {"input_rate":0.0466,"output_rate":0.3734,"cache_creation_rate":0,"cache_read_rate":0.0046},
    "gpt-5.2-codex":      {"input_rate":0.0466,"output_rate":0.3734,"cache_creation_rate":0,"cache_read_rate":0.0046},
    "gpt-5.1":            {"input_rate":0.0334,"output_rate":0.2666,"cache_creation_rate":0,"cache_read_rate":0.0034},
    "gpt-5.1-codex":      {"input_rate":0.0334,"output_rate":0.2666,"cache_creation_rate":0,"cache_read_rate":0.0034},
    "gpt-5.1-codex-max":  {"input_rate":0.0334,"output_rate":0.2666,"cache_creation_rate":0,"cache_read_rate":0.0034},
    "gpt-5.1-codex-mini": {"input_rate":0.0066,"output_rate":0.0534,"cache_creation_rate":0,"cache_read_rate":0.0006},
    "codex-auto-review":  {"input_rate":0.0466,"output_rate":0.3734,"cache_creation_rate":0,"cache_read_rate":0.0046}
}'::jsonb,
    updated_at = NOW();
