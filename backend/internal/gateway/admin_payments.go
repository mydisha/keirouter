package gateway

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// adminListPaymentOrders returns every payment order for the Payments page.
func (s *Server) adminListPaymentOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := s.db.PaymentOrders().ListAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(orders))
	for _, o := range orders {
		view := s.orderView(o)
		view["google_sub"] = o.GoogleSub
		view["key_id"] = o.KeyID
		view["provider"] = o.Provider
		view["provider_payment_id"] = o.ProviderPaymentID
		view["actor"] = o.Actor
		view["hosting_instance_id"] = o.HostingInstanceID
		view["terms_version"] = o.TermsVersion
		view["terms_accepted_at"] = o.TermsAcceptedAt
		if key, kerr := s.identity.Get(r.Context(), o.KeyID); kerr == nil {
			view["key_display"] = key.Display
		}
		if u, uerr := s.db.PortalUsers().GetBySub(r.Context(), o.GoogleSub); uerr == nil {
			view["user_email"] = u.Email
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": out})
}

// adminPaymentSummary aggregates revenue and credit totals, plus operator
// economics derived from usage: the pre-markup upstream cost (what the operator
// pays providers) and the resulting profit on what users were charged.
func (s *Server) adminPaymentSummary(w http.ResponseWriter, r *http.Request) {
	orders, err := s.db.PaymentOrders().ListAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	var totalIDR, totalCredit int64
	counts := map[string]int{}
	for _, o := range orders {
		counts[string(o.Status)]++
		if o.Status == store.PaymentCompleted || o.Status == store.PaymentManual {
			totalIDR += o.AmountIDR
			totalCredit += o.CreditMicros
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_idr":        totalIDR,
		"total_credit_usd": float64(totalCredit) / 1_000_000,
		"count_by_status":  counts,
	})
}

// adminUsageEconomics returns lifetime operator economics computed from usage:
// charged revenue, pre-markup upstream cost, and profit. Only requests with a
// known upstream cost contribute to the margin so missing prices cannot inflate
// profit.
func (s *Server) adminUsageEconomics(w http.ResponseWriter, r *http.Request) {
	profit, err := s.usage.ProfitSince(r.Context(), store.DefaultTenantID, time.Time{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	billed := float64(profit.BilledNanos) / 1_000_000_000
	upstream := float64(profit.UpstreamNanos) / 1_000_000_000
	covered := float64(profit.CoveredNanos) / 1_000_000_000
	marginPct := 0.0
	if covered > 0 {
		marginPct = (covered - upstream) / covered * 100
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"billed_usd":      billed,
		"upstream_usd":    upstream,
		"profit_usd":      covered - upstream,
		"margin_pct":      marginPct,
		"requests":        profit.Requests,
		"priced_requests": profit.PricedRequests,
		"unpriced_usd":    billed - covered,
	})
}

// adminApprovePaymentOrder manually credits a pending order.
func (s *Server) adminApprovePaymentOrder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if len(reason) > 500 {
		writeError(w, http.StatusBadRequest, "reason must be 500 characters or fewer")
		return
	}
	order, err := s.db.PaymentOrders().Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "order not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if order.Status != store.PaymentPending {
		writeError(w, http.StatusConflict, "order is already "+string(order.Status))
		return
	}
	if order.Product == store.ProductHosting {
		applied, herr := s.markHostingOrderPaid(r.Context(), order, store.PaymentManual, "dashboard", reason)
		if herr != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, herr, "internal server error"))
			return
		}
		if !applied {
			writeError(w, http.StatusConflict, "order was already handled")
			return
		}
		updated, gerr := s.db.PaymentOrders().Get(r.Context(), order.ID)
		if gerr != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, gerr, "internal server error"))
			return
		}
		writeJSON(w, http.StatusOK, s.orderView(updated))
		return
	}
	updated, budget, applied, err := s.creditPaymentOrder(r.Context(), order, store.PaymentManual, "dashboard", reason)
	if err != nil {
		if errors.Is(err, ErrKeyDisabled) {
			writeError(w, http.StatusConflict, "order key is disabled")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if !applied {
		writeError(w, http.StatusConflict, "order was already handled")
		return
	}
	view := s.orderView(updated)
	view["budget_limit_usd"] = float64(budget.LimitMicros) / 1_000_000
	writeJSON(w, http.StatusOK, view)
}
