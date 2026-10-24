package gateway

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/payment"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// handlePortalPaymentConfig exposes the non-secret payment options the portal
// top-up page needs before/without a session.
func (s *Server) handlePortalPaymentConfig(w http.ResponseWriter, r *http.Request) {
	p := s.cfg.Payment
	if !p.Enabled || s.paymentClient == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	out := map[string]any{
		"enabled":                  true,
		"min_topup_idr":            p.MinTopupIDR,
		"max_topup_idr":            p.MaxTopupIDR,
		"payment_method_type_code": p.PaymentMethodTypeCode,
		"packages":                 configPackages(p.Packages),
	}
	if s.currencySvc != nil {
		if rate, source, ok := s.currencySvc.Current(r.Context()); ok {
			out["fx_rate"] = rate
			out["currency_source"] = source
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func configPackages(pkgs []config.PaymentPackage) []map[string]any {
	out := make([]map[string]any, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, map[string]any{"id": p.ID, "label": p.Label, "amount_idr": p.AmountIDR})
	}
	return out
}

// portalKeyID resolves the caller's claimed key binding. On failure it writes
// the error response and returns ok=false.
func (s *Server) portalKeyID(w http.ResponseWriter, r *http.Request) (store.PortalUser, string, bool) {
	sub, ok := s.portalSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "portal session required")
		return store.PortalUser{}, "", false
	}
	u, err := s.db.PortalUsers().GetBySub(r.Context(), portalGoogleSub(sub))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusConflict, "no api key claimed")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load portal user")
		}
		return store.PortalUser{}, "", false
	}
	return u, u.KeyID, true
}

type createOrderRequest struct {
	AmountIDR      int64  `json:"amount_idr"`
	PackageID      string `json:"package_id"`
	IdempotencyKey string `json:"idempotency_key"`
	TurnstileToken string `json:"turnstile_token"`
}

// handlePortalCreateOrder creates a SumoPod payment for the caller's key.
func (s *Server) handlePortalCreateOrder(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Payment.Enabled || s.paymentClient == nil {
		writeError(w, http.StatusServiceUnavailable, "payments are not enabled")
		return
	}
	var body createOrderRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if !s.turnstileVerify(w, r, body.TurnstileToken) {
		return
	}
	u, keyID, ok := s.portalKeyID(w, r)
	if !ok {
		return
	}
	key, err := s.identity.Get(r.Context(), keyID)
	if err != nil || key.Disabled {
		writeError(w, http.StatusConflict, "api key is not usable")
		return
	}

	p := s.cfg.Payment
	amountIDR := body.AmountIDR
	if body.PackageID != "" {
		found := false
		for _, pkg := range p.Packages {
			if pkg.ID == body.PackageID {
				amountIDR = pkg.AmountIDR
				found = true
				break
			}
		}
		if !found {
			writeError(w, http.StatusBadRequest, "unknown package")
			return
		}
	}
	if amountIDR < p.MinTopupIDR || amountIDR > p.MaxTopupIDR {
		writeError(w, http.StatusBadRequest, "amount_idr is out of range")
		return
	}

	idem := strings.TrimSpace(body.IdempotencyKey)
	if idem == "" {
		idem = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	if len(idem) > maxIdempotencyKeyLen {
		writeError(w, http.StatusBadRequest, "idempotency key is too long")
		return
	}
	if idem != "" {
		if existing, gerr := s.db.PaymentOrders().GetByIdempotencyKey(r.Context(), idem); gerr == nil {
			if existing.KeyID != keyID {
				writeError(w, http.StatusConflict, "idempotency key already used")
				return
			}
			s.writeOrderResponse(w, http.StatusOK, existing)
			return
		} else if !errors.Is(gerr, store.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, gerr, "internal server error"))
			return
		}
	}

	if s.currencySvc == nil {
		writeError(w, http.StatusServiceUnavailable, "currency rate unavailable")
		return
	}
	rate, _, ok := s.currencySvc.Current(r.Context())
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "currency rate unavailable")
		return
	}
	creditMicros, rateMicros, err := idrToMicros(amountIDR, rate)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "currency rate unavailable")
		return
	}

	orderID := uuid.NewString()
	successURL := p.SuccessReturnURL
	if successURL == "" {
		successURL = s.publicBaseURL(r) + "/portal/topup?status=success"
	}
	cancelURL := p.CancelReturnURL
	if cancelURL == "" {
		cancelURL = s.publicBaseURL(r) + "/portal/topup?status=cancel"
	}

	created, err := s.paymentClient.CreatePayment(r.Context(), payment.CreateRequest{
		OrderID: orderID, AmountIDR: amountIDR, Currency: "IDR",
		ExpiresInHours: p.ExpiresInHours, SuccessReturnURL: successURL,
		CancelReturnURL: cancelURL, PaymentMethodTypeCode: p.PaymentMethodTypeCode,
	})
	if err != nil {
		s.log.Error("payment: create failed", "err", err)
		writeError(w, http.StatusBadGateway, "failed to create payment")
		return
	}

	now := time.Now()
	o := store.PaymentOrder{
		ID: orderID, TenantID: adminTenant, KeyID: keyID, GoogleSub: u.GoogleSub,
		AmountIDR: amountIDR, CreditMicros: creditMicros, FxRateMicros: rateMicros,
		Status: store.PaymentPending, Provider: p.Provider,
		ProviderPaymentID: created.PaymentID, PaymentLinkURL: created.PaymentLinkURL,
		IdempotencyKey: idem, Actor: "portal", CreatedAt: now, UpdatedAt: now,
		ExpiresAt: created.ExpiresAt,
	}
	if err := s.db.PaymentOrders().Create(r.Context(), o); err != nil {
		if idem != "" {
			if existing, gerr := s.db.PaymentOrders().GetByIdempotencyKey(r.Context(), idem); gerr == nil {
				if existing.KeyID != keyID {
					writeError(w, http.StatusConflict, "idempotency key already used")
					return
				}
				s.writeOrderResponse(w, http.StatusOK, existing)
				return
			}
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	s.writeOrderResponse(w, http.StatusCreated, o)
}

// writeOrderResponse renders an order. credit_usd and fx_rate are display-only
// floats derived from the integer micros stored on the order.
func (s *Server) writeOrderResponse(w http.ResponseWriter, status int, o store.PaymentOrder) {
	writeJSON(w, status, map[string]any{
		"order_id":         o.ID,
		"status":           string(o.Status),
		"amount_idr":       o.AmountIDR,
		"credit_usd":       float64(o.CreditMicros) / 1_000_000,
		"fx_rate":          float64(o.FxRateMicros) / 1_000_000,
		"payment_link_url": o.PaymentLinkURL,
		"expires_at":       o.ExpiresAt,
		"created_at":       o.CreatedAt,
	})
}

// handlePortalListOrders lists the caller's orders.
func (s *Server) handlePortalListOrders(w http.ResponseWriter, r *http.Request) {
	_, keyID, ok := s.portalKeyID(w, r)
	if !ok {
		return
	}
	orders, err := s.db.PaymentOrders().ListByKey(r.Context(), keyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(orders))
	for _, o := range orders {
		if productOrCredit(o.Product) != store.ProductCredit {
			continue // hosting orders show on the hosting page
		}
		out = append(out, s.orderView(o))
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": out})
}

func productOrCredit(p string) string {
	if p == "" {
		return store.ProductCredit
	}
	return p
}

// handlePortalGetOrder returns one order the caller owns.
func (s *Server) handlePortalGetOrder(w http.ResponseWriter, r *http.Request) {
	_, keyID, ok := s.portalKeyID(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	o, err := s.db.PaymentOrders().Get(r.Context(), id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			sanitizeError(s.log, err, "portal get order: load failed")
		}
		writeError(w, http.StatusNotFound, "order not found")
		return
	}
	if o.KeyID != keyID {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}
	s.writeOrderResponse(w, http.StatusOK, o)
}

func (s *Server) orderView(o store.PaymentOrder) map[string]any {
	return map[string]any{
		"order_id": o.ID, "status": string(o.Status), "amount_idr": o.AmountIDR,
		"credit_usd": float64(o.CreditMicros) / 1_000_000,
		"fx_rate":    float64(o.FxRateMicros) / 1_000_000,
		"created_at": o.CreatedAt, "paid_at": o.PaidAt,
		"product": productOrCredit(o.Product),
	}
}
