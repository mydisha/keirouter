package gateway

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/finance"
	"github.com/mydisha/keirouter/backend/internal/store"
)

const (
	maxTopupUSD          = 1_000_000.0
	maxUpstreamAmountUSD = 1_000_000_000.0
	discordTestMessage   = "Tes notifikasi saldo provider dari Tokenizer."
)

// mountFinance registers the admin finance dashboard endpoints.
func (s *Server) mountFinance(r chi.Router) {
	r.Get("/payments/finance", s.adminFinanceReport)
	r.Get("/payments/providers", s.adminFinanceProviders)
	r.Get("/payments/upstream-accounts", s.adminListUpstreamAccounts)
	r.Post("/payments/upstream-accounts", s.adminCreateUpstreamAccount)
	r.Patch("/payments/upstream-accounts/{id}", s.adminUpdateUpstreamAccount)
	r.Get("/payments/upstream-accounts/{id}/topups", s.adminListUpstreamTopups)
	r.Post("/payments/upstream-accounts/{id}/topups", s.adminCreateUpstreamTopup)
	r.Delete("/payments/upstream-topups/{id}", s.adminDeleteUpstreamTopup)
	r.Get("/payments/alerts", s.adminGetFinanceAlerts)
	r.Put("/payments/alerts", s.adminPutFinanceAlerts)
	r.Post("/payments/alerts/test", s.adminTestFinanceAlerts)
}

func (s *Server) settingsGet() func(context.Context, string) (string, error) {
	if s.settings != nil {
		return s.settings.Get
	}
	return s.db.Settings().Get
}

// financeBox seals the Discord webhook with the master key. It returns a nil
// interface (not a typed nil) when no vault is configured.
func (s *Server) financeBox() finance.SecretBox {
	if s.vault == nil {
		return nil
	}
	return s.vault.Sealer()
}

func (s *Server) settingsSet() func(context.Context, string, string) error {
	if s.settings != nil {
		return s.settings.Set
	}
	return s.db.Settings().Set
}

// financeLoader returns the loader wired in New, or builds one from the
// server's store (handler tests construct Server directly).
func (s *Server) financeLoader() *finance.Loader {
	if s.fin != nil {
		return s.fin
	}
	return &finance.Loader{DB: s.db, Settings: s.settingsGet(), InFlight: s.inflight.Snapshot}
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
}

func usdFromMicros(m int64) float64 { return float64(m) / 1_000_000 }

func microsFromUSD(v float64) int64 { return int64(math.Round(v * 1_000_000)) }

func timeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func upstreamAccountView(a store.UpstreamAccount) map[string]any {
	return map[string]any{
		"id":                  a.ID,
		"name":                a.Name,
		"url":                 a.URL,
		"status":              a.Status,
		"opening_balance_usd": usdFromMicros(a.OpeningBalanceMicros),
		"opening_at":          timeString(a.OpeningAt),
		"low_balance_usd":     usdFromMicros(a.LowBalanceMicros),
		"alert_fired_at":      timeString(a.AlertFiredAt),
		"providers":           nonNilStrings(a.Providers),
		"created_at":          timeString(a.CreatedAt),
		"updated_at":          timeString(a.UpdatedAt),
	}
}

func accountBalanceView(b finance.AccountBalance) map[string]any {
	v := upstreamAccountView(b.Account)
	v["balance_usd"] = b.BalanceUSD
	v["topups_usd"] = b.TopupsUSD
	v["spent_usd"] = b.SpentUSD
	v["spent_estimated_usd"] = b.SpentEstimatedUSD
	v["spent_range_usd"] = b.SpentRangeUSD
	v["avg_daily_usd"] = b.AvgDailyUSD
	v["days_left"] = b.DaysLeft
	v["in_flight"] = b.InFlight
	v["low"] = b.Low
	return v
}

func overviewView(o finance.Overview) map[string]any {
	return map[string]any{
		"billed_usd":             o.BilledUSD,
		"revenue_usd":            o.RevenueUSD,
		"cost_exact_usd":         o.CostExactUSD,
		"cost_estimated_usd":     o.CostEstimatedUSD,
		"profit_usd":             o.ProfitUSD,
		"margin_pct":             o.MarginPct,
		"requests":               o.Requests,
		"credit_in_idr":          o.CreditInIDR,
		"credit_in_usd":          o.CreditInUSD,
		"paid_orders":            o.PaidOrders,
		"outstanding_credit_usd": o.OutstandingCreditUSD,
		"in_flight":              o.InFlight,
	}
}

func topupView(t store.UpstreamTopup) map[string]any {
	return map[string]any{
		"id":         t.ID,
		"account_id": t.AccountID,
		"amount_usd": usdFromMicros(t.AmountMicros),
		"topup_at":   timeString(t.TopupAt),
		"note":       t.Note,
		"actor":      t.Actor,
		"created_at": timeString(t.CreatedAt),
	}
}

// adminFinanceReport returns overview, account balances and the unmapped
// bucket for ?range=today|7d|30d|all (default 30d).
func (s *Server) adminFinanceReport(w http.ResponseWriter, r *http.Request) {
	rangeName := r.URL.Query().Get("range")
	if rangeName == "" {
		rangeName = "30d"
	}
	rep, err := s.financeLoader().Report(r.Context(), rangeName)
	if err != nil {
		if errors.Is(err, finance.ErrBadRange) {
			writeError(w, http.StatusBadRequest, "range must be one of today, 7d, 30d, all")
			return
		}
		s.internalError(w, err)
		return
	}
	accounts := make([]map[string]any, 0, len(rep.Accounts))
	for _, b := range rep.Accounts {
		accounts = append(accounts, accountBalanceView(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":    rep.Range,
		"overview": overviewView(rep.Overview),
		"accounts": accounts,
		"unmapped": map[string]any{
			"providers":       nonNilStrings(rep.Unmapped.Providers),
			"spent_range_usd": rep.Unmapped.SpentRangeUSD,
			"requests":        rep.Unmapped.Requests,
		},
	})
}

// adminFinanceProviders lists provider ids seen in usage plus registered
// custom providers, for the account mapping picker.
func (s *Server) adminFinanceProviders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	labels := map[string]string{}
	seen, err := s.db.Usage().DistinctProviders(ctx, adminTenant)
	if err != nil {
		s.internalError(w, err)
		return
	}
	for _, p := range seen {
		labels[p] = p
	}
	custom, err := s.db.CustomProviders().ListProviders(ctx, adminTenant)
	if err != nil {
		s.internalError(w, err)
		return
	}
	for _, p := range custom {
		label := p.DisplayName
		if label == "" {
			label = p.ID
		}
		labels[p.ID] = label
	}
	ids := make([]string, 0, len(labels))
	for id := range labels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, map[string]string{"id": id, "label": labels[id]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

func (s *Server) adminListUpstreamAccounts(w http.ResponseWriter, r *http.Request) {
	accs, err := s.db.Upstream().List(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(accs))
	for _, a := range accs {
		out = append(out, upstreamAccountView(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

// parseFinanceDate accepts RFC3339 or YYYY-MM-DD (local midnight) and rejects
// dates in the future.
func parseFinanceDate(v string, now time.Time) (time.Time, string) {
	v = strings.TrimSpace(v)
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		t, err = time.ParseInLocation("2006-01-02", v, time.Local)
	}
	if err != nil {
		return time.Time{}, "date must be RFC3339 or YYYY-MM-DD"
	}
	if t.After(now) {
		return time.Time{}, "date cannot be in the future"
	}
	return t, ""
}

func validAmount(v float64, max float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= max
}

type upstreamAccountBody struct {
	Name              *string   `json:"name"`
	URL               *string   `json:"url"`
	Status            *string   `json:"status"`
	OpeningBalanceUSD *float64  `json:"opening_balance_usd"`
	OpeningAt         *string   `json:"opening_at"`
	LowBalanceUSD     *float64  `json:"low_balance_usd"`
	Providers         *[]string `json:"providers"`
}

// apply validates the body and copies set fields onto a. It returns an error
// message for a 400 response, or "".
func (b upstreamAccountBody) apply(a *store.UpstreamAccount, now time.Time) string {
	if b.Name != nil {
		name := strings.TrimSpace(*b.Name)
		if name == "" || len(name) > 100 {
			return "name is required (max 100 characters)"
		}
		a.Name = name
	}
	if b.URL != nil {
		u := strings.TrimSpace(*b.URL)
		if len(u) > 500 {
			return "url must be 500 characters or fewer"
		}
		if u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return "url must start with http:// or https://"
		}
		a.URL = u
	}
	if b.Status != nil {
		switch *b.Status {
		case "active", "planned", "archived":
			a.Status = *b.Status
		default:
			return "status must be active, planned or archived"
		}
	}
	if b.OpeningBalanceUSD != nil {
		if !validAmount(*b.OpeningBalanceUSD, maxUpstreamAmountUSD) {
			return "opening_balance_usd must be between 0 and 1000000000"
		}
		a.OpeningBalanceMicros = microsFromUSD(*b.OpeningBalanceUSD)
	}
	if b.OpeningAt != nil {
		t, msg := parseFinanceDate(*b.OpeningAt, now)
		if msg != "" {
			return "opening_at: " + msg
		}
		a.OpeningAt = t
	}
	if b.LowBalanceUSD != nil {
		if !validAmount(*b.LowBalanceUSD, maxUpstreamAmountUSD) {
			return "low_balance_usd must be between 0 and 1000000000"
		}
		a.LowBalanceMicros = microsFromUSD(*b.LowBalanceUSD)
	}
	return ""
}

func cleanProviders(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func (s *Server) adminCreateUpstreamAccount(w http.ResponseWriter, r *http.Request) {
	var body upstreamAccountBody
	if !decodeJSON(w, r, &body) {
		return
	}
	now := time.Now()
	a := store.UpstreamAccount{ID: uuid.NewString(), Status: "active", OpeningAt: now}
	if body.Name == nil {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if msg := body.apply(&a, now); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ctx := r.Context()
	if err := s.db.Upstream().Create(ctx, a); err != nil {
		s.internalError(w, err)
		return
	}
	if body.Providers != nil {
		if err := s.db.Upstream().SetProviders(ctx, a.ID, cleanProviders(*body.Providers)); err != nil {
			s.internalError(w, err)
			return
		}
	}
	created, err := s.db.Upstream().Get(ctx, a.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, upstreamAccountView(created))
}

func (s *Server) adminUpdateUpstreamAccount(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body upstreamAccountBody
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()
	repo := s.db.Upstream()
	a, err := repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "upstream account not found")
			return
		}
		s.internalError(w, err)
		return
	}
	prevThreshold := a.LowBalanceMicros
	if msg := body.apply(&a, time.Now()); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := repo.Update(ctx, a); err != nil {
		s.internalError(w, err)
		return
	}
	if body.Providers != nil {
		if err := repo.SetProviders(ctx, id, cleanProviders(*body.Providers)); err != nil {
			s.internalError(w, err)
			return
		}
	}
	// A new threshold re-arms the low-balance alert.
	if a.LowBalanceMicros != prevThreshold && !a.AlertFiredAt.IsZero() {
		if err := repo.SetAlertFiredAt(ctx, id, time.Time{}); err != nil {
			s.internalError(w, err)
			return
		}
	}
	updated, err := repo.Get(ctx, id)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, upstreamAccountView(updated))
}

// upstreamAccountExists writes a 404 or 500 and returns false when the
// account cannot be loaded.
func (s *Server) upstreamAccountExists(w http.ResponseWriter, ctx context.Context, id string) bool {
	if _, err := s.db.Upstream().Get(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "upstream account not found")
			return false
		}
		s.internalError(w, err)
		return false
	}
	return true
}

func (s *Server) adminListUpstreamTopups(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !s.upstreamAccountExists(w, r.Context(), id) {
		return
	}
	list, err := s.db.Upstream().ListTopups(r.Context(), id)
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, t := range list {
		out = append(out, topupView(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"topups": out})
}

func (s *Server) adminCreateUpstreamTopup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		AmountUSD float64 `json:"amount_usd"`
		TopupAt   string  `json:"topup_at"`
		Note      string  `json:"note"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !validAmount(body.AmountUSD, maxTopupUSD) || body.AmountUSD <= 0 {
		writeError(w, http.StatusBadRequest, "amount_usd must be greater than 0 and at most 1000000")
		return
	}
	now := time.Now()
	at := now
	if strings.TrimSpace(body.TopupAt) != "" {
		t, msg := parseFinanceDate(body.TopupAt, now)
		if msg != "" {
			writeError(w, http.StatusBadRequest, "topup_at: "+msg)
			return
		}
		at = t
	}
	note := strings.TrimSpace(body.Note)
	if len(note) > 500 {
		writeError(w, http.StatusBadRequest, "note must be 500 characters or fewer")
		return
	}
	acc, err := s.db.Upstream().Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "upstream account not found")
			return
		}
		s.internalError(w, err)
		return
	}
	// Top-ups before opening_at are not counted in the balance, so refuse
	// them instead of accepting a top-up that silently has no effect.
	if at.Before(acc.OpeningAt) {
		writeError(w, http.StatusBadRequest, "Tanggal topup tidak boleh sebelum tanggal saldo awal akun.")
		return
	}
	t := store.UpstreamTopup{
		ID: uuid.NewString(), AccountID: id, AmountMicros: microsFromUSD(body.AmountUSD),
		TopupAt: at, Note: note, Actor: "dashboard", CreatedAt: now,
	}
	if err := s.db.Upstream().AddTopup(r.Context(), t); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, topupView(t))
}

func (s *Server) adminDeleteUpstreamTopup(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Upstream().DeleteTopup(r.Context(), chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "top-up not found")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func alertSettingsView(a finance.AlertSettings) map[string]any {
	return map[string]any{
		"enabled":                    a.Enabled,
		"discord_webhook_url_masked": finance.MaskDiscordWebhook(a.DiscordWebhookURL),
		"configured":                 a.DiscordWebhookURL != "",
	}
}

func (s *Server) adminGetFinanceAlerts(w http.ResponseWriter, r *http.Request) {
	a, err := finance.LoadAlertSettings(r.Context(), s.settingsGet(), s.financeBox())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, alertSettingsView(a))
}

func (s *Server) adminPutFinanceAlerts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled           bool    `json:"enabled"`
		DiscordWebhookURL *string `json:"discord_webhook_url"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()
	cur, err := finance.LoadAlertSettings(ctx, s.settingsGet(), s.financeBox())
	if err != nil {
		s.internalError(w, err)
		return
	}
	cur.Enabled = body.Enabled
	// Omitted URL keeps the stored one; an explicit "" removes it.
	if body.DiscordWebhookURL != nil {
		u := strings.TrimSpace(*body.DiscordWebhookURL)
		if u != "" {
			if err := finance.ValidateDiscordWebhook(u); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		cur.DiscordWebhookURL = u
	}
	if cur.DiscordWebhookURL == "" {
		cur.Enabled = false
	}
	if err := finance.SaveAlertSettings(ctx, s.settingsSet(), s.financeBox(), cur); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, alertSettingsView(cur))
}

func (s *Server) adminTestFinanceAlerts(w http.ResponseWriter, r *http.Request) {
	a, err := finance.LoadAlertSettings(r.Context(), s.settingsGet(), s.financeBox())
	if err != nil {
		s.internalError(w, err)
		return
	}
	if a.DiscordWebhookURL == "" {
		writeError(w, http.StatusBadRequest, "no Discord webhook configured")
		return
	}
	if err := finance.PostDiscord(r.Context(), a.DiscordWebhookURL, discordTestMessage); err != nil {
		s.log.Warn("finance alert test failed", "err", err)
		writeError(w, http.StatusBadGateway, "Discord did not accept the test message")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
