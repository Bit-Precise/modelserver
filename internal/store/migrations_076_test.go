package store

import (
	"context"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

// Run the actual migration against a temporary pre-migration table, leaving
// permanent routes untouched. WebSocket must be opt-in, never backfilled.
func TestMigration076ResponsesWebsocketKind(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		CREATE TEMP TABLE routes (
			id INT PRIMARY KEY,
			request_kinds TEXT[] NOT NULL,
			CONSTRAINT routes_request_kinds_valid CHECK (
				request_kinds <@ ARRAY['openai_responses', 'openai_responses_compact']::TEXT[]
			)
		) ON COMMIT DROP;
		INSERT INTO routes VALUES (1, ARRAY['openai_responses']);`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrationsFS.ReadFile("migrations/076_responses_websocket_request_kind.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := tx.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("migration run %d: %v", i+1, err)
		}
	}
	var kinds []string
	if err := tx.QueryRow(ctx, "SELECT request_kinds FROM routes WHERE id = 1").Scan(&kinds); err != nil {
		t.Fatal(err)
	}
	if !equalStringSlices(kinds, []string{types.KindOpenAIResponses}) {
		t.Fatalf("existing HTTP route was changed: %v", kinds)
	}
	for i, kind := range types.AllRequestKinds {
		if _, err := tx.Exec(ctx, "INSERT INTO routes VALUES ($1, $2)", i+2, []string{kind}); err != nil {
			t.Fatalf("kind %s rejected by SQL: %v", kind, err)
		}
	}
	if _, err := tx.Exec(ctx, "INSERT INTO routes VALUES (100, $1)", []string{types.KindOpenAIResponses, types.KindOpenAIResponsesWebsocket}); err != nil {
		t.Fatalf("explicit combined route rejected: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO routes VALUES (101, ARRAY['unknown_kind'])"); err == nil {
		t.Fatal("unknown request kind accepted")
	}
}
