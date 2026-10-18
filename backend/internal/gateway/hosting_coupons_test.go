package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/hosting"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

// addPortalUser adds another portal user with its own key and returns its
// session token.
func addPortalUser(t *testing.T, s *Server, sub string) string {
	t.Helper()
	issued, err := s.identity.Create(t.Context(), store.DefaultTenantID, "", "portal-"+sub)
	require.NoError(t, err)
	require.NoError(t, s.db.PortalUsers().Upsert(t.Context(), store.PortalUser{
		GoogleSub: sub, Email: sub + "@example.com", KeyID: issued.Record.ID,
	}))
	tok, err := s.auth.IssuePortalSession("portal:"+sub, sub+"@example.com")
	require.NoError(t, err)
	return tok
}

func createCoupon(t *testing.T, s *Server, body string) map[string]any {
	t.Helper()
	rec := adminHostingCall(s, http.MethodPost, "/hosting/coupons", "", body, s.adminCreateHostingCoupon)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func checkCoupon(t *testing.T, s *Server, tok, code string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/portal/api/hosting/coupons/check", strings.NewReader(`{"code":"`+code+`"}`))
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	s.handlePortalCheckHostingCoupon(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func orderWithCoupon(code string) string {
	return `{"accept_terms":true,"terms_version":"` + hosting.TermsVersion + `","coupon_code":"` + code + `"}`
}

func couponCounts(t *testing.T, s *Server, id string) (used, held int) {
	t.Helper()
	c, err := s.db.HostingCoupons().Get(t.Context(), id)
	require.NoError(t, err)
	return c.UsedCount, c.HeldCount
}

func TestHostingCoupon_DiscountCheckoutAndPay(t *testing.T) {
	s, tok, _ := hostingTestServer(t)
	ctx := t.Context()
	c := createCoupon(t, s, `{"code":"hemat20","kind":"discount","discount_type":"percent","discount_value":20,"max_uses":1}`)
	require.Equal(t, "HEMAT20", c["code"])
	id := c["id"].(string)

	got := checkCoupon(t, s, tok, " Hemat20 ")
	require.Equal(t, true, got["valid"])
	require.EqualValues(t, 40000, got["final_idr"])
	require.EqualValues(t, 10000, got["discount_idr"])
	require.Equal(t, hosting.CouponNotFound, checkCoupon(t, s, tok, "NOPE")["code"])

	// Checking does not take a use.
	used, held := couponCounts(t, s, id)
	require.Zero(t, used+held)

	rec := postHostingOrder(t, s, tok, orderWithCoupon("hemat20"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	order, err := s.db.PaymentOrders().Get(ctx, out["order_id"].(string))
	require.NoError(t, err)
	require.EqualValues(t, 40000, order.AmountIDR)
	require.EqualValues(t, 50000, order.BaseAmountIDR)
	require.Equal(t, "HEMAT20", order.CouponCode)
	used, held = couponCounts(t, s, id)
	require.Equal(t, 0, used)
	require.Equal(t, 1, held)

	// Ordering again with the same coupon reuses the open payment.
	again := postHostingOrder(t, s, tok, orderWithCoupon("HEMAT20"))
	require.Equal(t, http.StatusOK, again.Code, again.Body.String())
	require.Contains(t, again.Body.String(), order.ID)

	// The only use is held, so another user is turned away.
	tok2 := addPortalUser(t, s, "second")
	require.Equal(t, hosting.CouponExhausted, checkCoupon(t, s, tok2, "HEMAT20")["code"])
	rec2 := postHostingOrder(t, s, tok2, orderWithCoupon("HEMAT20"))
	require.Equal(t, http.StatusConflict, rec2.Code, rec2.Body.String())
	require.Contains(t, rec2.Body.String(), hosting.CouponExhausted)

	applied, err := s.markHostingOrderPaid(ctx, order, store.PaymentCompleted, "webhook", "")
	require.NoError(t, err)
	require.True(t, applied)
	used, held = couponCounts(t, s, id)
	require.Equal(t, 1, used)
	require.Equal(t, 0, held)
	require.Equal(t, hosting.CouponAlreadyUsed, checkCoupon(t, s, tok, "HEMAT20")["code"])
}

func TestHostingCoupon_ExpiredPaymentGivesUseBack(t *testing.T) {
	s, tok, _ := hostingTestServer(t)
	s.cfg.Payment.Provider = "sumopod"
	s.cfg.Payment.WebhookToken = "whtok"
	c := createCoupon(t, s, `{"code":"SATU","kind":"discount","discount_type":"fixed","discount_value":5000,"max_uses":1}`)

	rec := postHostingOrder(t, s, tok, orderWithCoupon("SATU"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	order, err := s.db.PaymentOrders().Get(t.Context(), out["order_id"].(string))
	require.NoError(t, err)
	require.EqualValues(t, 45000, order.AmountIDR)

	payload := `{"event_type":"payment.expired","data":{"payment_id":"` + order.ProviderPaymentID + `","order_id":"` + order.ID + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/sumopod", strings.NewReader(payload))
	req.Header.Set("X-Webhook-Token", "whtok")
	wrec := httptest.NewRecorder()
	s.handleSumopodWebhook(wrec, req)
	require.Equal(t, http.StatusOK, wrec.Code, wrec.Body.String())

	used, held := couponCounts(t, s, c["id"].(string))
	require.Zero(t, used)
	require.Zero(t, held)
	tok2 := addPortalUser(t, s, "second")
	require.Equal(t, true, checkCoupon(t, s, tok2, "SATU")["valid"])
}

func TestHostingCoupon_WebhookAmountMustMatch(t *testing.T) {
	s, tok, _ := hostingTestServer(t)
	s.cfg.Payment.Provider = "sumopod"
	s.cfg.Payment.WebhookToken = "whtok"
	createCoupon(t, s, `{"code":"MURAH","kind":"discount","discount_type":"percent","discount_value":50}`)
	rec := postHostingOrder(t, s, tok, orderWithCoupon("MURAH"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	order, err := s.db.PaymentOrders().Get(t.Context(), out["order_id"].(string))
	require.NoError(t, err)

	send := func(amount string) {
		payload := `{"event_type":"payment.completed","data":{"payment_id":"` + order.ProviderPaymentID + `"` + amount + `}}`
		req := httptest.NewRequest(http.MethodPost, "/webhooks/sumopod", strings.NewReader(payload))
		req.Header.Set("X-Webhook-Token", "whtok")
		w := httptest.NewRecorder()
		s.handleSumopodWebhook(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
	send("")                // amount missing: left pending
	send(`,"amount":50000`) // full price on a discounted order: left pending
	got, err := s.db.PaymentOrders().Get(t.Context(), order.ID)
	require.NoError(t, err)
	require.Equal(t, store.PaymentPending, got.Status)
	send(`,"amount":25000`)
	got, err = s.db.PaymentOrders().Get(t.Context(), order.ID)
	require.NoError(t, err)
	require.Equal(t, store.PaymentCompleted, got.Status)
}

func TestHostingCoupon_BonusDays(t *testing.T) {
	s, tok, _ := hostingTestServer(t)
	createCoupon(t, s, `{"code":"BONUS7","kind":"bonus_days","bonus_days":7}`)
	got := checkCoupon(t, s, tok, "bonus7")
	require.Equal(t, true, got["valid"])
	require.EqualValues(t, 50000, got["final_idr"])
	require.EqualValues(t, 7, got["bonus_days"])

	rec := postHostingOrder(t, s, tok, orderWithCoupon("BONUS7"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	order, err := s.db.PaymentOrders().Get(t.Context(), out["order_id"].(string))
	require.NoError(t, err)
	require.EqualValues(t, 50000, order.AmountIDR)
	_, err = s.markHostingOrderPaid(t.Context(), order, store.PaymentCompleted, "webhook", "")
	require.NoError(t, err)
	inst, err := s.db.Hosting().Get(t.Context(), order.HostingInstanceID)
	require.NoError(t, err)
	require.Equal(t, hosting.PeriodDays+7, inst.PrepaidDays)
}

// Parallel checkouts by different users cannot take more uses than allowed.
func TestHostingCoupon_ConcurrentCheckoutRespectsMaxUses(t *testing.T) {
	s, _, _ := hostingTestServer(t)
	c := createCoupon(t, s, `{"code":"REBUTAN","kind":"discount","discount_type":"percent","discount_value":10,"max_uses":2}`)
	const users = 6
	toks := make([]string, users)
	for i := range toks {
		toks[i] = addPortalUser(t, s, "u"+string(rune('a'+i)))
	}
	var wg sync.WaitGroup
	codes := make([]int, users)
	for i, tk := range toks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = postHostingOrder(t, s, tk, orderWithCoupon("REBUTAN")).Code
		}()
	}
	wg.Wait()
	created, conflict := 0, 0
	for _, code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		}
	}
	require.Equal(t, 2, created, "codes: %v", codes)
	require.Equal(t, users-2, conflict, "codes: %v", codes)
	used, held := couponCounts(t, s, c["id"].(string))
	require.Equal(t, 0, used)
	require.Equal(t, 2, held)
}

func TestHostingCoupon_AdminValidationAndDelete(t *testing.T) {
	s, tok, _ := hostingTestServer(t)
	for _, body := range []string{
		`{"code":"X","kind":"discount","discount_type":"percent","discount_value":10}`,    // code too short
		`{"code":"BAD","kind":"discount","discount_type":"percent","discount_value":150}`, // > 100%
		`{"code":"BAD","kind":"discount","discount_type":"fixed","discount_value":0}`,     // zero
		`{"code":"BAD","kind":"bonus_days","bonus_days":0}`,                               // zero days
		`{"code":"BAD","kind":"other"}`,                                                   // unknown kind
		`{"code":"BAD","kind":"bonus_days","bonus_days":3,"expires_at":"tomorrow"}`,       // bad time
		`{"code":"BAD","kind":"bonus_days","bonus_days":3,"max_uses":-1}`,                 // negative
	} {
		rec := adminHostingCall(s, http.MethodPost, "/hosting/coupons", "", body, s.adminCreateHostingCoupon)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
	}
	c := createCoupon(t, s, `{"code":"PROMO","kind":"bonus_days","bonus_days":3}`)
	dup := adminHostingCall(s, http.MethodPost, "/hosting/coupons", "", `{"code":"promo","kind":"bonus_days","bonus_days":3}`, s.adminCreateHostingCoupon)
	require.Equal(t, http.StatusConflict, dup.Code)

	// An expired or deactivated coupon is refused at checkout.
	id := c["id"].(string)
	upd := adminHostingCall(s, http.MethodPut, "/hosting/coupons/"+id, id,
		`{"code":"PROMO","kind":"bonus_days","bonus_days":3,"expires_at":"2020-01-01T00:00:00Z"}`, s.adminUpdateHostingCoupon)
	require.Equal(t, http.StatusOK, upd.Code, upd.Body.String())
	require.Equal(t, hosting.CouponExpired, checkCoupon(t, s, tok, "PROMO")["code"])
	upd = adminHostingCall(s, http.MethodPut, "/hosting/coupons/"+id, id,
		`{"code":"PROMO","kind":"bonus_days","bonus_days":3,"active":false}`, s.adminUpdateHostingCoupon)
	require.Equal(t, http.StatusOK, upd.Code, upd.Body.String())
	rec := postHostingOrder(t, s, tok, orderWithCoupon("PROMO"))
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), hosting.CouponInactive)

	// Re-activate, use it, then delete is refused.
	upd = adminHostingCall(s, http.MethodPut, "/hosting/coupons/"+id, id,
		`{"code":"PROMO","kind":"bonus_days","bonus_days":3,"active":true}`, s.adminUpdateHostingCoupon)
	require.Equal(t, http.StatusOK, upd.Code)
	require.Equal(t, http.StatusCreated, postHostingOrder(t, s, tok, orderWithCoupon("PROMO")).Code)
	del := adminHostingCall(s, http.MethodDelete, "/hosting/coupons/"+id, id, "", s.adminDeleteHostingCoupon)
	require.Equal(t, http.StatusConflict, del.Code)
}

func TestHostingPortal_ShowsSupportURL(t *testing.T) {
	s, tok, _ := hostingTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/portal/api/hosting", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	s.handlePortalHosting(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"support_url":"`+hosting.DefaultSupportURL+`"`)
}
