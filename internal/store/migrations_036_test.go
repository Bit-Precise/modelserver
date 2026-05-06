package store

import (
	"context"
	"testing"
)

// TestMigration036_BackfillsOnlyOpenAIOrCodexRoutes asserts the eligibility
// predicate of migration 036:
//
//   - A route on a purely openai (or codex) upstream group whose
//     request_kinds includes openai_responses MUST be auto-extended with
//     openai_responses_compact.
//   - A route whose group includes any non-openai/non-codex upstream
//     (e.g. vertex-openai, bedrock-openai) MUST NOT be auto-extended.
//   - A route that already lists openai_responses_compact MUST be left
//     unchanged (no duplicate, idempotent).
//
// The migration is applied by openTestStore on first connect. This test
// seeds rows AFTER migrations have applied, then re-runs the migration's
// UPDATE statement to exercise the predicate against the new fixture.
// Each row name is suffixed with gen_random_uuid() so re-runs and parallel
// tests are hermetic.
func TestMigration036_BackfillsOnlyOpenAIOrCodexRoutes(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// Helper: insert an upstream with a known provider; name suffixed for uniqueness.
	insertUpstream := func(label, provider string) string {
		var id string
		if err := st.pool.QueryRow(ctx, `
			INSERT INTO upstreams (name, provider, status, weight, supported_models)
			VALUES ('mig036-' || $1 || '-' || gen_random_uuid()::text, $2, 'active', 1, ARRAY['gpt-5'])
			RETURNING id`, label, provider).Scan(&id); err != nil {
			t.Fatalf("insert upstream %s: %v", label, err)
		}
		return id
	}

	// Helper: insert an upstream group containing the given upstreams.
	insertGroup := func(label string, upstreamIDs ...string) string {
		var gid string
		if err := st.pool.QueryRow(ctx, `
			INSERT INTO upstream_groups (name, lb_policy, status)
			VALUES ('mig036-' || $1 || '-' || gen_random_uuid()::text, 'weighted_random', 'active')
			RETURNING id`, label).Scan(&gid); err != nil {
			t.Fatalf("insert group %s: %v", label, err)
		}
		for _, uid := range upstreamIDs {
			if _, err := st.pool.Exec(ctx, `
				INSERT INTO upstream_group_members (upstream_group_id, upstream_id)
				VALUES ($1, $2)`, gid, uid); err != nil {
				t.Fatalf("insert member: %v", err)
			}
		}
		return gid
	}

	// Helper: insert a route with the given request_kinds against a group.
	insertRoute := func(label, gid string, kinds []string) string {
		var rid string
		if err := st.pool.QueryRow(ctx, `
			INSERT INTO routes (model_names, request_kinds, upstream_group_id, match_priority, status)
			VALUES (ARRAY['gpt-5'], $1, $2, 1, 'active')
			RETURNING id`, kinds, gid).Scan(&rid); err != nil {
			t.Fatalf("insert route %s: %v", label, err)
		}
		return rid
	}

	upOpenAI := insertUpstream("openai", "openai")
	upCodex := insertUpstream("codex", "codex")
	upVertexOA := insertUpstream("vertex-openai", "vertex-openai")

	gOpenAIOnly := insertGroup("openai-only", upOpenAI)
	gOpenAICodex := insertGroup("openai-codex", upOpenAI, upCodex)
	gMixed := insertGroup("mixed", upOpenAI, upVertexOA)

	rOpenAIOnly := insertRoute("openai-only", gOpenAIOnly, []string{"openai_responses"})
	rOpenAICodex := insertRoute("openai-codex", gOpenAICodex, []string{"openai_responses"})
	rMixed := insertRoute("mixed", gMixed, []string{"openai_responses"})
	rAlready := insertRoute("already-has-compact", gOpenAIOnly, []string{"openai_responses", "openai_responses_compact"})

	t.Cleanup(func() {
		// Best-effort cleanup so we don't leave fixtures behind.
		for _, rid := range []string{rOpenAIOnly, rOpenAICodex, rMixed, rAlready} {
			st.pool.Exec(ctx, `DELETE FROM routes WHERE id = $1`, rid)
		}
		for _, gid := range []string{gOpenAIOnly, gOpenAICodex, gMixed} {
			st.pool.Exec(ctx, `DELETE FROM upstream_group_members WHERE upstream_group_id = $1`, gid)
			st.pool.Exec(ctx, `DELETE FROM upstream_groups WHERE id = $1`, gid)
		}
		for _, uid := range []string{upOpenAI, upCodex, upVertexOA} {
			st.pool.Exec(ctx, `DELETE FROM upstreams WHERE id = $1`, uid)
		}
	})

	// Re-run the migration's UPDATE statement against the seeded fixture.
	if _, err := st.pool.Exec(ctx, `
		WITH eligible AS (
			SELECT rt.id
			FROM routes rt
			WHERE 'openai_responses' = ANY(rt.request_kinds)
			  AND NOT 'openai_responses_compact' = ANY(rt.request_kinds)
			  AND NOT EXISTS (
				  SELECT 1
				  FROM upstream_group_members m
				  JOIN upstreams u ON u.id = m.upstream_id
				  WHERE m.upstream_group_id = rt.upstream_group_id
					AND u.provider NOT IN ('openai', 'codex')
			  )
		)
		UPDATE routes
		SET request_kinds = array_append(request_kinds, 'openai_responses_compact')
		WHERE id IN (SELECT id FROM eligible)`); err != nil {
		t.Fatalf("re-run backfill: %v", err)
	}

	kindsFor := func(rid string) []string {
		var kinds []string
		if err := st.pool.QueryRow(ctx, `SELECT request_kinds FROM routes WHERE id = $1`, rid).Scan(&kinds); err != nil {
			t.Fatalf("read kinds for %s: %v", rid, err)
		}
		return kinds
	}

	hasKind := func(kinds []string, kind string) bool {
		for _, k := range kinds {
			if k == kind {
				return true
			}
		}
		return false
	}

	countKind := func(kinds []string, kind string) int {
		n := 0
		for _, k := range kinds {
			if k == kind {
				n++
			}
		}
		return n
	}

	if got := kindsFor(rOpenAIOnly); !hasKind(got, "openai_responses_compact") {
		t.Errorf("openai-only route kinds = %v, want includes openai_responses_compact", got)
	}
	if got := kindsFor(rOpenAICodex); !hasKind(got, "openai_responses_compact") {
		t.Errorf("openai-codex route kinds = %v, want includes openai_responses_compact", got)
	}
	if got := kindsFor(rMixed); hasKind(got, "openai_responses_compact") {
		t.Errorf("mixed route kinds = %v, want NOT to include openai_responses_compact", got)
	}
	if got := kindsFor(rAlready); countKind(got, "openai_responses_compact") != 1 {
		t.Errorf("already-has-compact kinds = %v, want exactly one openai_responses_compact", got)
	}
}
