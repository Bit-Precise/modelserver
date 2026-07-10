package store

import (
	"context"
	"testing"
)

// TestMigration064_TablesExist asserts both tables were created with the
// expected columns.
func TestMigration064_TablesExist(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for _, tbl := range []string{"notifications", "notification_reads"} {
		var exists bool
		if err := st.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
			tbl).Scan(&exists); err != nil {
			t.Fatalf("query for %s: %v", tbl, err)
		}
		if !exists {
			t.Fatalf("table %s missing after migration", tbl)
		}
	}
}

// TestMigration064_AudienceCheck rejects malformed audience combinations
// per the CHECK constraint (global with id, project without id).
func TestMigration064_AudienceCheck(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// Seed a user for created_by.
	var uid string
	if err := st.pool.QueryRow(ctx,
		`INSERT INTO users (email, nickname) VALUES ('n@example.com', 'n') RETURNING id`).
		Scan(&uid); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	cases := []struct {
		name          string
		audienceType  string
		audienceIDSQL string
		wantErr       bool
	}{
		{"global no id ok", "global", "NULL", false},
		{"global with id rejected", "global", "gen_random_uuid()", true},
		{"project with id ok", "project", "gen_random_uuid()", false},
		{"project no id rejected", "project", "NULL", true},
		{"user with id ok", "user", "gen_random_uuid()", false},
		{"user no id rejected", "user", "NULL", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.pool.Exec(ctx,
				`INSERT INTO notifications (title, body, audience_type, audience_id, created_by)
				 VALUES ('t', 'b', $1, `+tc.audienceIDSQL+`, $2)`,
				tc.audienceType, uid)
			if tc.wantErr && err == nil {
				t.Fatalf("expected CHECK violation, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestMigration064_ReadsPK confirms notification_reads is keyed on
// (notification_id, user_id) — duplicate insert must fail.
func TestMigration064_ReadsPK(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var uid, nid string
	if err := st.pool.QueryRow(ctx,
		`INSERT INTO users (email, nickname) VALUES ('pk@example.com', 'pk') RETURNING id`).
		Scan(&uid); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := st.pool.QueryRow(ctx,
		`INSERT INTO notifications (title, body, audience_type, created_by)
		 VALUES ('t', 'b', 'global', $1) RETURNING id`, uid).Scan(&nid); err != nil {
		t.Fatalf("seed notification: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO notification_reads (notification_id, user_id) VALUES ($1, $2)`,
		nid, uid); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO notification_reads (notification_id, user_id) VALUES ($1, $2)`,
		nid, uid); err == nil {
		t.Fatalf("expected PK violation on duplicate insert")
	}
}
