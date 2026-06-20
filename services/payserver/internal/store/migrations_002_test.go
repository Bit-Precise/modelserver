package store

import (
	"context"
	"os"
	"testing"

	"github.com/modelserver/modelserver/services/payserver/internal/tenant"
)

// openTestStoreWithBootstrap mirrors openTestStore but feeds a real
// bootstrap so migration 002 succeeds on first apply. PAYSERVER_TEST_DB_URL
// must be set; uses "default-test-secret" for the bootstrap secret.
func openTestStoreWithBootstrap(t *testing.T) *Store {
	t.Helper()
	dbURL := os.Getenv("PAYSERVER_TEST_DB_URL")
	if dbURL == "" {
		t.Skip("set PAYSERVER_TEST_DB_URL to run (e.g. postgres://user:pass@localhost:5432/payserver_test?sslmode=disable)")
	}
	hash, err := tenant.HashSecret("default-test-secret")
	if err != nil {
		t.Fatalf("hash bootstrap secret: %v", err)
	}
	bootstrap := MigrationBootstrap{
		DefaultTenantSecretHash: hash,
		DefaultCallbackURL:      "https://test.example/webhook",
		DefaultCallbackSecret:   "test-callback-secret",
	}
	st, err := New(dbURL, testLogger(), bootstrap)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMigration002_TenantsTableExists(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	ctx := context.Background()
	var exists bool
	if err := st.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'tenants')
	`).Scan(&exists); err != nil {
		t.Fatalf("check tenants table: %v", err)
	}
	if !exists {
		t.Fatal("tenants table missing after migration 002")
	}
}

func TestMigration002_DefaultTenantPresent(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	ctx := context.Background()
	var name, callbackURL, callbackSecret, secretHash string
	if err := st.pool.QueryRow(ctx, `
		SELECT name, callback_url, callback_secret, secret_hash FROM tenants WHERE name = 'default'
	`).Scan(&name, &callbackURL, &callbackSecret, &secretHash); err != nil {
		t.Fatalf("read default tenant: %v", err)
	}
	if callbackURL != "https://test.example/webhook" {
		t.Errorf("default tenant callback_url = %q", callbackURL)
	}
	if callbackSecret != "test-callback-secret" {
		t.Errorf("default tenant callback_secret = %q", callbackSecret)
	}
	if !tenant.VerifySecret(secretHash, "default-test-secret") {
		t.Errorf("default tenant secret_hash doesn't verify against bootstrap secret")
	}
}

func TestMigration002_PaymentsHaveTenantID(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	ctx := context.Background()
	var nullCount int
	if err := st.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM payments WHERE tenant_id IS NULL`).Scan(&nullCount); err != nil {
		t.Fatalf("count null tenant_id: %v", err)
	}
	if nullCount != 0 {
		t.Fatalf("found %d payments with null tenant_id after migration 002", nullCount)
	}
}

func TestMigration002_DefaultTenantCannotBeDeleted(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	ctx := context.Background()
	// Seed a payment to make sure FK is enforced.
	var defaultID string
	if err := st.pool.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'default'`).Scan(&defaultID); err != nil {
		t.Fatalf("get default tenant id: %v", err)
	}
	if _, err := st.pool.Exec(ctx, `
		INSERT INTO payments (tenant_id, order_id, channel, amount, status)
		VALUES ($1, $2, 'wechat', 1, 'pending')
	`, defaultID, "test-fk-order-"+t.Name()); err != nil {
		t.Fatalf("insert payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(ctx, `DELETE FROM payments WHERE order_id = $1`, "test-fk-order-"+t.Name())
	})
	_, err := st.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, defaultID)
	if err == nil {
		t.Fatal("expected FK violation on default tenant delete; got nil")
	}
}
