package gateway

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/mydisha/keirouter/backend/internal/hosting"
	"github.com/mydisha/keirouter/backend/internal/payment"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// hostingTermsPath is the public page users accept at checkout.
const hostingTermsPath = "/ketentuan#hosting"

func (s *Server) loadHostingSettings(r *http.Request) (hosting.Settings, error) {
	return s.loadHostingSettingsCtx(r.Context())
}

func (s *Server) loadHostingSettingsCtx(ctx context.Context) (hosting.Settings, error) {
	return hosting.Load(ctx, s.settingsGet(), s.financeBox())
}

// hostingInstanceView is the portal-safe view of an instance. Dokploy ids,
// node and errors stay admin-only; the region is the customer's own choice.
func hostingInstanceView(st hosting.Settings, h store.HostingInstance, pending *store.PaymentOrder) map[string]any {
	region := st.InstanceRegion(h.Region, h.NodeID)
	regionName := ""
	if rg, ok := st.Region(region); ok {
		regionName = rg.Name
	}
	price, _ := st.PeriodPrice(region)
	v := map[string]any{
		"id":           h.ID,
		"status":       string(h.Status),
		"region":       region,
		"region_name":  regionName,
		"price_idr":    price,
		"url":          "",
		"paid_until":   timeOrEmpty(h.PaidUntil),
		"prepaid_days": h.PrepaidDays,
		"created_at":   h.CreatedAt,
		"has_password": h.Password.Ciphertext != "" && h.Status != store.HostingProvisioning,
	}
	if h.FQDN != "" && (h.Status == store.HostingActive || h.Status == store.HostingExpired) {
		v["url"] = "https://" + h.FQDN
	}
	if pending != nil {
		v["pending_payment_link"] = pending.PaymentLinkURL
		v["pending_order_id"] = pending.ID
	}
	return v
}

// orderRegion resolves the region asked for at checkout: a named region must
// be enabled and priced; an empty one takes the first such region.
func (s *Server) orderRegion(st hosting.Settings, want string) (string, bool) {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, rg := range st.Regions {
		if !rg.Enabled || rg.PriceIDR <= 0 {
			continue
		}
		if want == "" || rg.ID == want {
			return rg.ID, true
		}
	}
	return "", false
}

func writeHostingRegionError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"message": msg, "type": "invalid_request_error", "code": code,
	}})
}

func timeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// handlePortalHosting returns the hosting offer and the caller's instances.
func (s *Server) handlePortalHosting(w http.ResponseWriter, r *http.Request) {
	_, keyID, ok := s.portalKeyID(w, r)
	if !ok {
		return
	}
	st, err := s.loadHostingSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	instances, err := s.db.Hosting().ListByKey(r.Context(), keyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	orders, err := s.db.PaymentOrders().ListByKey(r.Context(), keyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	pending := latestPendingHostingOrders(orders, time.Now(), s.instancePrices(st, instances), nil)
	out := make([]map[string]any, 0, len(instances))
	for _, h := range instances {
		var p *store.PaymentOrder
		if o, ok := pending[h.ID]; ok {
			p = &o
		}
		out = append(out, hostingInstanceView(st, h, p))
	}
	open := st.Enabled && s.cfg.Payment.Enabled && s.paymentClient != nil
	regions := []map[string]any{}
	if st.Enabled {
		stocks, serr := s.hostingRegionStocks(r.Context(), st)
		if serr != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, serr, "internal server error"))
			return
		}
		for _, rs := range stocks {
			if rs.Enabled && rs.PriceIDR > 0 {
				regions = append(regions, publicRegionView(rs, open))
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":       open,
		"regions":       regions,
		"period_days":   hosting.PeriodDays,
		"terms_version": hosting.TermsVersion,
		"terms_url":     hostingTermsPath,
		"support_url":   st.SupportURL,
		"instances":     out,
	})
}

// instancePrices maps each instance to the current price of its region.
func (s *Server) instancePrices(st hosting.Settings, instances []store.HostingInstance) map[string]int64 {
	out := make(map[string]int64, len(instances))
	for _, h := range instances {
		if p, ok := st.PeriodPrice(st.InstanceRegion(h.Region, h.NodeID)); ok {
			out[h.ID] = p
		}
	}
	return out
}

// latestPendingHostingOrders maps instance id to its newest unexpired
// pending hosting order, so the portal can offer "continue payment".
// prices holds each instance's current base price; orders at another price
// are skipped so a price change cannot be dodged. A non-nil coupon keeps
// only orders made with that coupon code ("" = no coupon), so a reused
// payment always matches what was asked for.
func latestPendingHostingOrders(orders []store.PaymentOrder, now time.Time, prices map[string]int64, coupon *string) map[string]store.PaymentOrder {
	out := map[string]store.PaymentOrder{}
	for _, o := range orders { // newest first
		base := o.BaseAmountIDR
		if base == 0 {
			base = o.AmountIDR
		}
		price, priced := prices[o.HostingInstanceID]
		if o.Product != store.ProductHosting || o.Status != store.PaymentPending || o.HostingInstanceID == "" || !priced || base != price {
			continue
		}
		if coupon != nil && o.CouponCode != *coupon {
			continue
		}
		if o.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, o.ExpiresAt); err == nil && t.Before(now) {
				continue
			}
		}
		if _, seen := out[o.HostingInstanceID]; !seen {
			out[o.HostingInstanceID] = o
		}
	}
	return out
}

type createHostingOrderRequest struct {
	InstanceID     string `json:"instance_id"`
	AcceptTerms    bool   `json:"accept_terms"`
	TermsVersion   string `json:"terms_version"`
	IdempotencyKey string `json:"idempotency_key"`
	TurnstileToken string `json:"turnstile_token"`
	CouponCode     string `json:"coupon_code"`
	// Region is where a new instance runs; empty takes the first region
	// on sale. Renewals keep the instance's region.
	Region string `json:"region"`
}

// handlePortalCreateHostingOrder creates a SumoPod payment for one month of
// hosting: a new instance, or a renewal when instance_id is set. The caller
// must accept the current hosting terms version.
func (s *Server) handlePortalCreateHostingOrder(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Payment.Enabled || s.paymentClient == nil {
		writeError(w, http.StatusServiceUnavailable, "payments are not enabled")
		return
	}
	var body createHostingOrderRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if !body.AcceptTerms {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "you must accept the hosting terms", "type": "invalid_request_error", "code": "terms_required",
		}})
		return
	}
	if body.TermsVersion != hosting.TermsVersion {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{
			"message": "the hosting terms have changed; review and accept them again", "type": "invalid_request_error",
			"code": "terms_outdated", "terms_version": hosting.TermsVersion,
		}})
		return
	}
	st, err := s.loadHostingSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if !st.Enabled {
		writeError(w, http.StatusServiceUnavailable, "hosting is not available")
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
	couponCode := hosting.NormalizeCouponCode(body.CouponCode)

	// One checkout at a time per key: the unpaid-instance reuse and the
	// coupon hold below are read-then-write.
	unlock := s.lockHostingOrders(keyID)
	defer unlock()

	idem := strings.TrimSpace(body.IdempotencyKey)
	if idem == "" {
		idem = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	if idem != "" {
		if existing, gerr := s.db.PaymentOrders().GetByIdempotencyKey(r.Context(), idem); gerr == nil {
			if existing.KeyID != keyID || existing.Product != store.ProductHosting {
				writeError(w, http.StatusConflict, "idempotency key already used")
				return
			}
			s.writeHostingOrderResponse(w, http.StatusOK, existing)
			return
		} else if !errors.Is(gerr, store.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, gerr, "internal server error"))
			return
		}
	}

	// Pick the instance: the one named for a renewal, else reuse an unpaid
	// one in the same region so repeated "Order" clicks do not pile up
	// empty instances.
	var inst store.HostingInstance
	newInstance := false
	if id := strings.TrimSpace(body.InstanceID); id != "" {
		inst, err = s.db.Hosting().Get(r.Context(), id)
		if err != nil || inst.KeyID != keyID || inst.Status == store.HostingDeleted {
			writeError(w, http.StatusNotFound, "instance not found")
			return
		}
	} else {
		region, ok := s.orderRegion(st, body.Region)
		if !ok {
			writeHostingRegionError(w, http.StatusBadRequest, "region_unavailable", "Region ini tidak tersedia.")
			return
		}
		list, lerr := s.db.Hosting().ListByKey(r.Context(), keyID)
		if lerr != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, lerr, "internal server error"))
			return
		}
		for _, h := range list {
			if h.Status == store.HostingAwaitingPayment && h.Region == region {
				inst = h
				break
			}
		}
		if inst.ID == "" {
			now := time.Now()
			inst = store.HostingInstance{
				ID: uuid.NewString(), TenantID: adminTenant, KeyID: keyID, Region: region,
				Status: store.HostingAwaitingPayment, CreatedAt: now, UpdatedAt: now,
			}
			newInstance = true
		}
	}
	region := st.InstanceRegion(inst.Region, inst.NodeID)
	price, priced := st.PeriodPrice(region)
	if !priced {
		writeError(w, http.StatusServiceUnavailable, "hosting is not available for this region")
		return
	}
	// An instance nobody has paid for yet takes a new slot, so its region
	// must have stock. Renewals of paid instances do not.
	if inst.Status == store.HostingAwaitingPayment {
		rs, known, serr := s.hostingRegionStock(r.Context(), st, region)
		if serr != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, serr, "internal server error"))
			return
		}
		if region != "" && (!known || rs.SoldOut) {
			writeHostingRegionError(w, http.StatusConflict, "region_sold_out", "Stok region ini sedang habis.")
			return
		}
	}

	// Reuse an open payment for this instance instead of creating a new
	// SumoPod payment on every click.
	if !newInstance {
		orders, oerr := s.db.PaymentOrders().ListByKey(r.Context(), keyID)
		if oerr != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, oerr, "internal server error"))
			return
		}
		if open, ok := latestPendingHostingOrders(orders, time.Now(), map[string]int64{inst.ID: price}, &couponCode)[inst.ID]; ok {
			s.writeHostingOrderResponse(w, http.StatusOK, open)
			return
		}
	}

	quote := hosting.Quote{BaseIDR: price, FinalIDR: price}
	var coupon store.HostingCoupon
	if couponCode != "" {
		c, reject, cerr := s.lookupHostingCoupon(r.Context(), couponCode, keyID, time.Now())
		if cerr != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, cerr, "internal server error"))
			return
		}
		if reject != "" {
			writeCouponRejection(w, reject)
			return
		}
		coupon = c
		quote = hosting.QuoteCoupon(price, c, s.hostingMinChargeIDR())
	}

	p := s.cfg.Payment
	orderID := uuid.NewString()
	base := s.publicBaseURL(r)
	created, err := s.paymentClient.CreatePayment(r.Context(), payment.CreateRequest{
		OrderID: orderID, AmountIDR: quote.FinalIDR, Currency: "IDR",
		ExpiresInHours:        p.ExpiresInHours,
		SuccessReturnURL:      base + "/portal/hosting?status=success",
		CancelReturnURL:       base + "/portal/hosting?status=cancel",
		PaymentMethodTypeCode: p.PaymentMethodTypeCode,
	})
	if err != nil {
		s.log.Error("hosting: create payment failed", "err", err)
		writeError(w, http.StatusBadGateway, "failed to create payment")
		return
	}

	now := time.Now()
	o := store.PaymentOrder{
		ID: orderID, TenantID: adminTenant, KeyID: keyID, GoogleSub: u.GoogleSub,
		AmountIDR: quote.FinalIDR, Status: store.PaymentPending, Provider: p.Provider,
		ProviderPaymentID: created.PaymentID, PaymentLinkURL: created.PaymentLinkURL,
		IdempotencyKey: idem, Actor: "portal", CreatedAt: now, UpdatedAt: now,
		ExpiresAt: created.ExpiresAt, Product: store.ProductHosting,
		HostingInstanceID: inst.ID, TermsVersion: hosting.TermsVersion,
		TermsAcceptedAt: now.UTC().Format(time.RFC3339),
		CouponID:        coupon.ID, CouponCode: coupon.Code, BonusDays: quote.BonusDays,
	}
	if coupon.ID != "" {
		o.BaseAmountIDR = quote.BaseIDR
	}
	if err := s.createHostingOrder(r, inst, newInstance, o, s.couponHoldUntil(created.ExpiresAt, now)); err != nil {
		if reject := couponReserveRejection(err); reject != "" {
			writeCouponRejection(w, reject)
			return
		}
		if idem != "" {
			if existing, gerr := s.db.PaymentOrders().GetByIdempotencyKey(r.Context(), idem); gerr == nil && existing.KeyID == keyID {
				s.writeHostingOrderResponse(w, http.StatusOK, existing)
				return
			}
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	s.writeHostingOrderResponse(w, http.StatusCreated, o)
}

// createHostingOrder stores the order (and a new instance) and, for a coupon
// order, takes one coupon use until holdUntil, all in one transaction.
func (s *Server) createHostingOrder(r *http.Request, inst store.HostingInstance, newInstance bool, o store.PaymentOrder, holdUntil time.Time) error {
	tx, err := s.db.SQL().BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if newInstance {
		if err := s.db.Hosting().CreateOnTx(r.Context(), tx, inst); err != nil {
			return err
		}
	}
	if err := s.db.PaymentOrders().CreateOnTx(r.Context(), tx, o); err != nil {
		return err
	}
	if o.CouponID != "" {
		if err := s.db.HostingCoupons().ReserveOnTx(r.Context(), tx, o.CouponID, o.KeyID, o.ID, holdUntil, o.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Server) writeHostingOrderResponse(w http.ResponseWriter, status int, o store.PaymentOrder) {
	writeJSON(w, status, map[string]any{
		"order_id":         o.ID,
		"instance_id":      o.HostingInstanceID,
		"status":           string(o.Status),
		"amount_idr":       o.AmountIDR,
		"payment_link_url": o.PaymentLinkURL,
		"expires_at":       o.ExpiresAt,
		"terms_version":    o.TermsVersion,
		"created_at":       o.CreatedAt,
		"coupon_code":      o.CouponCode,
		"base_amount_idr":  o.BaseAmountIDR,
		"bonus_days":       o.BonusDays,
	})
}

// markHostingOrderPaid settles a hosting order exactly once: the guarded
// transition out of a settleable status must win, then the instance gains one period. An
// instance that is not created yet banks the days in prepaid_days so its
// clock starts when the admin creates it. A payment for a deleted instance
// is recorded with reason refund_required and grants nothing.
func (s *Server) markHostingOrderPaid(ctx context.Context, order store.PaymentOrder, to store.PaymentOrderStatus, actor, reason string) (bool, error) {
	if !order.Status.Settleable() {
		return false, nil
	}
	reason = latePaymentReason(order.Status, reason)
	if order.HostingInstanceID == "" {
		return false, errors.New("hosting: order has no instance")
	}
	for attempt := 0; ; attempt++ {
		applied, err := s.markHostingOrderPaidOnce(ctx, order, to, actor, reason)
		// Another payment changed the instance first: retry the whole
		// settlement in a fresh transaction.
		if errors.Is(err, store.ErrHostingConflict) && attempt < 4 {
			continue
		}
		return applied, err
	}
}

func (s *Server) markHostingOrderPaidOnce(ctx context.Context, order store.PaymentOrder, to store.PaymentOrderStatus, actor, reason string) (bool, error) {
	tx, err := s.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	inst, err := s.db.Hosting().GetOnTx(ctx, tx, order.HostingInstanceID)
	if err != nil {
		return false, err
	}
	if inst.Status == store.HostingDeleted {
		reason = strings.TrimSpace("refund_required: instance was deleted before payment " + reason)
		s.log.Error("hosting: payment for a deleted instance; refund it", "order", order.ID, "instance", inst.ID)
	}
	now := time.Now()
	applied, err := s.db.PaymentOrders().TransitionFromOnTx(ctx, tx, order.ID, store.PaymentSettleable, to, actor, reason, now.UTC().Format(time.RFC3339))
	if err != nil || !applied {
		return false, err
	}
	if inst.Status != store.HostingDeleted {
		if err := s.applyHostingPeriod(ctx, tx, inst, now, hosting.PeriodDays+order.BonusDays); err != nil {
			return false, err
		}
	}
	if order.CouponID != "" {
		coupons := s.db.HostingCoupons()
		if inst.Status == store.HostingDeleted {
			// The payment is refunded, so the coupon use goes back too.
			if _, err := coupons.ReleaseOnTx(ctx, tx, order.ID, now); err != nil {
				return false, err
			}
		} else if counted, err := coupons.ConsumeOnTx(ctx, tx, order.ID, now); err != nil {
			return false, err
		} else if !counted {
			s.log.Warn("hosting: paid coupon order could not be counted against the coupon",
				"order", order.ID, "coupon", order.CouponCode)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// applyHostingPeriod adds days (one period plus any coupon bonus) to the
// instance.
func (s *Server) applyHostingPeriod(ctx context.Context, tx *sql.Tx, inst store.HostingInstance, now time.Time, days int) error {
	repo := s.db.Hosting()
	if inst.ProvisionedAt.IsZero() {
		if err := repo.ApplyPaymentOnTx(ctx, tx, inst, time.Time{}, days); err != nil {
			return err
		}
		if inst.Status == store.HostingAwaitingPayment {
			if _, err := repo.TransitionOnTx(ctx, tx, inst.ID, []store.HostingStatus{store.HostingAwaitingPayment}, store.HostingPaid, ""); err != nil {
				return err
			}
		}
		return nil
	}
	// Extend from the old end date while it is within the grace window, so
	// running past the end and paying late does not get the gap for free.
	base := now
	if inst.PaidUntil.AddDate(0, 0, hosting.GraceDays).After(now) {
		base = inst.PaidUntil
	}
	if err := repo.ApplyPaymentOnTx(ctx, tx, inst, base.AddDate(0, 0, days), 0); err != nil {
		return err
	}
	if inst.Status == store.HostingExpired {
		if _, err := repo.TransitionOnTx(ctx, tx, inst.ID, []store.HostingStatus{store.HostingExpired}, store.HostingActive, ""); err != nil {
			return err
		}
	}
	return nil
}

// handlePortalHostingPassword reveals the initial dashboard password of an
// instance the caller owns.
func (s *Server) handlePortalHostingPassword(w http.ResponseWriter, r *http.Request) {
	_, keyID, ok := s.portalKeyID(w, r)
	if !ok {
		return
	}
	h, err := s.db.Hosting().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil || h.KeyID != keyID || h.Status == store.HostingDeleted ||
		h.Status == store.HostingProvisioning || h.Password.Ciphertext == "" {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "password unavailable")
		return
	}
	pw, err := s.vault.Sealer().OpenString(h.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"password": pw})
}
