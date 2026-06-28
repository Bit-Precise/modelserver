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

// TestMigration055_DeletesOrphanedGrants asserts the second half of migration
// 055: orphan oauth_grants (no matching project_members row) are deleted,
// while grants for current members are preserved. Idempotent on re-run.
func TestMigration055_DeletesOrphanedGrants(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// One project with one member (owner from seedUserAndProject).
	member, projectID := seedUserAndProject(t, st)

	// Orphan user: NOT a member of projectID.
	var orphanUserID string
	if err := st.pool.QueryRow(ctx, `
		INSERT INTO users (email) VALUES ('orphan-grant-' || gen_random_uuid()::text || '@test.local')
		RETURNING id`).Scan(&orphanUserID); err != nil {
		t.Fatalf("seed orphan user: %v", err)
	}

	seedGrant := func(projectID, userID, clientID string) string {
		t.Helper()
		var id string
		if err := st.pool.QueryRow(ctx, `
			INSERT INTO oauth_grants (project_id, user_id, client_id, client_name, scopes)
			VALUES ($1, $2, $3, 'test-client', ARRAY['openid'])
			RETURNING id`, projectID, userID, clientID).Scan(&id); err != nil {
			t.Fatalf("seed grant: %v", err)
		}
		return id
	}

	// gA: member-grant (member is in project_members) → keep
	gA := seedGrant(projectID, member, "client-A")
	// gB, gC: orphan-grants (orphanUserID is NOT a member) → delete
	gB := seedGrant(projectID, orphanUserID, "client-A")
	gC := seedGrant(projectID, orphanUserID, "client-B")

	// Re-run the migration DELETE manually (idempotence test).
	if _, err := st.pool.Exec(ctx, `
		DELETE FROM oauth_grants
		 WHERE NOT EXISTS (
		     SELECT 1 FROM project_members
		      WHERE project_members.project_id = oauth_grants.project_id
		        AND project_members.user_id    = oauth_grants.user_id
		 )`); err != nil {
		t.Fatalf("re-run migration grants pass: %v", err)
	}

	countGrant := func(id string) int {
		t.Helper()
		var n int
		if err := st.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM oauth_grants WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatalf("count grant %s: %v", id, err)
		}
		return n
	}
	if countGrant(gA) != 1 {
		t.Errorf("member grant gA got deleted")
	}
	if countGrant(gB) != 0 {
		t.Errorf("orphan grant gB survived")
	}
	if countGrant(gC) != 0 {
		t.Errorf("orphan grant gC survived")
	}
}
