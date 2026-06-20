package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelserver/modelserver/services/payserver/internal/gateway"
	"github.com/modelserver/modelserver/services/payserver/internal/store"
	"github.com/modelserver/modelserver/services/payserver/internal/tenant"
)

// fakeGateway lets tests drive handleCreatePayment without hitting a real
// payment provider. The handler doesn't actually need to be reached for the
// cross-tenant guard — the guard rejects before the gateway call — but
// having one wired keeps the test honest if the guard ever moves.
type fakeGateway struct{ channel string }

func (g *fakeGateway) Channel() string { return g.channel }
func (g *fakeGateway) CreatePayment(_ context.Context, _ *gateway.PaymentRequest) (*gateway.PaymentResult, error) {
	return &gateway.PaymentResult{TradeNo: "fake-trade", PaymentURL: "https://fake.example/pay"}, nil
}

// makeTenant creates a tenant with a unique nanosecond-suffixed name and
// registers cleanup. Used by security tests that need >1 tenant per case
// or that may collide on rerun if seedTestTenant's t.Name()-only naming
// left stale rows from an interrupted previous run.
func makeTenant(t *testing.T, st *store.Store, label, secret string) *tenant.Tenant {
	t.Helper()
	hash, _ := tenant.HashSecret(secret)
	tt := &tenant.Tenant{
		Name:           fmt.Sprintf("sec-test-%s-%s-%d", t.Name(), label, time.Now().UnixNano()),
		SecretHash:     hash,
		CallbackURL:    "https://sec.example/cb",
		CallbackSecret: "cb",
		IsActive:       true,
	}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("create tenant %s: %v", label, err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM payments WHERE tenant_id = $1`, tt.ID)
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID)
	})
	return tt
}

// TestCreatePayment_CrossTenantOrderIDCollisionRejected exercises the
// security guard added in response to the auto-review finding:
// payments.order_id is globally UNIQUE, so a second tenant submitting the
// same order_id must not receive the first tenant's payment_url back.
func TestCreatePayment_CrossTenantOrderIDCollisionRejected(t *testing.T) {
	st := openTestStoreServer(t) // from auth_test.go: real DB store via PAYSERVER_TEST_DB_URL
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tenantA := makeTenant(t, st, "A", "secret-a")
	tenantB := makeTenant(t, st, "B", "secret-b")

	// Shared order_id that both tenants will try to use.
	orderID := "11111111-1111-1111-1111-111111111111"

	gateways := map[string]gateway.Gateway{"wechat": &fakeGateway{channel: "wechat"}}
	handler := handleCreatePayment(st, gateways, logger)

	// Tenant A creates the order first; succeeds.
	body, _ := json.Marshal(paymentAPIRequest{
		OrderID:  orderID,
		Channel:  "wechat",
		Amount:   2000,
		Currency: "CNY",
	})
	reqA := httptest.NewRequest("POST", "/payments", bytes.NewReader(body))
	reqA = reqA.WithContext(context.WithValue(reqA.Context(), ctxKeyTenant, tenantA))
	wA := httptest.NewRecorder()
	handler.ServeHTTP(wA, reqA)
	if wA.Code != http.StatusOK {
		t.Fatalf("tenant A create: got %d, body=%s", wA.Code, wA.Body.String())
	}
	var aResp paymentAPIResponse
	if err := json.Unmarshal(wA.Body.Bytes(), &aResp); err != nil {
		t.Fatalf("decode A response: %v", err)
	}
	if aResp.PaymentRef == "" {
		t.Fatal("tenant A response missing payment_ref")
	}
	// Make sure cleanup will catch this payment row even if assertions fail.
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM payments WHERE order_id = $1`, orderID)
	})

	// Tenant B tries the same order_id. The cross-tenant guard must reject
	// with 409 and a generic message — must NOT leak A's payment_ref/URL.
	reqB := httptest.NewRequest("POST", "/payments", bytes.NewReader(body))
	reqB = reqB.WithContext(context.WithValue(reqB.Context(), ctxKeyTenant, tenantB))
	wB := httptest.NewRecorder()
	handler.ServeHTTP(wB, reqB)
	if wB.Code != http.StatusConflict {
		t.Fatalf("tenant B should be rejected with 409; got %d, body=%s", wB.Code, wB.Body.String())
	}

	var bResp map[string]string
	_ = json.Unmarshal(wB.Body.Bytes(), &bResp)
	if msg := bResp["error"]; msg != "order_id already in use" {
		t.Errorf("expected generic error, got %q", msg)
	}
	// CRITICAL: tenant A's payment_ref/payment_url must NOT appear in B's response.
	respStr := wB.Body.String()
	if bytes.Contains([]byte(respStr), []byte(aResp.PaymentRef)) {
		t.Errorf("tenant B response leaked tenant A's payment_ref")
	}
	if aResp.PaymentURL != "" && bytes.Contains([]byte(respStr), []byte(aResp.PaymentURL)) {
		t.Errorf("tenant B response leaked tenant A's payment_url")
	}
}

// TestCreatePayment_SameTenantReplayReturnsExisting confirms that the
// guard doesn't break the legitimate idempotent-replay case: tenant A
// re-submitting its OWN order_id while the original is still pending
// must get the existing pending record back, not a 409.
func TestCreatePayment_SameTenantReplayReturnsExisting(t *testing.T) {
	st := openTestStoreServer(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tenantA := makeTenant(t, st, "A", "secret-a")
	orderID := "22222222-2222-2222-2222-222222222222"

	gateways := map[string]gateway.Gateway{"wechat": &fakeGateway{channel: "wechat"}}
	handler := handleCreatePayment(st, gateways, logger)

	body, _ := json.Marshal(paymentAPIRequest{
		OrderID: orderID, Channel: "wechat", Amount: 2000, Currency: "CNY",
	})

	// First call.
	req1 := httptest.NewRequest("POST", "/payments", bytes.NewReader(body))
	req1 = req1.WithContext(context.WithValue(req1.Context(), ctxKeyTenant, tenantA))
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first call: %d, %s", w1.Code, w1.Body.String())
	}
	var r1 paymentAPIResponse
	_ = json.Unmarshal(w1.Body.Bytes(), &r1)
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM payments WHERE order_id = $1`, orderID)
	})

	// Replay with same tenant + same order_id while still pending.
	req2 := httptest.NewRequest("POST", "/payments", bytes.NewReader(body))
	req2 = req2.WithContext(context.WithValue(req2.Context(), ctxKeyTenant, tenantA))
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("replay should return 200, got %d: %s", w2.Code, w2.Body.String())
	}
	var r2 paymentAPIResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &r2)
	if r2.PaymentRef != r1.PaymentRef {
		t.Errorf("replay returned different payment_ref: %q vs %q", r2.PaymentRef, r1.PaymentRef)
	}
}
