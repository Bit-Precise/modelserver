# GPT-5.6 Family — Add sol / terra / luna to Catalog + Plans + Policies

Status: **draft — awaiting review**
Author: (assistant, drafted with user 2026-07-10)
Related migrations: `045_add_gpt_5_4_mini_nano.sql` (structural twin),
`047_gpt_5_x_plan_rebase.sql` (subscription multiplier),
`053_gpt_plan_prices_2x.sql` (current 2× tier),
`058_add_sonnet_5_fable_5.sql` (INSERT patterns with `long_context` block).

## 1. Goal

Register OpenAI's GPT-5.6 model family in modelserver's catalog and seed
per-model credit rates into every subscription plan and rate-limit policy,
so that:

- clients can send `model: "gpt-5.6"` / `"gpt-5.6-sol"` / `"gpt-5.6-terra"` /
  `"gpt-5.6-luna"` through the proxy;
- subscription-priced consumption bills at the same `catalog × 0.2` ratio
  used by the rest of the gpt-5.x family (`047` × `053`);
- extra-usage / non-subscription requests bill at the catalog rate
  (`official-price / 7.5`, project convention);
- OpenAI's newly published Short/Long-context two-tier pricing produces the
  correct multiplier on input, output, cached input, and cache-write tokens.

Out of scope:

- Priority / Batch / Flex tiers — these are choices the upstream OpenAI
  account makes; modelserver bills what the upstream actually charged.
- Any Go / provider-layer changes — the family is `provider: openai` and
  the existing `provider_openai.go` handles routing unchanged.
- Adding upstreams. Ops will attach the 3 models to relevant upstreams via
  the admin UI (`supported_models` / `model_map`) after deploy.

## 2. Source of truth

- Model list: <https://developers.openai.com/api/docs/models> (fetched
  2026-07-10). Only 3 IDs in the family — no `mini`/`nano`/`codex`
  variants are published.
- Pricing: <https://developers.openai.com/api/docs/pricing> (fetched
  2026-07-10). Standard tier only; Priority/Batch/Flex not used.

### 2.1 Standard-tier prices (USD per 1M tokens)

| Model         | Alias    | Context |   In (S) | Cached In (S) | Cache Write (S) | Out (S) |   In (L) | Cached In (L) | Cache Write (L) | Out (L) |
|---------------|----------|---------|---------:|--------------:|----------------:|--------:|---------:|--------------:|----------------:|--------:|
| gpt-5.6-sol   | gpt-5.6  |  1.05 M |   $5.00  |         $0.50 |         $6.25   |  $30.00 |  $10.00  |         $1.00 |        $12.50   |  $45.00 |
| gpt-5.6-terra | —        |  1.05 M |   $2.50  |         $0.25 |         $3.125  |  $15.00 |   $5.00  |         $0.50 |         $6.25   |  $22.50 |
| gpt-5.6-luna  | —        |  1.05 M |   $1.00  |         $0.10 |         $1.25   |   $6.00 |   $2.00  |         $0.20 |         $2.50   |   $9.00 |

Observations that the design relies on:

- `cached_input = input × 0.1` — same as prior gpt-5.x families.
- **`cache_write = input × 1.25` — new in 5.6.** Prior OpenAI generations
  did not bill cache writes separately (the previous convention captured
  in migration 027: `cache_creation_rate = 0` for gpt-5.5). Structure
  matches the Anthropic 5-min ephemeral-cache pricing and is directly
  representable by the existing `CacheCreationRate` field.
- Long / Short ratio is exactly `input×2, cached×2, cache_write×2,
  output×1.5` for all three models — a perfect fit for the existing
  `LongContextCreditRate{InputMultiplier: 2.0, OutputMultiplier: 1.5}`.

### 2.2 Long-context threshold

**OpenAI does not publish the input-token cutoff** for 5.6's Short/Long
tiers on either the models or pricing page. We reuse the project
convention `threshold_input_tokens = 272000`, established for 5.4-mini
and 5.4-nano in migrations `032_openai_long_context_pricing.sql` and
`045_add_gpt_5_4_mini_nano.sql`. A follow-up migration can adjust if the
official number turns out to be different.

## 3. Rate table

### 3.1 Catalog default rate (`credit = USD / 7.5`)

Applies to extra-usage / non-subscriber requests via
`computeExtraUsageCostCredits` (`internal/proxy/executor.go:1721`).

| Model         | input | output | cache_read | cache_creation | long_context               |
|---------------|------:|-------:|-----------:|---------------:|:---------------------------|
| gpt-5.6-sol   | 0.667 |  4.000 |      0.067 |          0.833 | thr=272000, in×2, out×1.5  |
| gpt-5.6-terra | 0.333 |  2.000 |      0.033 |          0.417 | same                       |
| gpt-5.6-luna  | 0.133 |  0.800 |      0.013 |          0.167 | same                       |

Numbers rounded to 3 decimals, matching the style of `058` (sonnet-5:
`0.4`, fable-5: `1.333`, etc.).

### 3.2 Plan / rate_limit_policies subscription rate (`catalog × 0.2`)

Applies to subscribers via `RateLimitPolicy.ComputeCreditsForClient`
(`internal/types/policy.go:148`). The `0.2` multiplier is the standing
gpt-5.x ratio (0.1 from `047` × 2 from `053`), so 5.6 lands on the same
"burn-rate per model" curve users are already accustomed to.

| Model         |  input | output | cache_read | cache_creation |
|---------------|-------:|-------:|-----------:|---------------:|
| gpt-5.6-sol   | 0.1334 |  0.800 |     0.0134 |         0.1668 |
| gpt-5.6-terra | 0.0666 |  0.400 |     0.0066 |         0.0834 |
| gpt-5.6-luna  | 0.0266 |  0.160 |     0.0027 |         0.0334 |

Every entry carries the same `long_context` block as its catalog row.

### 3.3 Why keep the OpenAI-style cache convention

Existing plans on gpt-5.x set `cache_creation_rate = 0` (OpenAI didn't
bill it). 5.6 is the first model where the number is non-zero. We use
the subscription-scaled value rather than the Anthropic-tier convention
(`cache_creation = input`) for two reasons:

- Consistency across the gpt-5.x family: every rate is
  `catalog_rate × 0.2`, one and only one multiplier.
- Faithfulness to upstream cost: OpenAI actually bills for cache writes;
  hiding it in a subscription-discount rewrite would push extra-usage-vs-
  subscription drift further apart than it already is.

If policy shifts later, one migration re-anchors the whole family at
once (mirrors `047`).

## 4. Go/policy behaviour — confirmed, no changes required

`internal/types/policy.go:195` (`ApplyLongContextCreditRate`) already
multiplies **input, output, cache_read AND cache_creation** by the
respective long-context multipliers, matching OpenAI's stated "Long tier
applies to the whole request" semantics. Both consumption paths call it:

- Subscription: `RateLimitPolicy.ComputeCreditsForClient@policy.go:185`
- Extra-usage: `computeExtraUsageCostCredits@executor.go:1721`

Therefore, once the catalog row and plan seeds carry the correct base
rates and a `long_context` block, the Long tier produces:

`input × 2, cached_input × 2, cache_write × 2, output × 1.5`

for every 5.6-family request whose accounted input exceeds 272 000 tokens
— exactly the OpenAI Long-tier price ratio.

No provider, executor, or middleware code needs to change.

## 5. Change list

### 5.1 New DB migration

`internal/store/migrations/062_add_gpt_5_6.sql`

Three parts, structural twin of `045_add_gpt_5_4_mini_nano.sql`:

1. `INSERT INTO models ... ON CONFLICT (name) DO NOTHING` for the three
   rows above. `aliases = {gpt-5.6}` only on `gpt-5.6-sol`.
   `metadata = {"context_window":1050000,"category":"chat"}`.
2. Three `UPDATE plans SET model_credit_rates = jsonb_set(..., NOT ?
   guard)` — one per model.
3. Three matching `UPDATE rate_limit_policies` — one per model.

Idempotent by design (`ON CONFLICT DO NOTHING` + `NOT ?` guards preserve
any operator override).

### 5.2 New Go test

`internal/store/migrations_062_test.go` — mirrors `migrations_045_test.go`
plus:

- catalog: 3 rows exist with expected `default_credit_rate` including
  non-zero `cache_creation_rate` and the `long_context` block;
  `metadata.context_window = 1050000`.
- alias: `Lookup("gpt-5.6")` resolves to `gpt-5.6-sol`.
- plans + policies: expected 3 keys present with expected rate values.
- second run of `062` is a no-op (`NOT ?` guards).
- worked example: `ApplyLongContextCreditRate` against sol at 200k input
  vs. 300k input tokens produces the manual-math credit numbers for
  short/long tiers respectively (locks the semantics regression-proof).

### 5.3 Utilization analyser base rates

`internal/admin/handle_utilization_analysis.go` — add three entries to
`utilizationAnalysisBaseRates` (before the `gpt-5.5` row so new-to-old
ordering is preserved). Each entry mirrors the plan/policy subscription
rate above and carries the `LongContext` field.

Extend `handle_utilization_analysis_test.go` assertions accordingly.

### 5.4 Dashboard default rates

`dashboard/src/pages/admin/PlansPage.tsx` — add three entries to
`DEFAULT_MODEL_CREDIT_RATES` (before `gpt-5.5`). Each carries the
non-zero `cache_creation_rate` and the `long_context` block, matching
the shape used by `gpt-5.4-mini`.

### 5.5 Left unchanged

- Provider layer (`provider_openai.go`, etc.) — model names flow through
  from the catalog.
- `types/upstream.go AllProviders` — still `openai`.
- `UsageGuideDialog.tsx` code sample still uses `gpt-5.4`; not updated
  in this change (independent copy-doc concern).

## 6. Validation

1. `go test ./internal/store/... ./internal/admin/... ./internal/types/...
   ./internal/proxy/...`
2. `go build ./...`
3. Dev PostgreSQL: `migrate up`; `SELECT name, default_credit_rate ->
   'long_context' FROM models WHERE name LIKE 'gpt-5.6%';` — 3 rows.
4. Admin UI → Plans → new plan: dropdown lists the 3 models; default
   rates auto-populated with `long_context` block; `cache_creation_rate`
   non-zero.
5. Attach `gpt-5.6-sol` to an OpenAI upstream via admin UI. Send
   `POST /v1/chat/completions {"model":"gpt-5.6", ...}` — catalog
   `Lookup` resolves to `gpt-5.6-sol`, request forwards, `request_usage`
   row has non-zero `credits` matching short-tier rate.
6. Construct a prompt whose accounted input > 272 000 tokens; verify
   `request_usage.credits` matches long-tier rate (input×2, output×1.5,
   cache_read×2, cache_creation×2 of the base).

## 7. Deploy & rollback

- **Deploy.** Merge → `docker compose up -d`. `store.Migrate` applies
  `062` on start; the change is data-only and idempotent, so re-deploys
  are safe. After deploy, ops adds the 3 models to relevant OpenAI
  upstreams' `supported_models` (or `model_map` if the upstream only
  advertises `gpt-5.6`). Front-end assets ship with the next dashboard
  release.
- **Rollback (soft).** `UPDATE models SET status='disabled' WHERE name
  LIKE 'gpt-5.6%';` — catalog `Lookup` still resolves the names but
  policy layers reject requests. Plan/policy seed rows are inert while
  status is disabled.
- **Rollback (hard).** Ship a `063_revert_gpt_5_6.sql` that `DELETE`s
  the 3 catalog rows and strips the 3 keys from `plans.model_credit_rates`
  and `rate_limit_policies.model_credit_rates`. Only needed if a mistake
  is discovered pre-adoption — after the first billed request, the
  `request_usage` snapshot preserves audit history regardless.

## 8. Risks

- **R1 — long-context threshold guess (272 000).** OpenAI has not
  published the actual cutoff. Impact: requests near the boundary bill
  at the wrong tier by a fraction. Mitigation: follow-up single-line
  migration when the number is known. Blast radius: small (bounded to
  requests in the 200k–350k range).
- **R2 — subscription curve consistency.** 5.6 sits at the top of the
  gpt-5.x pool; at `catalog × 0.2` sol's plan-side output rate (0.8) is
  6× the current 5.5 rate. This is intentional — it tracks the API
  price ratio — but pool operators should be aware before enabling sol
  broadly on lower-tier plans. No mitigation baked in; if usage-shock
  materialises, a per-plan override on sol lands next to this doc.
- **R3 — cache-writes newly billed.** Any customer whose workload
  writes into prompt cache heavily will see a non-trivial line-item that
  did not exist on 5.4/5.5. This is faithful to upstream cost. Called
  out in release notes so ops can pre-communicate.
