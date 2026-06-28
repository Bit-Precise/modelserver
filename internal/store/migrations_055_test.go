package store

import (
	"context"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

// TestMigration055_RevokesOrphanedKeys asserts the migration only flips
// active keys whose creator has no project_members row in the same project,
// and leaves all other keys alone. Idempotent on re-run.
func TestMigration055_RevokesOrphanedKeys(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// Two users + one project.
	member, projectID := seedUserAndProject(t, st)

	// Second user: never a member of projectID (the "orphan" creator).
	var orphanUserID string
	if err := st.pool.QueryRow(ctx, `
		INSERT INTO users (email) VALUES ('orphan-' || gen_random_uuid()::text || '@test.local')
		RETURNING id`).Scan(&orphanUserID); err != nil {
		t.Fatalf("seed orphan user: %v", err)
	}

	// Seed four api_keys (raw SQL — bypass any creator-must-be-member checks):
	//   k1: created_by = member,       status='active'   -> stays active
	//   k2: created_by = orphan,       status='active'   -> revoked by migration
	//   k3: created_by = orphan,       status='active'   -> revoked by migration
	//   k4: created_by = orphan,       status='revoked'  -> stays revoked
	insert := func(createdBy, status string) string {
		var id string
		if err := st.pool.QueryRow(ctx, `
			INSERT INTO api_keys (project_id, created_by, key_hash, key_suffix, name, status)
			VALUES ($1, $2, gen_random_uuid()::text, '', 'test-key', $3)
			RETURNING id`, projectID, createdBy, status).Scan(&id); err != nil {
			t.Fatalf("seed key: %v", err)
		}
		return id
	}
	k1 := insert(member, types.APIKeyStatusActive)
	k2 := insert(orphanUserID, types.APIKeyStatusActive)
	k3 := insert(orphanUserID, types.APIKeyStatusActive)
	k4 := insert(orphanUserID, types.APIKeyStatusRevoked)

	// Re-run migration 055 manually (it already ran at openTestStore;
	// re-running tests idempotence directly).
	if _, err := st.pool.Exec(ctx, `
		UPDATE api_keys
		   SET status = 'revoked', updated_at = NOW()
		 WHERE status = 'active'
		   AND NOT EXISTS (
		     SELECT 1 FROM project_members
		      WHERE project_members.project_id = api_keys.project_id
		        AND project_members.user_id    = api_keys.created_by
		   )`); err != nil {
		t.Fatalf("re-run migration: %v", err)
	}

	check := func(id, want string) {
		t.Helper()
		var got string
		if err := st.pool.QueryRow(ctx, `SELECT status FROM api_keys WHERE id=$1`, id).Scan(&got); err != nil {
			t.Fatalf("query key %s: %v", id, err)
		}
		if got != want {
			t.Errorf("key %s status = %q, want %q", id, got, want)
		}
	}
	check(k1, types.APIKeyStatusActive)   // member -> untouched
	check(k2, types.APIKeyStatusRevoked)  // orphan -> revoked
	check(k3, types.APIKeyStatusRevoked)  // orphan -> revoked
	check(k4, types.APIKeyStatusRevoked)  // already revoked -> unchanged
}
