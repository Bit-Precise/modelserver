-- 067_gpt_plan_prices_2x_again.sql
--
-- Second across-the-board 2x bump on every gpt-5.x and codex-auto-review
-- subscription rate, on top of 053_gpt_plan_prices_2x.sql (so 4x the
-- 047_gpt_5_x_plan_rebase.sql baseline). Motivation: codex utilization
-- telemetry (official used_percent vs. locally accounted credits) showed
-- OpenAI's effective quota burn running ~2x ahead of the post-053 rate
-- table — see internal/admin/handle_utilization_analysis.go.
--
-- As in 053: catalog (default_credit_rate on models) is left alone — that
-- row represents real OpenAI API cost and is what non-subscribers /
-- extra-usage fall through to (computeExtraUsageCostCredits reads it).
--
-- Same 12-key set as 053 plus the 3 gpt-5.6 keys from 062, same
-- shallow-merge semantics (`||`) — this is an authoritative re-anchor,
-- not a "preserve operator overrides" merge. Any operator who has set a
-- custom rate for one of these models must reapply it AFTER this
-- migration. Per-client overrides (client_model_credit_rates, 057) are
-- NOT touched, matching 053.
--
-- long_context blocks on gpt-5.4-mini / gpt-5.4-nano / gpt-5.6-* are
-- preserved verbatim (multipliers do NOT change — only base rates
-- double). Note gpt-5.6 cache_creation doubles 062's rounded values
-- (0.1668 -> 0.3336 etc.), so it drifts ~0.0001 off the plan_input*1.25
-- invariant documented in 062 — immaterial, doubling is the intent.
--
-- Already-recorded usage rows (requests.credits_consumed etc.) snapshot
-- the rate at request time, so this migration only affects future
-- requests; utilization windows straddling the deploy will mix old and
-- new prices in their local-credit sums.
--
-- The schema_migrations table guarantees this migration runs exactly
-- once per database.

-- ---------------------------------------------------------------------
-- Part 1: Plans
-- ---------------------------------------------------------------------

UPDATE plans
SET model_credit_rates = COALESCE(model_credit_rates, '{}'::jsonb) || '{
    "gpt-5.5":            {"input_rate":0.2668,"output_rate":1.6,"cache_creation_rate":0,"cache_read_rate":0.0268},
    "gpt-5.4":            {"input_rate":0.1332,"output_rate":0.8,"cache_creation_rate":0,"cache_read_rate":0.0132},
    "gpt-5.4-mini":       {"input_rate":0.0132,"output_rate":0.1068,"cache_creation_rate":0,"cache_read_rate":0.0012,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}},
    "gpt-5.4-nano":       {"input_rate":0.0028,"output_rate":0.0212,"cache_creation_rate":0,"cache_read_rate":0.0004,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}},
    "gpt-5.3-codex":      {"input_rate":0.0932,"output_rate":0.7468,"cache_creation_rate":0,"cache_read_rate":0.0092},
    "gpt-5.2":            {"input_rate":0.0932,"output_rate":0.7468,"cache_creation_rate":0,"cache_read_rate":0.0092},
    "gpt-5.2-codex":      {"input_rate":0.0932,"output_rate":0.7468,"cache_creation_rate":0,"cache_read_rate":0.0092},
    "gpt-5.1":            {"input_rate":0.0668,"output_rate":0.5332,"cache_creation_rate":0,"cache_read_rate":0.0068},
    "gpt-5.1-codex":      {"input_rate":0.0668,"output_rate":0.5332,"cache_creation_rate":0,"cache_read_rate":0.0068},
    "gpt-5.1-codex-max":  {"input_rate":0.0668,"output_rate":0.5332,"cache_creation_rate":0,"cache_read_rate":0.0068},
    "gpt-5.1-codex-mini": {"input_rate":0.0132,"output_rate":0.1068,"cache_creation_rate":0,"cache_read_rate":0.0012},
    "codex-auto-review":  {"input_rate":0.0932,"output_rate":0.7468,"cache_creation_rate":0,"cache_read_rate":0.0092},
    "gpt-5.6-sol":        {"input_rate":0.2668,"output_rate":1.6,"cache_creation_rate":0.3336,"cache_read_rate":0.0268,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}},
    "gpt-5.6-terra":      {"input_rate":0.1332,"output_rate":0.8,"cache_creation_rate":0.1668,"cache_read_rate":0.0132,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}},
    "gpt-5.6-luna":       {"input_rate":0.0532,"output_rate":0.32,"cache_creation_rate":0.0668,"cache_read_rate":0.0054,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}
}'::jsonb,
    updated_at = NOW();

-- ---------------------------------------------------------------------
-- Part 2: Same against rate_limit_policies
-- ---------------------------------------------------------------------

UPDATE rate_limit_policies
SET model_credit_rates = COALESCE(model_credit_rates, '{}'::jsonb) || '{
    "gpt-5.5":            {"input_rate":0.2668,"output_rate":1.6,"cache_creation_rate":0,"cache_read_rate":0.0268},
    "gpt-5.4":            {"input_rate":0.1332,"output_rate":0.8,"cache_creation_rate":0,"cache_read_rate":0.0132},
    "gpt-5.4-mini":       {"input_rate":0.0132,"output_rate":0.1068,"cache_creation_rate":0,"cache_read_rate":0.0012,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}},
    "gpt-5.4-nano":       {"input_rate":0.0028,"output_rate":0.0212,"cache_creation_rate":0,"cache_read_rate":0.0004,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}},
    "gpt-5.3-codex":      {"input_rate":0.0932,"output_rate":0.7468,"cache_creation_rate":0,"cache_read_rate":0.0092},
    "gpt-5.2":            {"input_rate":0.0932,"output_rate":0.7468,"cache_creation_rate":0,"cache_read_rate":0.0092},
    "gpt-5.2-codex":      {"input_rate":0.0932,"output_rate":0.7468,"cache_creation_rate":0,"cache_read_rate":0.0092},
    "gpt-5.1":            {"input_rate":0.0668,"output_rate":0.5332,"cache_creation_rate":0,"cache_read_rate":0.0068},
    "gpt-5.1-codex":      {"input_rate":0.0668,"output_rate":0.5332,"cache_creation_rate":0,"cache_read_rate":0.0068},
    "gpt-5.1-codex-max":  {"input_rate":0.0668,"output_rate":0.5332,"cache_creation_rate":0,"cache_read_rate":0.0068},
    "gpt-5.1-codex-mini": {"input_rate":0.0132,"output_rate":0.1068,"cache_creation_rate":0,"cache_read_rate":0.0012},
    "codex-auto-review":  {"input_rate":0.0932,"output_rate":0.7468,"cache_creation_rate":0,"cache_read_rate":0.0092},
    "gpt-5.6-sol":        {"input_rate":0.2668,"output_rate":1.6,"cache_creation_rate":0.3336,"cache_read_rate":0.0268,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}},
    "gpt-5.6-terra":      {"input_rate":0.1332,"output_rate":0.8,"cache_creation_rate":0.1668,"cache_read_rate":0.0132,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}},
    "gpt-5.6-luna":       {"input_rate":0.0532,"output_rate":0.32,"cache_creation_rate":0.0668,"cache_read_rate":0.0054,"long_context":{"threshold_input_tokens":272000,"input_multiplier":2.0,"output_multiplier":1.5}}
}'::jsonb,
    updated_at = NOW();
