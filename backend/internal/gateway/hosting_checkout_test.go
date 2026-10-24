package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/hosting"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

// ageOrder moves an order's creation back by d, as if the customer had left
// the payment page open that long.
func ageOrder(t *testing.T, s *Server, id string, d time.Duration) {
	t.Helper()
	o, err := s.db.PaymentOrders().Get(t.Context(), id)
	require.NoError(t, err)
	_, err = s.db.SQL().ExecContext(t.Context(), `UPDATE payment_orders SET created_at = ?, expires_at = ? WHERE id = ?`,
		o.CreatedAt.Add(-d).UTC().Format(time.RFC3339), "", id)
	require.NoError(t, err)
}

func getPortalHosting(t *testing.T, s *Server, tok string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/portal/api/hosting", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	s.handlePortalHosting(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func decodeOrder(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// An unpaid checkout holds the region's last slot for CheckoutWindow, keeps
// offering the same payment while it is open, and frees the slot after.
func TestHostingCheckout_HoldsSlotForWindow(t *testing.T) {
	s, tok := regionTestServer(t) // Indonesia has one slot
	tok2 := addPortalUser(t, s, "second")

	rec := postHostingOrder(t, s, tok, regionOrder("id"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	first := decodeOrder(t, rec)

	exp, err := time.Parse(time.RFC3339, first["expires_at"].(string))
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(hosting.CheckoutWindow), exp, 5*time.Second,
		"the link is offered for the checkout window, not the gateway's hours")

	// The slot is held: the region shows sold out and another user is refused.
	_, regions := publicHostingJSON(t, s)
	require.EqualValues(t, 0, regions["id"]["stock"])
	rec = postHostingOrder(t, s, tok2, regionOrder("id"))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "region_sold_out")

	// The holder ordering again (or refreshing) gets the same payment, even
	// though the region now looks sold out.
	rec = postHostingOrder(t, s, tok, regionOrder("id"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, first["order_id"], decodeOrder(t, rec)["order_id"])
	inst := getPortalHosting(t, s, tok)["instances"].([]any)[0].(map[string]any)
	require.Equal(t, first["order_id"], inst["pending_order_id"])
	require.Equal(t, first["expires_at"], inst["pending_expires_at"])
	require.NotEmpty(t, inst["pending_payment_link"])

	// After the window the slot is free and the old link is no longer offered.
	ageOrder(t, s, first["order_id"].(string), hosting.CheckoutWindow+time.Second)
	_, regions = publicHostingJSON(t, s)
	require.EqualValues(t, 1, regions["id"]["stock"])
	inst = getPortalHosting(t, s, tok)["instances"].([]any)[0].(map[string]any)
	require.Nil(t, inst["pending_payment_link"])

	// The holder's next order is a new payment for the same instance.
	rec = postHostingOrder(t, s, tok, regionOrder("id"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	second := decodeOrder(t, rec)
	require.NotEqual(t, first["order_id"], second["order_id"])
	require.Equal(t, first["instance_id"], second["instance_id"])

	// The lapsed order is marked expired; a payment that still arrives for it
	// settles as a late payment.
	ids, err := s.db.PaymentOrders().ExpireHostingCheckouts(t.Context(), time.Now().Add(-hosting.CheckoutWindow))
	require.NoError(t, err)
	require.Equal(t, []string{first["order_id"].(string)}, ids)
	old, err := s.db.PaymentOrders().Get(t.Context(), first["order_id"].(string))
	require.NoError(t, err)
	require.Equal(t, store.PaymentExpired, old.Status)
	applied, err := s.markHostingOrderPaid(t.Context(), old, store.PaymentCompleted, "webhook", "")
	require.NoError(t, err)
	require.True(t, applied)
}

// A renewal never needs stock, and its open payment is reused the same way.
func TestHostingCheckout_RenewalReusesOpenPayment(t *testing.T) {
	s, tok := regionTestServer(t)
	rec := postHostingOrder(t, s, tok, regionOrder("eu"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	o, err := s.db.PaymentOrders().Get(t.Context(), decodeOrder(t, rec)["order_id"].(string))
	require.NoError(t, err)
	_, err = s.markHostingOrderPaid(t.Context(), o, store.PaymentCompleted, "webhook", "")
	require.NoError(t, err)

	renew := `{"accept_terms":true,"terms_version":"` + hosting.TermsVersion + `","instance_id":"` + o.HostingInstanceID + `"}`
	rec = postHostingOrder(t, s, tok, renew)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	a := decodeOrder(t, rec)
	rec = postHostingOrder(t, s, tok, renew)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, a["order_id"], decodeOrder(t, rec)["order_id"])
}

func TestHostingCheckout_RejectsLongIdempotencyKey(t *testing.T) {
	s, tok := regionTestServer(t)
	body := `{"accept_terms":true,"terms_version":"` + hosting.TermsVersion + `","region":"eu","idempotency_key":"` +
		strings.Repeat("a", maxIdempotencyKeyLen+1) + `"}`
	rec := postHostingOrder(t, s, tok, body)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

// Buyers racing for a region's last slot: exactly one checkout gets it, the
// rest are told the region is sold out.
func TestHostingCheckout_LastSlotRace(t *testing.T) {
	s, _ := regionTestServer(t) // Indonesia has one slot
	const buyers = 8
	toks := make([]string, buyers)
	for i := range toks {
		toks[i] = addPortalUser(t, s, "racer-"+strconv.Itoa(i))
	}
	codes := make([]int, buyers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range toks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = postHostingOrder(t, s, toks[i], regionOrder("id")).Code
		}(i)
	}
	close(start)
	wg.Wait()
	created, soldOut := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			soldOut++
		}
	}
	require.Equal(t, 1, created, "codes: %v", codes)
	require.Equal(t, buyers-1, soldOut, "codes: %v", codes)
}

// The recount inside the transaction catches a checkout that passed the
// stock check before another buyer's order committed.
func TestHostingCheckout_RecountInsideTransaction(t *testing.T) {
	s, tok := regionTestServer(t)
	rec := postHostingOrder(t, s, tok, regionOrder("id"))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	st, err := hosting.Load(t.Context(), s.settingsGet(), s.financeBox())
	require.NoError(t, err)
	now := time.Now()
	late := store.HostingInstance{ID: "late", TenantID: adminTenant, KeyID: "k-late", Region: "id",
		Status: store.HostingAwaitingPayment, CreatedAt: now, UpdatedAt: now}
	o := store.PaymentOrder{ID: "o-late", TenantID: adminTenant, KeyID: "k-late", AmountIDR: 20000,
		Status: store.PaymentPending, Provider: "sumopod", ProviderPaymentID: "pay-late",
		CreatedAt: now, UpdatedAt: now, Product: store.ProductHosting, HostingInstanceID: "late"}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	err = s.createHostingOrder(req, st, late, true, o, now.Add(time.Minute), "id")
	require.ErrorIs(t, err, errRegionSoldOut)
	_, err = s.db.PaymentOrders().Get(t.Context(), "o-late")
	require.ErrorIs(t, err, store.ErrNotFound, "the losing checkout leaves nothing behind")
}
