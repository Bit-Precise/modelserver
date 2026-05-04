# Project Savings Stat Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three KPI cards to each project's Overview page that compare equivalent cost at API standard pricing vs actual cost (subscription + extra usage), surfacing how much the active plan saved the user during the current subscription period.

**Architecture:** Query-time recomputation only — no schema changes. Two new SQL aggregation methods on `Store` produce per-model token sums and per-window extra-usage spend. A pure function in `internal/billing/savings.go` combines those with the in-memory model catalog (for `default_credit_rate`), the `credit_price_fen` config, and the active subscription/plan to produce a `CostBreakdown`. The existing `GET /api/v1/projects/:projectID/usage` (default branch) returns this breakdown as a new optional field, consumed by `OverviewPage.tsx`.

**Tech Stack:** Go 1.22 (chi router, pgx/v5), React + TypeScript (TanStack Query, shadcn StatCard, Tailwind grid).

**Spec:** `docs/superpowers/specs/2026-05-04-project-savings-stat-design.md`

---

## File Structure

**New files:**
- `internal/store/usage_test.go` — integration tests for the two new store methods (also covers existing usage methods that lack tests today)
- `internal/billing/savings.go` — `CostBreakdown` struct + `ComputeCostBreakdown` pure function
- `internal/billing/savings_test.go` — table-driven unit tests

**Modified files:**
- `internal/store/usage.go` — append `GetPerModelTokenSums`, `GetExtraUsageSpendInWindow`
- `internal/admin/handle_requests.go` — change `handleGetUsage` signature to accept catalog + creditPriceFen, build `cost_breakdown` for the default (overview) branch
- `internal/admin/routes.go` — pass `catalog` and `cfg.ExtraUsage.CreditPriceFen` to `handleGetUsage`
- `dashboard/src/api/types.ts` — extend `UsageOverview` with `cost_breakdown?: CostBreakdown`
- `dashboard/src/pages/dashboard/OverviewPage.tsx` — render the three new cards in a second grid row

---

## Task 1: Store — `GetPerModelTokenSums`

**Files:**
- Modify: `internal/store/usage.go` (append)
- Create: `internal/store/usage_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/usage_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"
)

func TestGetPerModelTokenSums(t *testing.T) {
	st := openTestStore(t)
	_, projectID := seedUserAndProject(t, st)

	ctx := context.Background()
	// Seed three requests across two models inside the window, plus one
	// outside the window that must NOT be counted.
	now := time.Now()
	insert := func(model string, in, out, cc, cr int64, at time.Time) {
		t.Helper()
		_, err := st.pool.Exec(ctx, `
			INSERT INTO requests (project_id, model, status, input_tokens, output_tokens,
				cache_creation_tokens, cache_read_tokens, credits_consumed, created_at)
			VALUES ($1, $2, 'success', $3, $4, $5, $6, 0, $7)`,
			projectID, model, in, out, cc, cr, at)
		if err != nil {
			t.Fatalf("insert request: %v", err)
		}
	}
	insert("claude-sonnet-4-6", 100, 200, 10, 20, now.Add(-1*time.Hour))
	insert("claude-sonnet-4-6", 50, 75, 0, 5, now.Add(-2*time.Hour))
	insert("gpt-5", 1000, 500, 0, 0, now.Add(-3*time.Hour))
	insert("claude-sonnet-4-6", 999, 999, 0, 0, now.Add(-48*time.Hour)) // outside window

	got, err := st.GetPerModelTokenSums(projectID, now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("GetPerModelTokenSums: %v", err)
	}

	byModel := make(map[string]PerModelTokenSums)
	for _, s := range got {
		byModel[s.Model] = s
	}
	if got, want := byModel["claude-sonnet-4-6"].RequestCount, int64(2); got != want {
		t.Errorf("claude RequestCount = %d, want %d", got, want)
	}
	if got, want := byModel["claude-sonnet-4-6"].InputTokens, int64(150); got != want {
		t.Errorf("claude InputTokens = %d, want %d", got, want)
	}
	if got, want := byModel["claude-sonnet-4-6"].OutputTokens, int64(275); got != want {
		t.Errorf("claude OutputTokens = %d, want %d", got, want)
	}
	if got, want := byModel["claude-sonnet-4-6"].CacheCreationTokens, int64(10); got != want {
		t.Errorf("claude CacheCreationTokens = %d, want %d", got, want)
	}
	if got, want := byModel["claude-sonnet-4-6"].CacheReadTokens, int64(25); got != want {
		t.Errorf("claude CacheReadTokens = %d, want %d", got, want)
	}
	if got, want := byModel["gpt-5"].InputTokens, int64(1000); got != want {
		t.Errorf("gpt-5 InputTokens = %d, want %d", got, want)
	}
	if _, present := byModel["claude-sonnet-4-6"]; !present {
		t.Errorf("claude row missing")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/coding/modelserver && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/store/ -run TestGetPerModelTokenSums -v`
Expected: FAIL — `undefined: GetPerModelTokenSums` (or `undefined: PerModelTokenSums`).

- [ ] **Step 3: Add the type and method**

Append to `internal/store/usage.go`:

```go
// PerModelTokenSums is one row of GetPerModelTokenSums output.
type PerModelTokenSums struct {
	Model               string `json:"model"`
	RequestCount        int64  `json:"request_count"`
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
}

// GetPerModelTokenSums returns per-model token totals for a project in
// [since, until). Used by the savings-stat path to fold per-model API
// standard pricing in Go without joining the in-memory model catalog.
func (s *Store) GetPerModelTokenSums(projectID string, since, until time.Time) ([]PerModelTokenSums, error) {
	rows, err := s.pool.Query(context.Background(), `
		SELECT model,
			COUNT(*),
			COALESCE(SUM(input_tokens),          0),
			COALESCE(SUM(output_tokens),         0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(cache_read_tokens),     0)
		FROM requests
		WHERE project_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY model`,
		projectID, since, until)
	if err != nil {
		return nil, fmt.Errorf("per-model token sums: %w", err)
	}
	defer rows.Close()

	var out []PerModelTokenSums
	for rows.Next() {
		var s PerModelTokenSums
		if err := rows.Scan(&s.Model, &s.RequestCount, &s.InputTokens,
			&s.OutputTokens, &s.CacheCreationTokens, &s.CacheReadTokens); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/coding/modelserver && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/store/ -run TestGetPerModelTokenSums -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver && \
git add internal/store/usage.go internal/store/usage_test.go && \
git commit -m "feat(store): add GetPerModelTokenSums for cost breakdown"
```

---

## Task 2: Store — `GetExtraUsageSpendInWindow`

**Files:**
- Modify: `internal/store/usage.go`
- Modify: `internal/store/usage_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/usage_test.go`:

```go
func TestGetExtraUsageSpendInWindow(t *testing.T) {
	st := openTestStore(t)
	_, projectID := seedUserAndProject(t, st)

	ctx := context.Background()
	now := time.Now()
	insertEU := func(isExtra bool, costFen int64, at time.Time) {
		t.Helper()
		_, err := st.pool.Exec(ctx, `
			INSERT INTO requests (project_id, model, status, input_tokens, output_tokens,
				cache_creation_tokens, cache_read_tokens, credits_consumed,
				is_extra_usage, extra_usage_cost_fen, created_at)
			VALUES ($1, 'claude-sonnet-4-6', 'success', 0, 0, 0, 0, 0, $2, $3, $4)`,
			projectID, isExtra, costFen, at)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	insertEU(true, 1234, now.Add(-1*time.Hour))
	insertEU(true, 5000, now.Add(-2*time.Hour))
	insertEU(false, 9999, now.Add(-3*time.Hour))         // not extra usage
	insertEU(true, 7777, now.Add(-72*time.Hour))         // outside window

	got, err := st.GetExtraUsageSpendInWindow(projectID, now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("GetExtraUsageSpendInWindow: %v", err)
	}
	if want := int64(6234); got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/coding/modelserver && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/store/ -run TestGetExtraUsageSpendInWindow -v`
Expected: FAIL — `undefined: GetExtraUsageSpendInWindow`.

- [ ] **Step 3: Add the method**

Append to `internal/store/usage.go`:

```go
// GetExtraUsageSpendInWindow returns the sum of extra_usage_cost_fen for
// is_extra_usage=true requests of the given project in [since, until).
func (s *Store) GetExtraUsageSpendInWindow(projectID string, since, until time.Time) (int64, error) {
	var total int64
	err := s.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(extra_usage_cost_fen), 0)
		FROM requests
		WHERE project_id = $1 AND created_at >= $2 AND created_at < $3
		  AND is_extra_usage = TRUE`,
		projectID, since, until).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("extra usage spend: %w", err)
	}
	return total, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/coding/modelserver && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/store/ -run TestGetExtraUsageSpendInWindow -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver && \
git add internal/store/usage.go internal/store/usage_test.go && \
git commit -m "feat(store): add GetExtraUsageSpendInWindow for cost breakdown"
```

---

## Task 3: Billing — `CostBreakdown` type + happy-path test

**Files:**
- Create: `internal/billing/savings.go`
- Create: `internal/billing/savings_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/billing/savings_test.go`:

```go
package billing

import (
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/modelcatalog"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

func newTestCatalog() modelcatalog.Catalog {
	return modelcatalog.New([]types.Model{
		{
			Name:   "claude-sonnet-4-6",
			Status: types.ModelStatusActive,
			DefaultCreditRate: &types.CreditRate{
				InputRate: 3, OutputRate: 15, CacheCreationRate: 3.75, CacheReadRate: 0.30,
			},
		},
		{
			Name:              "no-rate-model",
			Status:            types.ModelStatusActive,
			DefaultCreditRate: nil,
		},
	})
}

func TestComputeCostBreakdown_PaidPlanWithSavings(t *testing.T) {
	cat := newTestCatalog()
	sub := &types.Subscription{ID: "s1", Status: types.SubscriptionStatusActive,
		StartsAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	plan := &types.Plan{PricePerPeriod: 19900} // ¥199.00

	sums := []store.PerModelTokenSums{{
		Model: "claude-sonnet-4-6",
		// 1M input, 1M output → credits = 3*1e6 + 15*1e6 = 18e6
		// fen = ceil(18e6 * 5438 / 1e6) = ceil(97884) = 97884 → ¥978.84
		InputTokens: 1_000_000, OutputTokens: 1_000_000,
	}}

	got := ComputeCostBreakdown(sums, 0, cat, 5438, sub, plan)

	if got.APIStandardFen != 97884 {
		t.Errorf("APIStandardFen = %d, want 97884", got.APIStandardFen)
	}
	if got.SubscriptionFen != 19900 {
		t.Errorf("SubscriptionFen = %d, want 19900", got.SubscriptionFen)
	}
	if got.ExtraUsageFen != 0 {
		t.Errorf("ExtraUsageFen = %d, want 0", got.ExtraUsageFen)
	}
	if got.ActualPaidFen != 19900 {
		t.Errorf("ActualPaidFen = %d, want 19900", got.ActualPaidFen)
	}
	if got.SavedFen != 77984 {
		t.Errorf("SavedFen = %d, want 77984", got.SavedFen)
	}
	if !got.HasActiveSub {
		t.Errorf("HasActiveSub = false, want true")
	}
	if !got.PeriodStart.Equal(sub.StartsAt) || !got.PeriodEnd.Equal(sub.ExpiresAt) {
		t.Errorf("period mismatch: got [%v, %v]", got.PeriodStart, got.PeriodEnd)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/coding/modelserver && go test ./internal/billing/ -run TestComputeCostBreakdown_PaidPlanWithSavings -v`
Expected: FAIL — `undefined: ComputeCostBreakdown` / `undefined: CostBreakdown`.

- [ ] **Step 3: Implement the type and function**

Create `internal/billing/savings.go`:

```go
// Package billing — savings.go computes the per-project "API standard vs
// actual paid" breakdown surfaced in the project Overview page. Pure
// function so it stays cheaply unit-testable; the SQL aggregation lives
// in store.GetPerModelTokenSums.
package billing

import (
	"log/slog"
	"math"
	"time"

	"github.com/modelserver/modelserver/internal/modelcatalog"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

// CostBreakdown is the JSON-serializable shape consumed by the dashboard.
// All money fields are CNY fen.
type CostBreakdown struct {
	APIStandardFen  int64     `json:"api_standard_fen"`
	SubscriptionFen int64     `json:"subscription_fen"`
	ExtraUsageFen   int64     `json:"extra_usage_fen"`
	ActualPaidFen   int64     `json:"actual_paid_fen"`
	SavedFen        int64     `json:"saved_fen"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	HasActiveSub    bool      `json:"has_active_subscription"`
}

// ComputeCostBreakdown folds per-model token sums against the catalog's
// default credit rates to produce the equivalent API standard cost, then
// combines that with the active plan's PricePerPeriod and accumulated
// extra-usage spend.
//
// sub/plan may be nil (no active subscription); fallbackStart/fallbackEnd
// are used as the period in that case. Long-context multipliers are NOT
// applied in this v1; see spec §VII for the known limitation.
func ComputeCostBreakdown(
	sums []store.PerModelTokenSums,
	extraUsageFen int64,
	catalog modelcatalog.Catalog,
	creditPriceFen int64,
	sub *types.Subscription,
	plan *types.Plan,
	fallbackStart, fallbackEnd time.Time,
) CostBreakdown {
	var apiFen int64
	for _, s := range sums {
		m, ok := catalog.Lookup(s.Model)
		if !ok || m.DefaultCreditRate == nil {
			slog.Warn("savings: missing default credit rate, skipping model",
				"model", s.Model, "rows", s.RequestCount)
			continue
		}
		r := m.DefaultCreditRate
		credits := r.InputRate*float64(s.InputTokens) +
			r.OutputRate*float64(s.OutputTokens) +
			r.CacheCreationRate*float64(s.CacheCreationTokens) +
			r.CacheReadRate*float64(s.CacheReadTokens)
		// Per-model ceil so rounding never under-states the API standard cost.
		apiFen += int64(math.Ceil(credits * float64(creditPriceFen) / 1_000_000))
	}

	out := CostBreakdown{
		APIStandardFen: apiFen,
		ExtraUsageFen:  extraUsageFen,
	}
	if sub != nil && plan != nil {
		out.HasActiveSub = true
		out.SubscriptionFen = plan.PricePerPeriod
		out.PeriodStart = sub.StartsAt
		out.PeriodEnd = sub.ExpiresAt
	} else {
		out.PeriodStart = fallbackStart
		out.PeriodEnd = fallbackEnd
	}
	out.ActualPaidFen = out.SubscriptionFen + out.ExtraUsageFen
	if diff := out.APIStandardFen - out.ActualPaidFen; diff > 0 {
		out.SavedFen = diff
	}
	return out
}
```

Note: the test in Step 1 used the 6-arg form. Update it to pass two extra `time.Time` zeros for fallback because sub is non-nil here:

Update the call in the test:

```go
	got := ComputeCostBreakdown(sums, 0, cat, 5438, sub, plan, time.Time{}, time.Time{})
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/coding/modelserver && go test ./internal/billing/ -run TestComputeCostBreakdown_PaidPlanWithSavings -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver && \
git add internal/billing/savings.go internal/billing/savings_test.go && \
git commit -m "feat(billing): add ComputeCostBreakdown for project savings stat"
```

---

## Task 4: Billing — edge case tests

**Files:**
- Modify: `internal/billing/savings_test.go`

- [ ] **Step 1: Add the additional table-driven cases**

Append to `internal/billing/savings_test.go`:

```go
func TestComputeCostBreakdown_NoActiveSubscription(t *testing.T) {
	cat := newTestCatalog()
	sums := []store.PerModelTokenSums{{
		Model: "claude-sonnet-4-6",
		InputTokens: 1_000_000, OutputTokens: 1_000_000,
	}}
	fallbackStart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	fallbackEnd := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	got := ComputeCostBreakdown(sums, 1234, cat, 5438, nil, nil, fallbackStart, fallbackEnd)

	if got.HasActiveSub {
		t.Errorf("HasActiveSub = true, want false")
	}
	if got.SubscriptionFen != 0 {
		t.Errorf("SubscriptionFen = %d, want 0", got.SubscriptionFen)
	}
	if got.ActualPaidFen != 1234 {
		t.Errorf("ActualPaidFen = %d, want 1234", got.ActualPaidFen)
	}
	if got.SavedFen != 97884-1234 {
		t.Errorf("SavedFen = %d, want %d", got.SavedFen, 97884-1234)
	}
	if !got.PeriodStart.Equal(fallbackStart) || !got.PeriodEnd.Equal(fallbackEnd) {
		t.Errorf("fallback period not used")
	}
}

func TestComputeCostBreakdown_LowUsageClampsSavedToZero(t *testing.T) {
	cat := newTestCatalog()
	sub := &types.Subscription{Status: types.SubscriptionStatusActive,
		StartsAt: time.Now(), ExpiresAt: time.Now().Add(30 * 24 * time.Hour)}
	plan := &types.Plan{PricePerPeriod: 19900}

	// Tiny usage: 100 input tokens → credits = 300, fen = ceil(300*5438/1e6)=2
	sums := []store.PerModelTokenSums{{Model: "claude-sonnet-4-6", InputTokens: 100}}

	got := ComputeCostBreakdown(sums, 0, cat, 5438, sub, plan, time.Time{}, time.Time{})

	if got.APIStandardFen != 2 {
		t.Errorf("APIStandardFen = %d, want 2", got.APIStandardFen)
	}
	if got.SavedFen != 0 {
		t.Errorf("SavedFen = %d, want 0 (clamped)", got.SavedFen)
	}
}

func TestComputeCostBreakdown_UnknownModelSkipped(t *testing.T) {
	cat := newTestCatalog()
	sums := []store.PerModelTokenSums{
		{Model: "claude-sonnet-4-6", InputTokens: 1_000_000},        // counted
		{Model: "totally-unknown",  InputTokens: 1_000_000_000},     // skipped
		{Model: "no-rate-model",    InputTokens: 1_000_000_000},     // skipped (DefaultCreditRate==nil)
	}
	got := ComputeCostBreakdown(sums, 0, cat, 5438, nil, nil, time.Time{}, time.Time{})

	// Only claude row contributes: 1e6 input * 3 = 3e6 credits → ceil(3e6*5438/1e6)=16314
	if got.APIStandardFen != 16314 {
		t.Errorf("APIStandardFen = %d, want 16314 (unknown rows skipped)", got.APIStandardFen)
	}
}

func TestComputeCostBreakdown_ExtraUsageOnlyCountedThroughExtraField(t *testing.T) {
	cat := newTestCatalog()
	sub := &types.Subscription{Status: types.SubscriptionStatusActive,
		StartsAt: time.Now(), ExpiresAt: time.Now().Add(30 * 24 * time.Hour)}
	plan := &types.Plan{PricePerPeriod: 0} // free plan

	sums := []store.PerModelTokenSums{{Model: "claude-sonnet-4-6",
		InputTokens: 1_000_000, OutputTokens: 1_000_000}}
	extra := int64(50_000) // ¥500.00

	got := ComputeCostBreakdown(sums, extra, cat, 5438, sub, plan, time.Time{}, time.Time{})

	if got.ExtraUsageFen != 50_000 {
		t.Errorf("ExtraUsageFen = %d, want 50000", got.ExtraUsageFen)
	}
	if got.ActualPaidFen != 50_000 {
		t.Errorf("ActualPaidFen = %d, want 50000", got.ActualPaidFen)
	}
	// API standard 97884 − actual 50000 = 47884
	if got.SavedFen != 47884 {
		t.Errorf("SavedFen = %d, want 47884", got.SavedFen)
	}
}
```

- [ ] **Step 2: Run all billing tests**

Run: `cd /root/coding/modelserver && go test ./internal/billing/ -v`
Expected: all five `TestComputeCostBreakdown_*` cases PASS.

- [ ] **Step 3: Commit**

```bash
cd /root/coding/modelserver && \
git add internal/billing/savings_test.go && \
git commit -m "test(billing): cover savings edge cases (no-sub, low-usage, unknown model, extra usage)"
```

---

## Task 5: Admin handler — wire `cost_breakdown` into overview response

**Files:**
- Modify: `internal/admin/handle_requests.go`
- Modify: `internal/admin/routes.go`

- [ ] **Step 1: Update `handleGetUsage` signature and overview branch**

Edit `internal/admin/handle_requests.go`. Replace the `handleGetUsage` function with:

```go
func handleGetUsage(st *store.Store, catalog modelcatalog.Catalog, creditPriceFen int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := chi.URLParam(r, "projectID")
		q := r.URL.Query()

		since := time.Now().AddDate(0, 0, -30) // Default: last 30 days.
		until := time.Now()
		userProvidedWindow := false
		if v := q.Get("since"); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				since = t
				userProvidedWindow = true
			}
		}
		if v := q.Get("until"); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				until = t
				userProvidedWindow = true
			}
		}

		breakdown := q.Get("breakdown") // "model", "member", "daily"

		switch breakdown {
		case "model":
			data, err := st.GetUsageByModel(projectID, since, until)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", "failed to get usage")
				return
			}
			writeData(w, http.StatusOK, data)
		case "member":
			p := parsePagination(r)
			data, total, err := st.GetUsageByMember(projectID, since, until, p.Limit(), p.Offset())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", "failed to get usage")
				return
			}
			writeList(w, data, total, p.Page, p.Limit())
		case "daily":
			data, err := st.GetDailyUsage(projectID, since, until)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", "failed to get usage")
				return
			}
			writeData(w, http.StatusOK, data)
		default:
			// Determine the period for cost_breakdown:
			//  - If caller passed an explicit window, do NOT compute breakdown
			//    (subscription_fen would not align with the window).
			//  - Otherwise use the active subscription's period; when there is
			//    no active subscription, use the default 30-day window.
			var sub *types.Subscription
			var plan *types.Plan
			if !userProvidedWindow {
				sub, _ = st.GetActiveSubscription(projectID)
				if sub != nil {
					since, until = sub.StartsAt, sub.ExpiresAt
					if sub.PlanID != "" {
						plan, _ = st.GetPlanByID(sub.PlanID)
					}
				}
			}

			overview, err := st.GetUsageOverview(projectID, since, until)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", "failed to get usage")
				return
			}

			if !userProvidedWindow {
				sums, err := st.GetPerModelTokenSums(projectID, since, until)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "internal", "failed to get usage")
					return
				}
				extraFen, err := st.GetExtraUsageSpendInWindow(projectID, since, until)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "internal", "failed to get usage")
					return
				}
				cb := billing.ComputeCostBreakdown(sums, extraFen, catalog, creditPriceFen,
					sub, plan, since, until)
				overview["cost_breakdown"] = cb
			}

			writeData(w, http.StatusOK, overview)
		}
	}
}
```

Add the imports if not already present at the top of the file:

```go
import (
	// ...existing imports...
	"github.com/modelserver/modelserver/internal/billing"
	"github.com/modelserver/modelserver/internal/modelcatalog"
)
```

- [ ] **Step 2: Update routes.go to pass catalog + credit price**

In `internal/admin/routes.go`, line 219, replace:

```go
r.Get("/usage", handleGetUsage(st))
```

with:

```go
r.Get("/usage", handleGetUsage(st, catalog, cfg.ExtraUsage.CreditPriceFen))
```

- [ ] **Step 3: Build to verify everything compiles**

Run: `cd /root/coding/modelserver && go build ./...`
Expected: no errors.

- [ ] **Step 4: Run the full backend test suite**

Run: `cd /root/coding/modelserver && go test ./internal/admin/ ./internal/billing/ ./internal/store/ -count=1`
Expected: PASS (store integration tests skip without `TEST_DATABASE_URL` — that is the expected behavior in CI).

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver && \
git add internal/admin/handle_requests.go internal/admin/routes.go && \
git commit -m "feat(admin): include cost_breakdown in project usage overview response"
```

---

## Task 6: Frontend — extend `UsageOverview` type

**Files:**
- Modify: `dashboard/src/api/types.ts`

- [ ] **Step 1: Edit the type**

In `dashboard/src/api/types.ts`, replace the `UsageOverview` interface (around lines 288–294) with:

```ts
// --- Usage ---
export interface CostBreakdown {
  api_standard_fen: number;
  subscription_fen: number;
  extra_usage_fen: number;
  actual_paid_fen: number;
  saved_fen: number;
  period_start: string;
  period_end: string;
  has_active_subscription: boolean;
}

export interface UsageOverview {
  request_count: number;
  total_tokens: number;
  total_credits_k: number;
  since: string;
  until: string;
  cost_breakdown?: CostBreakdown;
}
```

- [ ] **Step 2: Verify TS compiles**

Run: `cd /root/coding/modelserver/dashboard && pnpm tsc --noEmit`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
cd /root/coding/modelserver && \
git add dashboard/src/api/types.ts && \
git commit -m "feat(dashboard): add CostBreakdown to UsageOverview type"
```

---

## Task 7: Frontend — render the three savings cards

**Files:**
- Modify: `dashboard/src/pages/dashboard/OverviewPage.tsx`

- [ ] **Step 1: Add a money formatter and import the new icon**

Near the top of `dashboard/src/pages/dashboard/OverviewPage.tsx`, in the existing lucide import line:

```tsx
import { Activity, Zap, Clock, Coins, Receipt, Wallet, PiggyBank } from "lucide-react";
```

Below the existing `formatNumber` function, add:

```tsx
function formatYuan(fen: number): string {
  const yuan = fen / 100;
  return `¥${yuan.toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function formatPeriod(startISO: string, endISO: string): string {
  const fmt = (s: string) => new Date(s).toLocaleDateString("zh-CN");
  return `${fmt(startISO)} – ${fmt(endISO)}`;
}
```

- [ ] **Step 2: Render the second card row**

In `OverviewPage.tsx`, immediately after the closing `</div>` of the existing 4-card grid (the one containing `Total Requests / Total Tokens / Total Credits / Avg Daily`), insert this block:

```tsx
{overview?.cost_breakdown && (
  <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
    <StatCard
      title="API 标准价"
      value={formatYuan(overview.cost_breakdown.api_standard_fen)}
      description={`按官方定价折算 · ${formatPeriod(
        overview.cost_breakdown.period_start,
        overview.cost_breakdown.period_end,
      )}`}
      icon={<Receipt className="h-4 w-4" />}
    />
    <StatCard
      title="本周期实付"
      value={formatYuan(overview.cost_breakdown.actual_paid_fen)}
      description={
        overview.cost_breakdown.has_active_subscription
          ? `订阅 ${formatYuan(overview.cost_breakdown.subscription_fen)} + 加油包 ${formatYuan(overview.cost_breakdown.extra_usage_fen)}`
          : `加油包 ${formatYuan(overview.cost_breakdown.extra_usage_fen)}`
      }
      icon={<Wallet className="h-4 w-4" />}
    />
    {overview.cost_breakdown.saved_fen > 0 ? (
      <StatCard
        title="套餐已为您节省"
        value={formatYuan(overview.cost_breakdown.saved_fen)}
        description={
          overview.cost_breakdown.api_standard_fen > 0
            ? `↓ ${Math.round(
                (overview.cost_breakdown.saved_fen / overview.cost_breakdown.api_standard_fen) * 100,
              )}% off`
            : ""
        }
        icon={<PiggyBank className="h-4 w-4" />}
      />
    ) : (
      <StatCard
        title="套餐已为您节省"
        value="—"
        description="本周期用量较低，套餐尚未回本"
        icon={<PiggyBank className="h-4 w-4" />}
      />
    )}
  </div>
)}
```

- [ ] **Step 3: Build to verify**

Run: `cd /root/coding/modelserver/dashboard && pnpm tsc --noEmit && pnpm build`
Expected: clean compile + successful Vite build.

- [ ] **Step 4: Commit**

```bash
cd /root/coding/modelserver && \
git add dashboard/src/pages/dashboard/OverviewPage.tsx && \
git commit -m "feat(dashboard): show API-standard vs actual-paid savings cards on project overview"
```

---

## Task 8: End-to-end smoke test

**Files:**
- None (manual / observational verification)

- [ ] **Step 1: Start the stack**

Run: `cd /root/coding/modelserver && docker-compose up -d` (or however the user normally runs the dev stack — the `dashboard` is typically served via Vite dev or nginx; pick whichever the user already uses).

- [ ] **Step 2: Hit the API directly to confirm the new field**

Pick a project with at least one historical request and an active subscription. Replace `<token>` and `<projectID>`:

```bash
curl -sS -H "Authorization: Bearer <token>" \
  "http://localhost:8080/api/v1/projects/<projectID>/usage" | jq '.data.cost_breakdown'
```

Expected: an object with all 8 fields populated, `period_start` matching the active sub start, `saved_fen >= 0`.

- [ ] **Step 3: Confirm the dashboard renders the new row**

Open the project Overview page in the browser. Expected:
- Three new cards appear under the existing 4-card row.
- Money values are formatted as `¥1,234.56`.
- "套餐已为您节省" shows a `↓ N% off` description when `saved_fen > 0`.
- For a freshly created project with negligible usage, the third card shows "本周期用量较低，套餐尚未回本".

- [ ] **Step 4: Confirm it gracefully degrades when no active sub**

Find or create a project with no active subscription. Hit the same endpoint. Expected: response still contains `cost_breakdown` with `has_active_subscription: false`, `subscription_fen: 0`, and the dashboard renders the row with the actual-paid card showing only the加油包 part.

- [ ] **Step 5: Confirm pass-through window suppresses the field**

```bash
curl -sS -H "Authorization: Bearer <token>" \
  "http://localhost:8080/api/v1/projects/<projectID>/usage?since=2026-04-01T00:00:00Z&until=2026-05-01T00:00:00Z" \
  | jq '.data.cost_breakdown'
```

Expected: `null` (field omitted) — confirms we don't compute the breakdown when caller imposes a custom window.

- [ ] **Step 6: Commit any small fixes discovered during smoke**

If smoke surfaces issues (formatting, nil deref, period off-by-one), fix and commit per discovery. Otherwise skip.

---

## Self-Review

**Spec coverage:**

| Spec section | Implemented in |
|---|---|
| §I Animation/UI cards | Task 7 |
| §II Formula | Task 3 (`ComputeCostBreakdown`) |
| §III.1 No schema change | Confirmed — no migration files anywhere |
| §III.2 Store layer (two methods) | Tasks 1 & 2 |
| §III.3 Pure billing function | Task 3 |
| §III.4 Endpoint reuse + new JSON field | Task 5 |
| §IV UI three cards + saved≤0 fallback + tooltip | Task 7 |
| §V Reuse `CreditPriceFen`, no new config | Task 5 (passes existing config) |
| §VI Unit + edge case tests | Tasks 1, 2, 3, 4 |
| §VII Long-context not applied | Documented as comment in `savings.go` Task 3 |

All spec sections covered.

**Placeholder scan:** Searched for "TBD/TODO/implement later/etc" — none in the plan.

**Type consistency:** `CostBreakdown` (Go) field names ↔ `CostBreakdown` (TS) field names are byte-identical via JSON tags. `PerModelTokenSums` consistent across Tasks 1, 3, 4. `ComputeCostBreakdown` signature is the same in Tasks 3, 4, 5 (8 args including `fallbackStart`, `fallbackEnd`).
