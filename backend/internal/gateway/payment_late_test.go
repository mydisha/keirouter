package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/mydisha/keirouter/backend/internal/hosting"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func sendWebhook(t *testing.T, s *Server, event, paymentID, extra string) {
	t.Helper()
	payload := `{"event_type":"` + event + `","data":{"payment_id":"` + paymentID + `"` + extra + `}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/sumopod", strings.NewReader(payload))
	req.Header.Set("X-Webhook-Token", "whtok")
	rec := httptest.NewRecorder()
	s.handleSumopodWebhook(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func approveOrder(s *Server, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/payments/orders/"+id+"/approve", strings.NewReader(`{"reason":"checked in SumoPod"}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.adminApprovePaymentOrder(rec, req)
	return rec
}

// A customer who pays just as the link expires must still get the month,
// and the coupon use released on expiry is counted again.
func TestHosting_PaymentAfterExpirySettles(t *testing.T) {
	s, tok, _ := hostingTestServer(t)
	s.cfg.Payment.Provider = "sumopod"
	s.cfg.Payment.WebhookToken = "whtok"
	c := createCoupon(t, s, `{"code":"TELAT","kind":"discount","discount_type":"fixed","discount_value":5000,"max_uses":1}`)

	rec := postHostingOrder(t, s, tok, orderWithCoupon("TELAT"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	order, err := s.db.PaymentOrders().Get(t.Context(), out["order_id"].(string))
	require.NoError(t, err)

	sendWebhook(t, s, "payment.expired", order.ProviderPaymentID, "")
	got, err := s.db.PaymentOrders().Get(t.Context(), order.ID)
	require.NoError(t, err)
	require.Equal(t, store.PaymentExpired, got.Status)

	// A wrong amount is still refused after expiry.
	sendWebhook(t, s, "payment.completed", order.ProviderPaymentID, `,"amount":50000`)
	got, err = s.db.PaymentOrders().Get(t.Context(), order.ID)
	require.NoError(t, err)
	require.Equal(t, store.PaymentExpired, got.Status)

	sendWebhook(t, s, "payment.completed", order.ProviderPaymentID, `,"amount":45000`)
	got, err = s.db.PaymentOrders().Get(t.Context(), order.ID)
	require.NoError(t, err)
	require.Equal(t, store.PaymentCompleted, got.Status)

	inst, err := s.db.Hosting().Get(t.Context(), order.HostingInstanceID)
	require.NoError(t, err)
	require.Equal(t, store.HostingPaid, inst.Status)
	require.Equal(t, hosting.PeriodDays, inst.PrepaidDays)

	used, held := couponCounts(t, s, c["id"].(string))
	require.Equal(t, 1, used)
	require.Zero(t, held)

	// Redelivery and a stale "expired" event change nothing.
	sendWebhook(t, s, "payment.completed", order.ProviderPaymentID, `,"amount":45000`)
	sendWebhook(t, s, "payment.expired", order.ProviderPaymentID, "")
	got, err = s.db.PaymentOrders().Get(t.Context(), order.ID)
	require.NoError(t, err)
	require.Equal(t, store.PaymentCompleted, got.Status)
	inst, err = s.db.Hosting().Get(t.Context(), order.HostingInstanceID)
	require.NoError(t, err)
	require.Equal(t, hosting.PeriodDays, inst.PrepaidDays)
}

// After checking SumoPod, an admin can approve an order the gateway marked
// expired.
func TestHosting_AdminApprovesExpiredOrder(t *testing.T) {
	s, tok, _ := hostingTestServer(t)
	s.cfg.Payment.Provider = "sumopod"
	s.cfg.Payment.WebhookToken = "whtok"
	rec := postHostingOrder(t, s, tok, orderWithCoupon(""))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	order, err := s.db.PaymentOrders().Get(t.Context(), out["order_id"].(string))
	require.NoError(t, err)

	sendWebhook(t, s, "payment.expired", order.ProviderPaymentID, "")
	require.Equal(t, http.StatusOK, approveOrder(s, order.ID).Code)
	require.Equal(t, http.StatusConflict, approveOrder(s, order.ID).Code)

	inst, err := s.db.Hosting().Get(t.Context(), order.HostingInstanceID)
	require.NoError(t, err)
	require.Equal(t, store.HostingPaid, inst.Status)
}

// Top-up credit has the same late-payment case.
func TestTopup_PaymentAfterExpiryCredits(t *testing.T) {
	s := newPaymentTestServer(t)
	s.cfg.Payment.WebhookToken = "whtok"
	key, err := s.identity.Create(t.Context(), store.DefaultTenantID, "", "payer")
	require.NoError(t, err)
	o := makePaidOrder(t, s, key.Record.ID, "late1")

	sendWebhook(t, s, "payment.expired", o.ProviderPaymentID, "")
	sendWebhook(t, s, "payment.completed", o.ProviderPaymentID, `,"amount":16000`)
	sendWebhook(t, s, "payment.completed", o.ProviderPaymentID, `,"amount":16000`)

	got, err := s.db.PaymentOrders().Get(t.Context(), o.ID)
	require.NoError(t, err)
	require.Equal(t, store.PaymentCompleted, got.Status)
	b, err := s.budgets.ListByScope(t.Context(), store.ScopeAPIKey, key.Record.ID)
	require.NoError(t, err)
	require.Len(t, b, 1)
	require.EqualValues(t, 1_000_000, b[0].LimitMicros)
}
