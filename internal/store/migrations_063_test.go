package store

import (
	"context"
	"os"
	"testing"
)

// TestMigration063_NormalizesDirtyRolesBeforeAddingCheck recreates the
// pre-migration unconstrained state inside a transaction, seeds an unknown
// role, and applies the migration verbatim. Unknown roles must be downgraded
// to developer before the CHECK constraint becomes active.
func TestMigration063_NormalizesDirtyRolesBeforeAddingCheck(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `ALTER TABLE project_members DROP CONSTRAINT IF EXISTS project_members_role_check`); err != nil {
		t.Fatalf("drop role constraint: %v", err)
	}

	var ownerID, memberID, projectID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email) VALUES ('mig063-owner-' || gen_random_uuid()::text || '@test.local')
		RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email) VALUES ('mig063-member-' || gen_random_uuid()::text || '@test.local')
		RETURNING id`).Scan(&memberID); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO projects (name, created_by) VALUES ('mig063-project', $1)
		RETURNING id`, ownerID).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role)
		VALUES ($1, $2, 'legacy-admin')`, projectID, memberID); err != nil {
		t.Fatalf("seed dirty role: %v", err)
	}

	migration, err := os.ReadFile("migrations/063_project_member_role_check.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := tx.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	var role string
	if err := tx.QueryRow(ctx, `
		SELECT role FROM project_members
		WHERE project_id = $1 AND user_id = $2`, projectID, memberID).Scan(&role); err != nil {
		t.Fatalf("read normalized role: %v", err)
	}
	if role != "developer" {
		t.Fatalf("normalized role = %q, want developer", role)
	}

	if _, err := tx.Exec(ctx, `UPDATE project_members SET role = 'administrator' WHERE project_id = $1 AND user_id = $2`, projectID, memberID); err == nil {
		t.Fatal("role CHECK accepted administrator")
	}
}
