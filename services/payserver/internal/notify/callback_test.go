package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCallback_Send_PerCallTargetSigning(t *testing.T) {
	secret := "test-webhook-secret"

	var receivedBody []byte
	var receivedSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-Webhook-Signature")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewCallbackClient(5 * time.Second)
	payload := DeliveryPayload{
		OrderID: "order-123", PaymentRef: "pay-456", Status: "paid",
		PaidAmount: 2000, PaidAt: "2026-03-11T12:00:00Z",
	}

	target := CallbackTarget{URL: srv.URL, Secret: secret}
	if err := client.Send(t.Context(), target, payload); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var got DeliveryPayload
	if err := json.Unmarshal(receivedBody, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.OrderID != "order-123" {
		t.Errorf("OrderID = %q", got.OrderID)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(receivedBody)
	expected := hex.EncodeToString(mac.Sum(nil))
	if receivedSig != expected {
		t.Errorf("signature = %q, want %q", receivedSig, expected)
	}
}

func TestCallback_Send_EmptyURLIsNoop(t *testing.T) {
	client := NewCallbackClient(5 * time.Second)
	target := CallbackTarget{URL: "", Secret: "anything"}
	err := client.Send(t.Context(), target, DeliveryPayload{OrderID: "x"})
	if err != nil {
		t.Errorf("empty URL should be no-op success, got: %v", err)
	}
}

func TestCallback_Send_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewCallbackClient(5 * time.Second)
	target := CallbackTarget{URL: srv.URL, Secret: "s"}
	err := client.Send(t.Context(), target, DeliveryPayload{OrderID: "x"})
	if err == nil {
		t.Error("expected error on 500 response")
	}
}

func TestCallback_Send_PerCallDifferentSecrets(t *testing.T) {
	var sig1, sig2 string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sig1 == "" {
			sig1 = r.Header.Get("X-Webhook-Signature")
		} else {
			sig2 = r.Header.Get("X-Webhook-Signature")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewCallbackClient(5 * time.Second)
	pl := DeliveryPayload{OrderID: "x"}
	_ = client.Send(t.Context(), CallbackTarget{URL: srv.URL, Secret: "secret-a"}, pl)
	_ = client.Send(t.Context(), CallbackTarget{URL: srv.URL, Secret: "secret-b"}, pl)

	if sig1 == sig2 {
		t.Error("different secrets produced same signature — secret not used per-call")
	}
}

// TestCallback_Send_EmptySecretIsError covers the defense-in-depth check
// added in response to the auto-review: an empty signing secret combined
// with a non-empty URL would silently emit a HMAC over an empty key,
// which is trivially forgeable. Loud failure forces operator notice.
func TestCallback_Send_EmptySecretIsError(t *testing.T) {
	client := NewCallbackClient(5 * time.Second)
	err := client.Send(t.Context(),
		CallbackTarget{URL: "https://x.example/cb", Secret: ""},
		DeliveryPayload{OrderID: "x"})
	if err == nil {
		t.Error("expected error when secret empty + URL non-empty")
	}
}

// TestCallback_Send_InvalidURLSchemeRejected catches non-http(s) schemes
// before the request is built — second wall behind the admin write-path
// validation. file:// would otherwise let an attacker exfiltrate request
// bytes (no scheme check means net/http will try and fail in undefined
// ways).
func TestCallback_Send_InvalidURLSchemeRejected(t *testing.T) {
	client := NewCallbackClient(5 * time.Second)
	for _, raw := range []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"ftp://x.example/cb",
		"://no-scheme/cb",
	} {
		err := client.Send(t.Context(),
			CallbackTarget{URL: raw, Secret: "s"},
			DeliveryPayload{OrderID: "x"})
		if err == nil {
			t.Errorf("scheme %q should have been rejected", raw)
		}
	}
}

// TestCallback_Send_UserinfoInURLRejected ensures embedded credentials
// (https://attacker@victim.example) cannot be used to confuse upstream
// auth or to leak attacker-supplied auth tokens to victim logs.
func TestCallback_Send_UserinfoInURLRejected(t *testing.T) {
	client := NewCallbackClient(5 * time.Second)
	err := client.Send(t.Context(),
		CallbackTarget{URL: "https://attacker@victim.example/cb", Secret: "s"},
		DeliveryPayload{OrderID: "x"})
	if err == nil {
		t.Error("userinfo in URL should be rejected")
	}
}
