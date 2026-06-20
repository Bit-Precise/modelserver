package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelserver/modelserver/services/payserver/internal/gateway"
	"github.com/modelserver/modelserver/services/payserver/internal/store"
)

const (
	maxRequestBodySize = 64 * 1024 // 64 KB
	maxReturnURLLen    = 2048      // RFC-suggested URL ceiling; Stripe/alipay tolerate well under this
)

// validateReturnURL accepts an empty string (caller's responsibility to
// fall back to a configured default) or a syntactically well-formed http /
// https URL with no embedded userinfo. Defense in depth against
// open-redirect / phishing-assist where callers pass through user input
// without their own allowlist.
func validateReturnURL(raw string) error {
	if raw == "" {
		return nil
	}
	if len(raw) > maxReturnURLLen {
		return fmt.Errorf("return_url too long")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("return_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("return_url scheme must be http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("return_url missing host")
	}
	if u.User != nil {
		return fmt.Errorf("return_url must not contain userinfo")
	}
	return nil
}

type paymentAPIRequest struct {
	OrderID       string            `json:"order_id"`
	ProductName   string            `json:"product_name"`
	Channel       string            `json:"channel"`
	Currency      string            `json:"currency"`
	Amount        int64             `json:"amount"`
	NotifyURL     string            `json:"notify_url"`
	ReturnURL     string            `json:"return_url"`
	CustomerEmail string            `json:"customer_email,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type paymentAPIResponse struct {
	PaymentRef string `json:"payment_ref"`
	PaymentURL string `json:"payment_url"`
	Status     string `json:"status"`
}

func handleCreatePayment(st *store.Store, gateways map[string]gateway.Gateway, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
		currentTenant := TenantFromContext(r.Context())

		var req paymentAPIRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.OrderID == "" || req.Channel == "" || req.Amount <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "order_id, channel, and amount are required"})
			return
		}

		if err := validateReturnURL(req.ReturnURL); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		gw, ok := gateways[req.Channel]
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported channel"})
			return
		}

		// Insert-first pattern: atomically insert a placeholder record or retrieve existing.
		// This prevents TOCTOU races where concurrent requests could both call the gateway.
		payment := &store.Payment{
			TenantID: currentTenant.ID,
			OrderID:  req.OrderID,
			Channel:  req.Channel,
			Amount:   req.Amount,
			Status:   "pending",
		}
		created, err := st.InsertOrGetPayment(payment)
		if err != nil {
			logger.Error("insert or get payment", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		if !created {
			// Existing record — idempotency handling. Cross-tenant guard:
			// payments.order_id is globally UNIQUE, so a malicious or
			// careless tenant could collide on another tenant's order_id
			// and the !created branch would otherwise leak that tenant's
			// payment.ID + payment.PaymentURL (Stripe Checkout URLs carry
			// session tokens). Reject the collision with a generic
			// conflict message before any data is returned.
			if payment.TenantID != currentTenant.ID {
				logger.Warn("cross-tenant order_id collision rejected",
					"order_id", req.OrderID,
					"requested_by_tenant", currentTenant.ID,
					"owning_tenant", payment.TenantID)
				writeJSON(w, http.StatusConflict, map[string]string{"error": "order_id already in use"})
				return
			}
			if payment.Status == "paid" {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "order already paid"})
				return
			}
			// Return existing pending payment.
			writeJSON(w, http.StatusOK, paymentAPIResponse{
				PaymentRef: payment.ID,
				PaymentURL: payment.PaymentURL,
				Status:     "pending",
			})
			return
		}

		// Same guard for the FK-collision case: InsertOrGetPayment's ON
		// CONFLICT path runs only when order_id matches; if tenant_id
		// somehow differs and a future runner relaxes constraints, we
		// still want to surface a deterministic conflict, not silently
		// rewrite an unrelated tenant's row.
		if payment.TenantID != currentTenant.ID {
			logger.Error("internal: InsertOrGetPayment returned a row from a different tenant on created=true",
				"order_id", req.OrderID,
				"requested_by_tenant", currentTenant.ID,
				"row_tenant", payment.TenantID)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		// New record inserted — call payment gateway.
		result, err := gw.CreatePayment(r.Context(), &gateway.PaymentRequest{
			OutTradeNo:    strings.ReplaceAll(req.OrderID, "-", ""),
			Description:   req.ProductName,
			Amount:        req.Amount,
			Currency:      req.Currency,
			ReturnURL:     req.ReturnURL,
			CustomerEmail: req.CustomerEmail,
			Metadata:      req.Metadata,
		})
		if err != nil {
			logger.Error("create payment", "channel", req.Channel, "order_id", req.OrderID, "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "payment gateway error"})
			return
		}

		// Update the record with gateway result.
		if err := st.UpdatePaymentGatewayResult(payment.ID, result.TradeNo, result.PaymentURL); err != nil {
			logger.Error("update payment gateway result", "order_id", req.OrderID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update payment record"})
			return
		}

		writeJSON(w, http.StatusOK, paymentAPIResponse{
			PaymentRef: payment.ID,
			PaymentURL: result.PaymentURL,
			Status:     "pending",
		})
	}
}


func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
