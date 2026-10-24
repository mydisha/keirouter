package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mydisha/keirouter/backend/internal/cloudflare"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/dokploy"
	"github.com/mydisha/keirouter/backend/internal/hosting"
	"github.com/mydisha/keirouter/backend/internal/payment"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
	"github.com/stretchr/testify/require"
)

type fakeDokploy struct {
	creates, domains, deploys, stops, deletes int
	failDomainOnce                            bool
	lastImage, lastHost, lastEnv, lastServer  string
	lastMem, lastCPU, lastMount               string
	mounts                                    int
}

func (f *fakeDokploy) CreateVolumeMount(_ context.Context, _, vol, path string) error {
	f.mounts++
	f.lastMount = vol + ":" + path
	return nil
}

func (f *fakeDokploy) SetResources(_ context.Context, _, mem, cpu string) error {
	f.lastMem, f.lastCPU = mem, cpu
	return nil
}

func (f *fakeDokploy) CreateApplication(_ context.Context, name, appName, env, server string) (dokploy.Application, error) {
	f.creates++
	f.lastServer = server
	return dokploy.Application{ApplicationID: "app-1", AppName: appName}, nil
}
func (f *fakeDokploy) SaveDockerProvider(_ context.Context, _, image string) error {
	f.lastImage = image
	return nil
}
func (f *fakeDokploy) SaveEnvironment(_ context.Context, _, env string) error {
	f.lastEnv = env
	return nil
}
func (f *fakeDokploy) CreateDomain(_ context.Context, _, host string, _ int) (dokploy.Domain, error) {
	if f.failDomainOnce {
		f.failDomainOnce = false
		return dokploy.Domain{}, errors.New("boom")
	}
	f.domains++
	f.lastHost = host
	return dokploy.Domain{DomainID: "dom-1", Host: host}, nil
}
func (f *fakeDokploy) Deploy(context.Context, string) error           { f.deploys++; return nil }
func (f *fakeDokploy) Stop(context.Context, string) error             { f.stops++; return nil }
func (f *fakeDokploy) Start(context.Context, string) error            { return nil }
func (f *fakeDokploy) Delete(context.Context, string) error           { f.deletes++; return nil }
func (f *fakeDokploy) CheckEnvironment(context.Context, string) error { return nil }

func (f *fakeDokploy) ListServers(context.Context) ([]dokploy.Server, error) {
	return []dokploy.Server{{ServerID: "srv-1", Name: "Jakarta 1", IPAddress: "203.0.113.10", Status: "active"}}, nil
}

// fakeDNS is an in-memory Cloudflare zone.
type fakeDNS struct {
	zoneName         string
	failZone         error
	failDelete       error
	records          map[string]cloudflare.Record // by name
	creates, deletes int
	lastComment      string
}

func (f *fakeDNS) Zone(context.Context, string) (string, error) {
	if f.failZone != nil {
		return "", f.failZone
	}
	return f.zoneName, nil
}

func (f *fakeDNS) FindA(_ context.Context, _, name string) (cloudflare.Record, bool, error) {
	r, ok := f.records[name]
	return r, ok, nil
}

func (f *fakeDNS) CreateA(_ context.Context, _, name, ip, comment string) (cloudflare.Record, error) {
	f.creates++
	f.lastComment = comment
	r := cloudflare.Record{ID: fmt.Sprintf("rec-%d", f.creates), Name: name, Content: ip}
	f.records[name] = r
	return r, nil
}

func (f *fakeDNS) Delete(_ context.Context, _, id string) error {
	if f.failDelete != nil {
		return f.failDelete
	}
	f.deletes++
	for name, r := range f.records {
		if r.ID == id {
			delete(f.records, name)
		}
	}
	return nil
}

// hostingTestServer is a portal server with payments, a sealer, enabled
// hosting settings, a fake SumoPod and a fake Dokploy.
func hostingTestServer(t *testing.T) (*Server, string, *fakeDokploy) {
	t.Helper()
	s, tok, fake, _ := hostingTestServerDNS(t)
	return s, tok, fake
}

// hostingTestServerDNS is hostingTestServer that also returns the fake
// Cloudflare zone (host.example.com).
func hostingTestServerDNS(t *testing.T) (*Server, string, *fakeDokploy, *fakeDNS) {
	t.Helper()
	s, tok := paymentPortalTestServer(t)
	s.settings = s.db.Settings()
	mk, err := crypto.GenerateMasterKey()
	require.NoError(t, err)
	sealer, err := crypto.NewSealer(mk)
	require.NoError(t, err)
	s.vault = vault.New(sealer)

	n := 0
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"payment_id": "pay-" + string(rune('a'+n)), "payment_link_url": "https://pay.example/x",
		})
	}))
	t.Cleanup(gw.Close)
	s.paymentClient = payment.NewClient(gw.URL, "k")

	fake := &fakeDokploy{}
	s.dokployFactory = func(string, string) dokployAPI { return fake }
	dns := &fakeDNS{zoneName: "host.example.com", records: map[string]cloudflare.Record{}}
	s.dnsFactory = func(string) dnsAPI { return dns }
	require.NoError(t, hosting.Save(t.Context(), s.settingsSet(), s.financeBox(), hosting.Settings{
		Enabled: true, DokployURL: "https://dokploy.example.com",
		APIKey: "dk", EnvironmentID: "env-1",
		RootDomain: "host.example.com", CloudflareZoneID: "z1", CloudflareToken: "cf",
		Nodes: []hosting.Node{{ID: "default", Name: "Node 1", ServerID: "srv-1",
			PublicIP: "203.0.113.7", Enabled: true, Region: "id"}},
		Regions: []hosting.Region{{ID: "id", Name: "Indonesia", PriceIDR: 50000, Enabled: true}},
		Images:  []string{"luqmenul/9router-go:latest"}, ContainerPort: 20130, DataPath: "/data",
		EnvTemplate: "BASE_URL={{URL}}\nINITIAL_PASSWORD=123456",
		PasswordEnv: "INITIAL_PASSWORD", MemoryLimit: "536870912", CPULimit: "1000000000",
	}))
	return s, tok, fake, dns
}

func postHostingOrder(t *testing.T, s *Server, tok, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/portal/api/hosting/orders", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	s.handlePortalCreateHostingOrder(rec, req)
	return rec
}

func adminHostingCall(s *Server, method, path, id, body string, h http.HandlerFunc) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestHostingOrder_RequiresCurrentTerms(t *testing.T) {
	s, tok, _ := hostingTestServer(t)

	rec := postHostingOrder(t, s, tok, `{"accept_terms":false,"terms_version":"`+hosting.TermsVersion+`"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "terms_required")

	rec = postHostingOrder(t, s, tok, `{"accept_terms":true,"terms_version":"2000-01-01"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "terms_outdated")

	rec = postHostingOrder(t, s, tok, `{"accept_terms":true,"terms_version":"`+hosting.TermsVersion+`"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	o, err := s.db.PaymentOrders().Get(t.Context(), out["order_id"].(string))
	require.NoError(t, err)
	require.Equal(t, store.ProductHosting, o.Product)
	require.Equal(t, hosting.TermsVersion, o.TermsVersion)
	require.NotEmpty(t, o.TermsAcceptedAt)
	require.EqualValues(t, 50000, o.AmountIDR)
	require.Zero(t, o.CreditMicros)

	// A second order without instance_id reuses the unpaid instance and its
	// open payment.
	rec = postHostingOrder(t, s, tok, `{"accept_terms":true,"terms_version":"`+hosting.TermsVersion+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	list, err := s.db.Hosting().ListByKey(t.Context(), o.KeyID)
	require.NoError(t, err)
	require.Len(t, list, 1)

	// Hosting orders stay off the top-up order list.
	req := httptest.NewRequest(http.MethodGet, "/portal/api/topup/orders", nil)
	req.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	lrec := httptest.NewRecorder()
	s.handlePortalListOrders(lrec, req)
	require.Equal(t, http.StatusOK, lrec.Code)
	require.NotContains(t, lrec.Body.String(), o.ID)
}

func TestHosting_PayProvisionRenew(t *testing.T) {
	s, tok, fake := hostingTestServer(t)
	ctx := t.Context()

	rec := postHostingOrder(t, s, tok, `{"accept_terms":true,"terms_version":"`+hosting.TermsVersion+`"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	order, err := s.db.PaymentOrders().Get(ctx, out["order_id"].(string))
	require.NoError(t, err)
	instID := order.HostingInstanceID

	// Provisioning before payment is refused.
	prov := func() *httptest.ResponseRecorder {
		return adminHostingCall(s, http.MethodPost, "/hosting/instances/"+instID+"/provision", instID,
			`{"image":"luqmenul/9router-go:latest"}`, s.adminProvisionHosting)
	}
	require.Equal(t, http.StatusConflict, prov().Code)

	// The credit path refuses hosting orders.
	_, _, _, cerr := s.creditPaymentOrder(ctx, order, store.PaymentCompleted, "test", "")
	require.Error(t, cerr)

	// Paying twice applies once and banks 30 days.
	applied, err := s.markHostingOrderPaid(ctx, order, store.PaymentCompleted, "webhook", "")
	require.NoError(t, err)
	require.True(t, applied)
	applied, err = s.markHostingOrderPaid(ctx, order, store.PaymentCompleted, "webhook", "")
	require.NoError(t, err)
	require.False(t, applied)
	inst, err := s.db.Hosting().Get(ctx, instID)
	require.NoError(t, err)
	require.Equal(t, store.HostingPaid, inst.Status)
	require.Equal(t, hosting.PeriodDays, inst.PrepaidDays)
	require.True(t, inst.PaidUntil.IsZero())

	// An image outside the allowed list is refused.
	bad := adminHostingCall(s, http.MethodPost, "/x", instID, `{"image":"evil:latest"}`, s.adminProvisionHosting)
	require.Equal(t, http.StatusBadRequest, bad.Code)

	// First attempt fails at the domain step and records the error.
	fake.failDomainOnce = true
	rec = prov()
	require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
	inst, err = s.db.Hosting().Get(ctx, instID)
	require.NoError(t, err)
	require.Equal(t, store.HostingFailed, inst.Status)
	require.Contains(t, inst.LastError, "create domain")
	require.Equal(t, "app-1", inst.DokployAppID)

	// Retry resumes without creating a second application.
	rec = prov()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, fake.creates)
	require.Equal(t, 1, fake.domains)
	require.Equal(t, 1, fake.deploys)
	inst, err = s.db.Hosting().Get(ctx, instID)
	require.NoError(t, err)
	require.Equal(t, store.HostingActive, inst.Status)
	require.Equal(t, inst.Subdomain+".host.example.com", inst.FQDN)
	require.Equal(t, inst.FQDN, fake.lastHost)
	// The template's fixed password is replaced by the instance's own.
	pw, err := s.vault.Sealer().OpenString(inst.Password)
	require.NoError(t, err)
	require.Len(t, pw, 20)
	require.Equal(t, "BASE_URL=https://"+inst.FQDN+"\nSTRICT_SSL=true\nINITIAL_PASSWORD="+pw+"\nPORT=20130\nDATA_DIR=/data", fake.lastEnv)
	// The data volume is mounted once even though the first attempt failed.
	require.Equal(t, 1, fake.mounts)
	require.Equal(t, "nr-"+inst.Subdomain+"-data:/data", fake.lastMount)
	require.Equal(t, "nr-"+inst.Subdomain+"-data", inst.DataVolume)
	require.NotContains(t, fake.lastEnv, "123456")
	require.Equal(t, "srv-1", fake.lastServer)
	require.Equal(t, "536870912", fake.lastMem)

	// The owner can read the password; another portal user cannot.
	preq := httptest.NewRequest(http.MethodGet, "/x", nil)
	preq.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: tok})
	prctx := chi.NewRouteContext()
	prctx.URLParams.Add("id", instID)
	preq = preq.WithContext(context.WithValue(preq.Context(), chi.RouteCtxKey, prctx))
	prec := httptest.NewRecorder()
	s.handlePortalHostingPassword(prec, preq)
	require.Equal(t, http.StatusOK, prec.Code, prec.Body.String())
	require.Contains(t, prec.Body.String(), pw)

	other, err := s.identity.Create(ctx, store.DefaultTenantID, "", "other-key")
	require.NoError(t, err)
	require.NoError(t, s.db.PortalUsers().Upsert(ctx, store.PortalUser{GoogleSub: "gsub2", Email: "o@example.com", KeyID: other.Record.ID}))
	otok, err := s.auth.IssuePortalSession("portal:gsub2", "o@example.com")
	require.NoError(t, err)
	oreq := httptest.NewRequest(http.MethodGet, "/x", nil)
	oreq.AddCookie(&http.Cookie{Name: portalSessionCookie, Value: otok})
	oreq = oreq.WithContext(context.WithValue(oreq.Context(), chi.RouteCtxKey, prctx))
	orec := httptest.NewRecorder()
	s.handlePortalHostingPassword(orec, oreq)
	require.Equal(t, http.StatusNotFound, orec.Code)
	// Nor renew it.
	require.Equal(t, http.StatusNotFound, postHostingOrder(t, s, otok,
		`{"instance_id":"`+instID+`","accept_terms":true,"terms_version":"`+hosting.TermsVersion+`"}`).Code)
	require.Equal(t, "luqmenul/9router-go:latest", fake.lastImage)
	require.Zero(t, inst.PrepaidDays)
	require.WithinDuration(t, time.Now().AddDate(0, 0, 30), inst.PaidUntil, time.Minute)
	firstUntil := inst.PaidUntil

	// Renewal adds 30 days to the end date.
	rec = postHostingOrder(t, s, tok, `{"instance_id":"`+instID+`","accept_terms":true,"terms_version":"`+hosting.TermsVersion+`"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	renew, err := s.db.PaymentOrders().Get(ctx, out["order_id"].(string))
	require.NoError(t, err)
	applied, err = s.markHostingOrderPaid(ctx, renew, store.PaymentCompleted, "webhook", "")
	require.NoError(t, err)
	require.True(t, applied)
	inst, err = s.db.Hosting().Get(ctx, instID)
	require.NoError(t, err)
	require.WithinDuration(t, firstUntil.AddDate(0, 0, 30), inst.PaidUntil, time.Second)

	// Stop then delete.
	require.Equal(t, http.StatusOK, adminHostingCall(s, http.MethodPost, "/x", instID, "", s.adminStopHosting).Code)
	require.Equal(t, http.StatusOK, adminHostingCall(s, http.MethodPost, "/x", instID, "", s.adminDeleteHosting).Code)
	require.Equal(t, 1, fake.stops)
	require.Equal(t, 1, fake.deletes)
	list, err := s.db.Hosting().ListByKey(ctx, renew.KeyID)
	require.NoError(t, err)
	require.Empty(t, list, "deleted instances are hidden from the portal")
}

func TestHosting_ExpireDue(t *testing.T) {
	s, _, _ := hostingTestServer(t)
	ctx := t.Context()
	now := time.Now()
	for id, until := range map[string]time.Time{"old": now.Add(-time.Hour), "new": now.Add(time.Hour)} {
		require.NoError(t, s.db.Hosting().Create(ctx, store.HostingInstance{
			ID: id, TenantID: adminTenant, KeyID: "k", Status: store.HostingActive,
			PaidUntil: until, CreatedAt: now, UpdatedAt: now,
		}))
	}
	n, err := s.db.Hosting().ExpireDue(ctx, now)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	old, err := s.db.Hosting().Get(ctx, "old")
	require.NoError(t, err)
	require.Equal(t, store.HostingExpired, old.Status)
}

func TestHostingSettings_MasksKey(t *testing.T) {
	s, _, _ := hostingTestServer(t)
	r := chi.NewRouter()
	s.mountHosting(r)
	rec := doJSON(t, r, http.MethodGet, "/hosting/settings", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), `"dk"`)
	require.Contains(t, rec.Body.String(), `"has_api_key":true`)

	// Saving without api_key keeps the stored key.
	rec = doJSON(t, r, http.MethodPut, "/hosting/settings", `{"enabled":true,
		"dokploy_url":"https://dokploy.example.com","environment_id":"env-1",
		"root_domain":"host.example.com","cloudflare_zone_id":"z1",
		"nodes":[{"id":"default","name":"Node 1","public_ip":"203.0.113.7","enabled":true,"region":"id"}],
		"images":["luqmenul/9router-go:latest"],"container_port":20130,"env_template":""}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	st, err := hosting.Load(t.Context(), s.settingsGet(), s.financeBox())
	require.NoError(t, err)
	require.Equal(t, "dk", st.APIKey)
}

func TestHostingOrder_RequiresTurnstile(t *testing.T) {
	s, tok, _ := hostingTestServer(t)
	ts := &fakeTurnstile{enabled: true, siteKey: "site"}
	s.turnstile = ts
	body := `{"accept_terms":true,"terms_version":"` + hosting.TermsVersion + `","turnstile_token":"bad"}`

	rec := postHostingOrder(t, s, tok, body)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Equal(t, 1, ts.calls)
	list, err := s.db.Hosting().ListAll(t.Context(), "")
	require.NoError(t, err)
	require.Empty(t, list, "a rejected captcha must not create an instance")

	ts.accept = true
	rec = postHostingOrder(t, s, tok, body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

func TestHostingOrder_ReusesOpenPayment(t *testing.T) {
	s, tok, _ := hostingTestServer(t)
	body := `{"accept_terms":true,"terms_version":"` + hosting.TermsVersion + `"}`
	first := postHostingOrder(t, s, tok, body)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	second := postHostingOrder(t, s, tok, body)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	var a, b map[string]any
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &a))
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &b))
	require.Equal(t, a["order_id"], b["order_id"], "an open payment is reused, not duplicated")
}

func TestHosting_PaymentForDeletedInstanceGrantsNothing(t *testing.T) {
	s, tok, _ := hostingTestServer(t)
	ctx := t.Context()
	rec := postHostingOrder(t, s, tok, `{"accept_terms":true,"terms_version":"`+hosting.TermsVersion+`"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	instID := out["instance_id"].(string)
	require.Equal(t, http.StatusOK, adminHostingCall(s, http.MethodPost, "/x", instID, "", s.adminDeleteHosting).Code)

	order, err := s.db.PaymentOrders().Get(ctx, out["order_id"].(string))
	require.NoError(t, err)
	applied, err := s.markHostingOrderPaid(ctx, order, store.PaymentCompleted, "webhook", "")
	require.NoError(t, err)
	require.True(t, applied)
	order, err = s.db.PaymentOrders().Get(ctx, order.ID)
	require.NoError(t, err)
	require.Contains(t, order.Reason, "refund_required")
	inst, err := s.db.Hosting().Get(ctx, instID)
	require.NoError(t, err)
	require.Zero(t, inst.PrepaidDays)
	require.Equal(t, store.HostingDeleted, inst.Status)
}

func TestHosting_LateRenewalInsideGraceCountsFromEndDate(t *testing.T) {
	s, _, _ := hostingTestServer(t)
	ctx := t.Context()
	now := time.Now()
	end := now.Add(-48 * time.Hour) // expired 2 days ago, inside the 3-day grace
	require.NoError(t, s.db.Hosting().Create(ctx, store.HostingInstance{
		ID: "g1", TenantID: adminTenant, KeyID: "k", Status: store.HostingExpired,
		PaidUntil: end, ProvisionedAt: now.AddDate(0, -1, 0), CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, s.db.PaymentOrders().Create(ctx, store.PaymentOrder{
		ID: "go1", TenantID: adminTenant, KeyID: "k", AmountIDR: 50000, Status: store.PaymentPending,
		Provider: "sumopod", Product: store.ProductHosting, HostingInstanceID: "g1", CreatedAt: now, UpdatedAt: now,
	}))
	order, err := s.db.PaymentOrders().Get(ctx, "go1")
	require.NoError(t, err)
	applied, err := s.markHostingOrderPaid(ctx, order, store.PaymentCompleted, "webhook", "")
	require.NoError(t, err)
	require.True(t, applied)
	inst, err := s.db.Hosting().Get(ctx, "g1")
	require.NoError(t, err)
	require.WithinDuration(t, end.AddDate(0, 0, 30), inst.PaidUntil, time.Second)
	require.Equal(t, store.HostingActive, inst.Status)
}

func TestHostingSettings_URLChangeNeedsKey(t *testing.T) {
	s, _, _ := hostingTestServer(t)
	r := chi.NewRouter()
	s.mountHosting(r)
	base := `"enabled":false,"environment_id":"env-1","root_domain":"host.example.com","cloudflare_zone_id":"z1","images":["luqmenul/9router-go:latest"],"container_port":20130,"env_template":""`
	rec := doJSON(t, r, http.MethodPut, "/hosting/settings", `{`+base+`,"dokploy_url":"https://attacker.example.net"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "re-enter")

	rec = doJSON(t, r, http.MethodPut, "/hosting/settings", `{`+base+`,"dokploy_url":"https://new.example.com","api_key":"fresh"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestHosting_StaleProvisioningCanRetry(t *testing.T) {
	s, _, fake := hostingTestServer(t)
	ctx := t.Context()
	old := time.Now().Add(-10 * time.Minute)
	require.NoError(t, s.db.Hosting().Create(ctx, store.HostingInstance{
		ID: "st1", TenantID: adminTenant, KeyID: "k", Status: store.HostingProvisioning,
		PrepaidDays: 30, CreatedAt: old, UpdatedAt: old,
	}))
	rec := adminHostingCall(s, http.MethodPost, "/x", "st1", `{"image":"luqmenul/9router-go:latest"}`, s.adminProvisionHosting)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, fake.deploys)
}

func TestHostingSettings_RejectSharedJWTSecret(t *testing.T) {
	st := hosting.Settings{EnvTemplate: "FOO=1\nJWT_SECRET=shared"}
	require.ErrorContains(t, st.Normalize(), "JWT_SECRET")
}

func TestHostingCosts_CRUDAndFinance(t *testing.T) {
	s, _, _ := hostingTestServer(t)
	r := chi.NewRouter()
	s.mountHostingOps(r)
	today := time.Now().In(hosting.WIB).Format("2006-01-02")

	rec := doJSON(t, r, http.MethodPost, "/hosting/costs", `{"name":"VPS 8GB","category":"vps","amount_idr":150000,"period":"monthly","start_date":"`+today+`"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeBody(t, rec)["id"].(string)

	for _, bad := range []string{
		`{"name":"","amount_idr":1,"start_date":"2026-01-01"}`,
		`{"name":"x","amount_idr":0,"start_date":"2026-01-01"}`,
		`{"name":"x","amount_idr":1,"start_date":"01-01-2026"}`,
		`{"name":"x","amount_idr":1,"period":"one_time","start_date":"2026-01-01","end_date":"2026-02-01"}`,
		`{"name":"x","amount_idr":1,"start_date":"2026-02-01","end_date":"2026-01-01"}`,
		`{"name":"x","amount_idr":1,"category":"food","start_date":"2026-01-01"}`,
	} {
		require.Equal(t, http.StatusBadRequest, doJSON(t, r, http.MethodPost, "/hosting/costs", bad).Code, bad)
	}

	rec = doJSON(t, r, http.MethodPut, "/hosting/costs/"+id, `{"name":"VPS 16GB","amount_idr":200000,"period":"monthly","start_date":"`+today+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doJSON(t, r, http.MethodGet, "/hosting/finance?months=3", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := decodeBody(t, rec)
	require.Len(t, body["months"], 3)
	cur := body["current"].(map[string]any)
	require.EqualValues(t, 200000, cur["cost_idr"])
	require.EqualValues(t, 4, cur["break_even_instances"], "200k cost / 50k price")
	require.Equal(t, http.StatusBadRequest, doJSON(t, r, http.MethodGet, "/hosting/finance?months=99", "").Code)

	require.Equal(t, http.StatusNoContent, doJSON(t, r, http.MethodDelete, "/hosting/costs/"+id, "").Code)
	require.Equal(t, http.StatusNotFound, doJSON(t, r, http.MethodDelete, "/hosting/costs/"+id, "").Code)
}

func TestHosting_MultiNodePlacement(t *testing.T) {
	s, _, fake := hostingTestServer(t)
	ctx := t.Context()
	st, err := s.loadHostingSettingsCtx(ctx)
	require.NoError(t, err)
	require.Len(t, st.Nodes, 1)

	st.Nodes = []hosting.Node{
		{ID: "n1", Name: "Node 1", ServerID: "srv-1", PublicIP: "203.0.113.1", Capacity: 1, Enabled: true},
		{ID: "n2", Name: "Node 2", ServerID: "srv-2", PublicIP: "203.0.113.2", Capacity: 3, Enabled: true},
		{ID: "n3", Name: "Node 3", ServerID: "srv-3", PublicIP: "203.0.113.3", Capacity: 10, Enabled: false},
	}
	require.NoError(t, hosting.Save(ctx, s.settingsSet(), s.financeBox(), st))

	now := time.Now()
	mkPaid := func(id string) {
		require.NoError(t, s.db.Hosting().Create(ctx, store.HostingInstance{
			ID: id, TenantID: adminTenant, KeyID: "k", Status: store.HostingPaid, PrepaidDays: 30, CreatedAt: now, UpdatedAt: now,
		}))
	}
	prov := func(id, body string) *httptest.ResponseRecorder {
		return adminHostingCall(s, http.MethodPost, "/x", id, body, s.adminProvisionHosting)
	}
	img := `"image":"luqmenul/9router-go:latest"`

	// Auto: n2 has 3 free slots vs n1's 1.
	mkPaid("a")
	require.Equal(t, http.StatusOK, prov("a", `{`+img+`}`).Code)
	a, _ := s.db.Hosting().Get(ctx, "a")
	require.Equal(t, "n2", a.NodeID)
	require.Equal(t, a.Subdomain+".host.example.com", a.FQDN, "every node shares the main domain")
	require.Equal(t, "srv-2", fake.lastServer)

	// Explicit node.
	mkPaid("b")
	require.Equal(t, http.StatusOK, prov("b", `{`+img+`,"node_id":"n1"}`).Code)
	b, _ := s.db.Hosting().Get(ctx, "b")
	require.Equal(t, "n1", b.NodeID)
	require.Equal(t, "srv-1", fake.lastServer)

	// n1 is now full, n3 disabled, unknown rejected.
	mkPaid("c")
	require.Equal(t, http.StatusConflict, prov("c", `{`+img+`,"node_id":"n1"}`).Code)
	require.Equal(t, http.StatusConflict, prov("c", `{`+img+`,"node_id":"n3"}`).Code)
	require.Equal(t, http.StatusBadRequest, prov("c", `{`+img+`,"node_id":"nope"}`).Code)

	// Nodes endpoint reports usage and the recommended node.
	r := chi.NewRouter()
	s.mountHosting(r)
	rec := doJSON(t, r, http.MethodGet, "/hosting/nodes", "")
	require.Equal(t, http.StatusOK, rec.Code)
	nb := decodeBody(t, rec)
	require.Equal(t, "n2", nb["recommended"])

	// A failed instance retries on its own node only.
	fake.failDomainOnce = true
	require.Equal(t, http.StatusBadGateway, prov("c", `{`+img+`,"node_id":"n2"}`).Code)
	require.Equal(t, http.StatusConflict, prov("c", `{`+img+`,"node_id":"n1"}`).Code)
	require.Equal(t, http.StatusOK, prov("c", `{`+img+`}`).Code)
	c, _ := s.db.Hosting().Get(ctx, "c")
	require.Equal(t, "n2", c.NodeID)

	// Fill n2 (3 of 3), then auto has nowhere to go.
	mkPaid("d")
	require.Equal(t, http.StatusOK, prov("d", `{`+img+`}`).Code)
	mkPaid("e")
	rec = prov("e", `{`+img+`}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "full")
}

func TestHostingSettings_CloudflareToken(t *testing.T) {
	s, _, _ := hostingTestServer(t)
	r := chi.NewRouter()
	s.mountHosting(r)
	base := `"enabled":false,"dokploy_url":"https://dokploy.example.com","environment_id":"env-1","root_domain":"host.example.com","cloudflare_zone_id":"z1","images":["luqmenul/9router-go:latest"],"container_port":20130,"env_template":""`

	rec := doJSON(t, r, http.MethodPut, "/hosting/settings", `{`+base+`,"cloudflare_token":"cf-new-token"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = doJSON(t, r, http.MethodGet, "/hosting/settings", "")
	require.Contains(t, rec.Body.String(), `"has_cloudflare_token":true`)
	require.Contains(t, rec.Body.String(), `"cloudflare_zone_id":"z1"`)
	require.NotContains(t, rec.Body.String(), "cf-new-token")

	rec = doJSON(t, r, http.MethodPut, "/hosting/settings", `{`+base+`}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	st, err := hosting.Load(t.Context(), s.settingsGet(), s.financeBox())
	require.NoError(t, err)
	require.Equal(t, "cf-new-token", st.CloudflareToken, "saving without a token keeps it")

	rec = doJSON(t, r, http.MethodPut, "/hosting/settings", `{`+base+`,"clear_cloudflare_token":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"has_cloudflare_token":false`)
}

func TestHostingSettings_ZoneChangeNeedsToken(t *testing.T) {
	s, _, _ := hostingTestServer(t)
	r := chi.NewRouter()
	s.mountHosting(r)
	base := `"enabled":false,"dokploy_url":"https://dokploy.example.com","environment_id":"env-1","root_domain":"host.example.com","images":["luqmenul/9router-go:latest"],"container_port":20130,"env_template":""`
	rec := doJSON(t, r, http.MethodPut, "/hosting/settings", `{`+base+`,"cloudflare_zone_id":"other-zone"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "re-enter")

	rec = doJSON(t, r, http.MethodPut, "/hosting/settings", `{`+base+`,"cloudflare_zone_id":"other-zone","cloudflare_token":"t2"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestHostingSettingsTest_Cloudflare(t *testing.T) {
	s, _, _, dns := hostingTestServerDNS(t)
	r := chi.NewRouter()
	s.mountHosting(r)

	rec := doJSON(t, r, http.MethodPost, "/hosting/settings/test", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"ok":true}`, rec.Body.String())

	dns.zoneName = "tokenizer.ai.id"
	rec = doJSON(t, r, http.MethodPost, "/hosting/settings/test", "")
	require.Contains(t, rec.Body.String(), `"ok":false`)
	require.Contains(t, rec.Body.String(), `"error":"Cloudflare: `)
	require.Contains(t, rec.Body.String(), "not in zone")

	dns.zoneName, dns.failZone = "host.example.com", errors.New("cloudflare: Authentication error")
	rec = doJSON(t, r, http.MethodPost, "/hosting/settings/test", "")
	require.Contains(t, rec.Body.String(), `"error":"Cloudflare: `)
	require.Contains(t, rec.Body.String(), "Authentication error")
}

// paidHosting inserts a paid instance waiting to be created.
func paidHosting(t *testing.T, s *Server, id string) {
	t.Helper()
	now := time.Now()
	require.NoError(t, s.db.Hosting().Create(t.Context(), store.HostingInstance{
		ID: id, TenantID: adminTenant, KeyID: "k", Status: store.HostingPaid, PrepaidDays: 30, CreatedAt: now, UpdatedAt: now,
	}))
}

func provisionHosting(s *Server, id string) *httptest.ResponseRecorder {
	return adminHostingCall(s, http.MethodPost, "/x", id, `{"image":"luqmenul/9router-go:latest"}`, s.adminProvisionHosting)
}

func TestHostingProvision_CreatesDNSRecord(t *testing.T) {
	s, _, _, dns := hostingTestServerDNS(t)
	paidHosting(t, s, "d1")
	rec := provisionHosting(s, "d1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	inst, err := s.db.Hosting().Get(t.Context(), "d1")
	require.NoError(t, err)
	require.Equal(t, inst.Subdomain+".host.example.com", inst.FQDN)
	require.Equal(t, 1, dns.creates)
	r, ok := dns.records[inst.FQDN]
	require.True(t, ok, "record named after the instance FQDN")
	require.Equal(t, "203.0.113.7", r.Content, "points at the node IP")
	require.Equal(t, "keirouter hosting d1", dns.lastComment)
	require.Equal(t, r.ID, inst.DNSRecordID)
}

func TestHostingProvision_RetryNoSecondRecord(t *testing.T) {
	s, _, fake, dns := hostingTestServerDNS(t)
	paidHosting(t, s, "d2")
	fake.failDomainOnce = true
	require.Equal(t, http.StatusBadGateway, provisionHosting(s, "d2").Code)
	require.Equal(t, http.StatusOK, provisionHosting(s, "d2").Code)
	require.Equal(t, 1, dns.creates)
}

func TestHostingProvision_AdoptsExistingRecord(t *testing.T) {
	s, _, _, dns := hostingTestServerDNS(t)
	ctx := t.Context()
	paidHosting(t, s, "d3")
	// Pin the subdomain so the existing record's name is known.
	h, err := s.db.Hosting().Get(ctx, "d3")
	require.NoError(t, err)
	h.Subdomain, h.FQDN = "pinned01", "pinned01.host.example.com"
	require.NoError(t, s.db.Hosting().SetDokploy(ctx, h))
	dns.records["pinned01.host.example.com"] = cloudflare.Record{ID: "old", Name: "pinned01.host.example.com", Content: "203.0.113.7"}

	require.Equal(t, http.StatusOK, provisionHosting(s, "d3").Code)
	require.Equal(t, 0, dns.creates)
	h, err = s.db.Hosting().Get(ctx, "d3")
	require.NoError(t, err)
	require.Equal(t, "old", h.DNSRecordID)

	paidHosting(t, s, "d4")
	h, err = s.db.Hosting().Get(ctx, "d4")
	require.NoError(t, err)
	h.Subdomain, h.FQDN = "pinned02", "pinned02.host.example.com"
	require.NoError(t, s.db.Hosting().SetDokploy(ctx, h))
	dns.records["pinned02.host.example.com"] = cloudflare.Record{ID: "x", Name: "pinned02.host.example.com", Content: "198.51.100.1"}
	require.Equal(t, http.StatusBadGateway, provisionHosting(s, "d4").Code)
	h, err = s.db.Hosting().Get(ctx, "d4")
	require.NoError(t, err)
	require.Contains(t, h.LastError, "different IP")
}

func deleteHosting(s *Server, id string) *httptest.ResponseRecorder {
	return adminHostingCall(s, http.MethodPost, "/x", id, "", s.adminDeleteHosting)
}

func TestHostingDelete_RemovesDNSRecord(t *testing.T) {
	s, _, _, dns := hostingTestServerDNS(t)
	paidHosting(t, s, "e1")
	require.Equal(t, http.StatusOK, provisionHosting(s, "e1").Code)
	rec := deleteHosting(s, "e1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, dns.deletes)
	require.Empty(t, dns.records)
	h, err := s.db.Hosting().Get(t.Context(), "e1")
	require.NoError(t, err)
	require.Equal(t, store.HostingDeleted, h.Status)
}

func TestHostingDelete_DNSOnlyInstance(t *testing.T) {
	s, _, fake, dns := hostingTestServerDNS(t)
	ctx := t.Context()
	now := time.Now()
	require.NoError(t, s.db.Hosting().Create(ctx, store.HostingInstance{
		ID: "e2", TenantID: adminTenant, KeyID: "k", Status: store.HostingFailed, Subdomain: "dnsonly1",
		FQDN: "dnsonly1.host.example.com", DNSRecordID: "rec-x", CreatedAt: now, UpdatedAt: now,
	}))
	dns.records["dnsonly1.host.example.com"] = cloudflare.Record{ID: "rec-x"}
	rec := deleteHosting(s, "e2")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, dns.deletes)
	require.Equal(t, 0, fake.deletes, "no Dokploy app to delete")
}

func TestHostingDelete_DNSFailureKeepsInstance(t *testing.T) {
	s, _, fake, dns := hostingTestServerDNS(t)
	paidHosting(t, s, "e3")
	require.Equal(t, http.StatusOK, provisionHosting(s, "e3").Code)
	dns.failDelete = errors.New("cloudflare: rate limited")
	rec := deleteHosting(s, "e3")
	require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
	require.Equal(t, 0, fake.deletes)
	h, err := s.db.Hosting().Get(t.Context(), "e3")
	require.NoError(t, err)
	require.Equal(t, store.HostingActive, h.Status)
}

func TestHostingProvision_RejectsDomainOutsideZone(t *testing.T) {
	s, _, _, dns := hostingTestServerDNS(t)
	dns.zoneName = "other.com"
	paidHosting(t, s, "z1")
	require.Equal(t, http.StatusBadGateway, provisionHosting(s, "z1").Code)
	require.Equal(t, 0, dns.creates)
	h, err := s.db.Hosting().Get(t.Context(), "z1")
	require.NoError(t, err)
	require.Contains(t, h.LastError, "not in zone")
}

func TestHostingProvision_RepairsMissingRecord(t *testing.T) {
	s, _, fake, dns := hostingTestServerDNS(t)
	paidHosting(t, s, "r1")
	fake.failDomainOnce = true
	require.Equal(t, http.StatusBadGateway, provisionHosting(s, "r1").Code)
	h, err := s.db.Hosting().Get(t.Context(), "r1")
	require.NoError(t, err)
	require.NotEmpty(t, h.DNSRecordID)

	// The record disappears (deleted by hand, or by a racing delete).
	delete(dns.records, h.FQDN)
	require.Equal(t, http.StatusOK, provisionHosting(s, "r1").Code)
	require.Equal(t, 2, dns.creates, "the retry recreates the missing record")
	h, err = s.db.Hosting().Get(t.Context(), "r1")
	require.NoError(t, err)
	require.Equal(t, dns.records[h.FQDN].ID, h.DNSRecordID)
}

func TestHostingDokployServers(t *testing.T) {
	s, _, _ := hostingTestServer(t)
	r := chi.NewRouter()
	s.mountHosting(r)

	rec := doJSON(t, r, http.MethodGet, "/hosting/dokploy/servers", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"servers":[{"id":"srv-1","name":"Jakarta 1","ip":"203.0.113.10","status":"active"}]}`, rec.Body.String())

	st, err := hosting.Load(t.Context(), s.settingsGet(), s.financeBox())
	require.NoError(t, err)
	st.APIKey = ""
	require.NoError(t, hosting.Save(t.Context(), s.settingsSet(), s.financeBox(), st))
	rec = doJSON(t, r, http.MethodGet, "/hosting/dokploy/servers", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
