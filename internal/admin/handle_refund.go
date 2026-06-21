package admin

import (
	"log/slog"
	"net/http"

	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

// handleBillingRefundWebhook applies a payserver-delivered refund event
// against the originating order. Mounted behind HMACAuthMiddleware so
// only payserver-signed requests reach this handler.
//
// Body shape: {order_id, amount, currency}
// NOTE: the actual payserver refund payload shape is TBD. This implements
// the brief-specified shape; a one-field Marshal update will reconcile if
// the real payserver sends differently-named fields.
func handleBillingRefundWebhook(st *store.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OrderID  string `json:"order_id"`
			Amount   int64  `json:"amount"`   // informational — actual reversal uses the order row's stored credits
			Currency string `json:"currency"` // informational
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid body")
			return
		}
		if body.OrderID == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "order_id required")
			return
		}

		order, err := st.GetOrderByID(body.OrderID)
		if err != nil || order == nil {
			writeError(w, http.StatusNotFound, "not_found", "order not found")
			return
		}

		switch order.OrderType {
		case types.OrderTypeExtraUsageTopup:
			newBal, err := st.RefundExtraUsageTopup(body.OrderID)
			if err != nil {
				logger.Error("refund failed", "order_id", body.OrderID, "err", err)
				writeError(w, http.StatusInternalServerError, "internal", "refund failed")
				return
			}
			logger.Info("refund applied",
				"order_id", body.OrderID,
				"new_balance_credits", newBal)
			writeData(w, http.StatusOK, map[string]any{
				"order_id":            body.OrderID,
				"new_balance_credits": newBal,
			})

		case types.OrderTypeSubscription:
			// Subscription refunds: out of scope for this PR; no-op with
			// observability so ops can investigate manually.
			logger.Warn("subscription refund received but unhandled",
				"order_id", body.OrderID)
			writeData(w, http.StatusAccepted, map[string]string{"status": "unhandled"})

		default:
			writeError(w, http.StatusBadRequest, "bad_request",
				"unknown order_type "+order.OrderType)
		}
	}
}
