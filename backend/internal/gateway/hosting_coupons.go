package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/mydisha/keirouter/backend/internal/hosting"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// couponHoldGrace keeps a coupon hold a little past the payment expiry, so
// a payment completed at the last second still finds its reservation.
const couponHoldGrace = time.Hour

// couponChecksPerMinute caps coupon lookups per API key.
const couponChecksPerMinute = 10

// lockHostingOrders serializes hosting checkouts for one API key and returns
// the unlock func.
func (s *Server) lockHostingOrders(keyID string) func() {
	m, _ := s.hostingOrderLocks.LoadOrStore(keyID, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// hostingMinChargeIDR is the lowest price a coupon may bring an order to:
// the gateway's minimum payment.
func (s *Server) hostingMinChargeIDR() int64 {
	if m := s.cfg.Payment.MinTopupIDR; m > 0 {
		return m
	}
	return 1000
}

// couponHoldUntil is when a coupon hold lapses: the payment's expiry plus a
// grace, or the configured payment lifetime when the gateway gave none.
func (s *Server) couponHoldUntil(expiresAt string, now time.Time) time.Time {
	if t, err := time.Parse(time.RFC3339, expiresAt); err == nil && t.After(now) {
		return t.Add(couponHoldGrace)
	}
	hours := s.cfg.Payment.ExpiresInHours
	if hours <= 0 {
		hours = 24
	}
	return now.Add(time.Duration(hours)*time.Hour + couponHoldGrace)
}

// lookupHostingCoupon finds a coupon by code and checks that keyID may use
// it now. It returns the coupon, or a rejection code from package hosting.
// Lapsed holds on the coupon are released first so they do not block it.
func (s *Server) lookupHostingCoupon(ctx context.Context, code, keyID string, now time.Time) (store.HostingCoupon, string, error) {
	repo := s.db.HostingCoupons()
	c, err := repo.GetByCode(ctx, code)
	if errors.Is(err, store.ErrNotFound) {
		return store.HostingCoupon{}, hosting.CouponNotFound, nil
	}
	if err != nil {
		return store.HostingCoupon{}, "", err
	}
	if n, rerr := repo.ReleaseExpired(ctx, c.ID, now); rerr != nil {
		return store.HostingCoupon{}, "", rerr
	} else if n > 0 {
		if c, err = repo.Get(ctx, c.ID); err != nil {
			return store.HostingCoupon{}, "", err
		}
	}
	used, err := repo.HasLiveRedemption(ctx, c.ID, keyID)
	if err != nil {
		return store.HostingCoupon{}, "", err
	}
	if used {
		return store.HostingCoupon{}, hosting.CouponAlreadyUsed, nil
	}
	if reject := hosting.CouponUsable(c, now); reject != "" {
		return store.HostingCoupon{}, reject, nil
	}
	return c, "", nil
}

// couponReserveRejection maps a reservation error to a rejection code, or ""
// when err is not a coupon rejection.
func couponReserveRejection(err error) string {
	switch {
	case errors.Is(err, store.ErrCouponExhausted):
		return hosting.CouponExhausted
	case errors.Is(err, store.ErrCouponAlreadyUsed):
		return hosting.CouponAlreadyUsed
	case errors.Is(err, store.ErrCouponInactive):
		return hosting.CouponInactive
	}
	return ""
}

func writeCouponRejection(w http.ResponseWriter, code string) {
	writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{
		"message": hosting.CouponMessage(code), "type": "invalid_request_error", "code": code,
	}})
}

// handlePortalCheckHostingCoupon previews a coupon for the caller: whether
// it is usable and the price it gives. It never takes a coupon use; that
// happens at checkout.
func (s *Server) handlePortalCheckHostingCoupon(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	_, keyID, ok := s.portalKeyID(w, r)
	if !ok {
		return
	}
	s.couponChecksOnce.Do(func() { s.couponChecks = newIPLimiter(couponChecksPerMinute, time.Minute) })
	if !s.couponChecks.Allow(keyID) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many coupon checks; wait a minute")
		return
	}
	code := hosting.NormalizeCouponCode(body.Code)
	if code == "" || len(code) > 64 {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "code": hosting.CouponNotFound, "message": hosting.CouponMessage(hosting.CouponNotFound)})
		return
	}
	st, err := s.loadHostingSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	c, reject, err := s.lookupHostingCoupon(r.Context(), code, keyID, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if reject != "" {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "code": reject, "message": hosting.CouponMessage(reject)})
		return
	}
	q := hosting.QuoteCoupon(st.MonthlyPriceIDR, c, s.hostingMinChargeIDR())
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":          true,
		"coupon_code":    c.Code,
		"kind":           c.Kind,
		"discount_type":  c.DiscountType,
		"discount_value": c.DiscountValue,
		"base_idr":       q.BaseIDR,
		"discount_idr":   q.DiscountIDR,
		"final_idr":      q.FinalIDR,
		"bonus_days":     q.BonusDays,
		"period_days":    hosting.PeriodDays,
		"expires_at":     timeOrEmpty(c.ExpiresAt),
	})
}

// ── Admin ───────────────────────────────────────────────────────────────────

func (s *Server) mountHostingCoupons(r chi.Router) {
	r.Get("/hosting/coupons", s.adminListHostingCoupons)
	r.Post("/hosting/coupons", s.adminCreateHostingCoupon)
	r.Put("/hosting/coupons/{id}", s.adminUpdateHostingCoupon)
	r.Delete("/hosting/coupons/{id}", s.adminDeleteHostingCoupon)
}

func hostingCouponView(c store.HostingCoupon, now time.Time) map[string]any {
	remaining := -1 // unlimited
	if c.MaxUses > 0 {
		remaining = max(c.MaxUses-c.UsedCount-c.HeldCount, 0)
	}
	state := "active"
	switch hosting.CouponUsable(c, now) {
	case hosting.CouponInactive:
		state = "inactive"
	case hosting.CouponNotStarted:
		state = "scheduled"
	case hosting.CouponExpired:
		state = "expired"
	case hosting.CouponExhausted:
		state = "exhausted"
	}
	return map[string]any{
		"id":             c.ID,
		"code":           c.Code,
		"kind":           c.Kind,
		"discount_type":  c.DiscountType,
		"discount_value": c.DiscountValue,
		"bonus_days":     c.BonusDays,
		"max_uses":       c.MaxUses,
		"used_count":     c.UsedCount,
		"held_count":     c.HeldCount,
		"remaining":      remaining,
		"starts_at":      timeOrEmpty(c.StartsAt),
		"expires_at":     timeOrEmpty(c.ExpiresAt),
		"active":         c.Active,
		"state":          state,
		"note":           c.Note,
		"created_at":     c.CreatedAt,
		"updated_at":     c.UpdatedAt,
	}
}

func (s *Server) adminListHostingCoupons(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	// Show true remaining counts: give back holds whose payment lapsed.
	if _, err := s.db.HostingCoupons().ReleaseExpired(r.Context(), "", now); err != nil {
		s.log.Warn("hosting: release lapsed coupon holds failed", "err", err)
	}
	list, err := s.db.HostingCoupons().List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		out = append(out, hostingCouponView(c, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"coupons": out})
}

type hostingCouponRequest struct {
	Code          string `json:"code"`
	Kind          string `json:"kind"`
	DiscountType  string `json:"discount_type"`
	DiscountValue int64  `json:"discount_value"`
	BonusDays     int    `json:"bonus_days"`
	MaxUses       int    `json:"max_uses"`
	StartsAt      string `json:"starts_at"`
	ExpiresAt     string `json:"expires_at"`
	Active        *bool  `json:"active"`
	Note          string `json:"note"`
}

func parseCouponTime(field, v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, errors.New(field + " must be an RFC 3339 time")
	}
	return t.UTC(), nil
}

// couponFromRequest builds and validates a coupon from the request body.
func couponFromRequest(body hostingCouponRequest, c *store.HostingCoupon) error {
	starts, err := parseCouponTime("starts_at", body.StartsAt)
	if err != nil {
		return err
	}
	expires, err := parseCouponTime("expires_at", body.ExpiresAt)
	if err != nil {
		return err
	}
	c.Code, c.Kind, c.DiscountType, c.DiscountValue = body.Code, body.Kind, body.DiscountType, body.DiscountValue
	c.BonusDays, c.MaxUses, c.StartsAt, c.ExpiresAt, c.Note = body.BonusDays, body.MaxUses, starts, expires, body.Note
	if body.Active != nil {
		c.Active = *body.Active
	}
	return hosting.ValidateCoupon(c)
}

func (s *Server) adminCreateHostingCoupon(w http.ResponseWriter, r *http.Request) {
	var body hostingCouponRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	now := time.Now()
	c := store.HostingCoupon{ID: uuid.NewString(), Active: true, CreatedAt: now, UpdatedAt: now}
	if err := couponFromRequest(body, &c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.db.HostingCoupons().Create(r.Context(), c); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "a coupon with this code already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusCreated, hostingCouponView(c, now))
}

func (s *Server) adminUpdateHostingCoupon(w http.ResponseWriter, r *http.Request) {
	var body hostingCouponRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	repo := s.db.HostingCoupons()
	c, err := repo.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "coupon not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if err := couponFromRequest(body, &c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := repo.Update(r.Context(), c); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "a coupon with this code already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	updated, err := repo.Get(r.Context(), c.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, hostingCouponView(updated, time.Now()))
}

func (s *Server) adminDeleteHostingCoupon(w http.ResponseWriter, r *http.Request) {
	err := s.db.HostingCoupons().Delete(r.Context(), chi.URLParam(r, "id"))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "coupon not found")
	case errors.Is(err, store.ErrCouponInUse):
		writeError(w, http.StatusConflict, "this coupon has been used; deactivate it instead of deleting")
	default:
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
	}
}
