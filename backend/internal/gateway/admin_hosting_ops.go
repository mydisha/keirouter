package gateway

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/mydisha/keirouter/backend/internal/hosting"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func (s *Server) mountHostingOps(r chi.Router) {
	r.Get("/hosting/finance", s.adminHostingFinance)
	r.Post("/hosting/costs", s.adminCreateHostingCost)
	r.Put("/hosting/costs/{id}", s.adminUpdateHostingCost)
	r.Delete("/hosting/costs/{id}", s.adminDeleteHostingCost)
	r.Get("/hosting/monitoring/settings", s.adminGetHostingMonitoring)
	r.Put("/hosting/monitoring/settings", s.adminPutHostingMonitoring)
	r.Get("/hosting/monitoring/servers", s.adminHostingServerMetrics)
	r.Get("/hosting/monitoring/containers", s.adminHostingContainerMetrics)
}

// ─── Finance ────────────────────────────────────────────────────────────────

func hostingCostView(c store.HostingCost) map[string]any {
	return map[string]any{
		"id": c.ID, "name": c.Name, "category": c.Category, "amount_idr": c.AmountIDR,
		"period": c.Period, "start_date": c.StartDate, "end_date": c.EndDate, "note": c.Note,
		"created_at": c.CreatedAt, "updated_at": c.UpdatedAt,
	}
}

// adminHostingFinance returns monthly revenue, cost and profit for the last
// `months` months (default 12), a snapshot of the current month and the
// cost list.
func (s *Server) adminHostingFinance(w http.ResponseWriter, r *http.Request) {
	months := 12
	if v := r.URL.Query().Get("months"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 36 {
			writeError(w, http.StatusBadRequest, "months must be 1-36")
			return
		}
		months = n
	}
	ctx := r.Context()
	orders, err := s.db.PaymentOrders().ListAll(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	costs, err := s.db.HostingCosts().List(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	instances, err := s.db.Hosting().ListAll(ctx, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	st, err := s.loadHostingSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	now := time.Now()
	series := hosting.Months(orders, costs, now, months)
	cur := series[len(series)-1]
	counts := map[string]int{}
	running := 0
	for _, h := range instances {
		counts[string(h.Status)]++
		if h.Status == store.HostingActive || h.Status == store.HostingExpired {
			running++
		}
	}
	var refundOpen []map[string]any
	var totalRevenue, totalCost int64
	for _, m := range series {
		totalRevenue += m.RevenueIDR
		totalCost += m.CostIDR
	}
	for _, o := range orders {
		if o.Product == store.ProductHosting && hosting.RefundRequired(o) {
			refundOpen = append(refundOpen, map[string]any{
				"order_id": o.ID, "amount_idr": o.AmountIDR, "paid_at": o.PaidAt, "instance_id": o.HostingInstanceID,
			})
		}
	}
	var perInstance, breakEven int64
	if running > 0 {
		perInstance = cur.CostIDR / int64(running)
	}
	if st.MonthlyPriceIDR > 0 {
		breakEven = (cur.CostIDR + st.MonthlyPriceIDR - 1) / st.MonthlyPriceIDR
	}
	costViews := make([]map[string]any, 0, len(costs))
	for _, c := range costs {
		v := hostingCostView(c)
		v["this_month_idr"] = hosting.CostInMonth(c, firstOfMonthWIB(now))
		costViews = append(costViews, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"months": series,
		"current": map[string]any{
			"month":                 cur.Month,
			"revenue_idr":           cur.RevenueIDR,
			"cost_idr":              cur.CostIDR,
			"profit_idr":            cur.ProfitIDR,
			"running_instances":     running,
			"mrr_idr":               int64(running) * st.MonthlyPriceIDR,
			"price_idr":             st.MonthlyPriceIDR,
			"cost_per_instance_idr": perInstance,
			"break_even_instances":  breakEven,
		},
		"totals":         map[string]any{"revenue_idr": totalRevenue, "cost_idr": totalCost, "profit_idr": totalRevenue - totalCost},
		"status_counts":  counts,
		"refunds_needed": refundOpen,
		"costs":          costViews,
		"note":           "Revenue is the gross order amount; SumoPod fees are not deducted.",
	})
}

func firstOfMonthWIB(t time.Time) time.Time {
	l := t.In(hosting.WIB)
	return time.Date(l.Year(), l.Month(), 1, 0, 0, 0, 0, hosting.WIB)
}

type hostingCostRequest struct {
	Name      string `json:"name"`
	Category  string `json:"category"`
	AmountIDR int64  `json:"amount_idr"`
	Period    string `json:"period"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Note      string `json:"note"`
}

var hostingCostCategories = map[string]bool{"vps": true, "domain": true, "software": true, "other": true}

func (b hostingCostRequest) toCost() (store.HostingCost, error) {
	c := store.HostingCost{
		Name: strings.TrimSpace(b.Name), Category: strings.TrimSpace(b.Category), AmountIDR: b.AmountIDR,
		Period: strings.TrimSpace(b.Period), StartDate: strings.TrimSpace(b.StartDate),
		EndDate: strings.TrimSpace(b.EndDate), Note: strings.TrimSpace(b.Note),
	}
	if c.Category == "" {
		c.Category = "vps"
	}
	if c.Period == "" {
		c.Period = store.CostMonthly
	}
	switch {
	case c.Name == "" || len(c.Name) > 120:
		return c, errors.New("name is required (max 120 characters)")
	case !hostingCostCategories[c.Category]:
		return c, errors.New("category must be vps, domain, software or other")
	case c.Period != store.CostMonthly && c.Period != store.CostOneTime:
		return c, errors.New("period must be monthly or one_time")
	case c.AmountIDR <= 0 || c.AmountIDR > 1_000_000_000_000:
		return c, errors.New("amount_idr must be positive")
	case len(c.Note) > 500:
		return c, errors.New("note must be 500 characters or fewer")
	}
	start, err := time.Parse("2006-01-02", c.StartDate)
	if err != nil {
		return c, errors.New("start_date must be YYYY-MM-DD")
	}
	if c.EndDate != "" {
		if c.Period == store.CostOneTime {
			return c, errors.New("a one-time cost has no end_date")
		}
		end, err := time.Parse("2006-01-02", c.EndDate)
		if err != nil {
			return c, errors.New("end_date must be YYYY-MM-DD")
		}
		if end.Before(start) {
			return c, errors.New("end_date must not be before start_date")
		}
	}
	return c, nil
}

func (s *Server) adminCreateHostingCost(w http.ResponseWriter, r *http.Request) {
	var body hostingCostRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	c, err := body.toCost()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now()
	c.ID, c.CreatedAt, c.UpdatedAt = uuid.NewString(), now, now
	if err := s.db.HostingCosts().Create(r.Context(), c); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusCreated, hostingCostView(c))
}

func (s *Server) adminUpdateHostingCost(w http.ResponseWriter, r *http.Request) {
	var body hostingCostRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	c, err := body.toCost()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c.ID = chi.URLParam(r, "id")
	if err := s.db.HostingCosts().Update(r.Context(), c); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "cost not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	updated, err := s.db.HostingCosts().Get(r.Context(), c.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, hostingCostView(updated))
}

func (s *Server) adminDeleteHostingCost(w http.ResponseWriter, r *http.Request) {
	if err := s.db.HostingCosts().Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "cost not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Monitoring ─────────────────────────────────────────────────────────────

func (s *Server) loadMonitorTargets(r *http.Request) ([]hosting.MonitorTarget, error) {
	return hosting.LoadMonitoring(r.Context(), s.settingsGet(), s.financeBox())
}

func monitorTargetView(t hosting.MonitorTarget) map[string]any {
	return map[string]any{
		"id": t.ID, "name": t.Name, "url": t.URL, "instances": t.Instances,
		"has_token": t.Token != "", "token_masked": hosting.MaskAPIKey(t.Token),
	}
}

func (s *Server) adminGetHostingMonitoring(w http.ResponseWriter, r *http.Request) {
	targets, err := s.loadMonitorTargets(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(targets))
	for _, t := range targets {
		out = append(out, monitorTargetView(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": out})
}

type monitorTargetRequest struct {
	ID        string  `json:"id"` // empty for a new target
	Name      string  `json:"name"`
	URL       string  `json:"url"`
	Token     *string `json:"token"` // nil or "" keeps the stored token
	Instances bool    `json:"instances"`
}

// adminPutHostingMonitoring replaces the target list. A stored token is kept
// only while the target's URL is unchanged, so a stolen session cannot point
// it at another host and harvest the token.
func (s *Server) adminPutHostingMonitoring(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Targets []monitorTargetRequest `json:"targets"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Targets) > 10 {
		writeError(w, http.StatusBadRequest, "at most 10 monitoring servers")
		return
	}
	cur, err := s.loadMonitorTargets(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	byID := map[string]hosting.MonitorTarget{}
	for _, t := range cur {
		byID[t.ID] = t
	}
	next := make([]hosting.MonitorTarget, 0, len(body.Targets))
	instancesSet := false
	for _, b := range body.Targets {
		t := hosting.MonitorTarget{ID: b.ID, Name: b.Name, URL: b.URL, Instances: b.Instances}
		if err := hosting.NormalizeTarget(&t); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		newToken := b.Token != nil && strings.TrimSpace(*b.Token) != ""
		if newToken {
			t.Token = strings.TrimSpace(*b.Token)
		} else if old, ok := byID[t.ID]; ok && t.ID != "" {
			if old.URL != t.URL {
				writeError(w, http.StatusBadRequest, "re-enter the token for "+t.Name+" when changing its URL")
				return
			}
			t.Token = old.Token
		}
		if t.Token == "" {
			writeError(w, http.StatusBadRequest, "a token is required for "+t.Name)
			return
		}
		if t.ID == "" {
			t.ID = uuid.NewString()
		}
		if t.Instances {
			if instancesSet {
				writeError(w, http.StatusBadRequest, "only one server can be marked as running customer instances")
				return
			}
			instancesSet = true
		}
		next = append(next, t)
	}
	if err := hosting.SaveMonitoring(r.Context(), s.settingsSet(), s.financeBox(), next); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(next))
	for _, t := range next {
		out = append(out, monitorTargetView(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": out})
}

// adminHostingServerMetrics returns the recent samples of every monitored
// server. A server that cannot be reached reports its error instead.
func (s *Server) adminHostingServerMetrics(w http.ResponseWriter, r *http.Request) {
	limit := 60
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 500 {
			limit = n
		}
	}
	targets, err := s.loadMonitorTargets(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out := make([]map[string]any, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t hosting.MonitorTarget) {
			defer wg.Done()
			v := map[string]any{"id": t.ID, "name": t.Name, "instances": t.Instances, "samples": []hosting.ServerMetric{}}
			samples, err := s.monitorClient(t).Server(ctx, limit)
			if err != nil {
				v["error"] = err.Error()
			} else if samples != nil {
				v["samples"] = samples
			}
			out[i] = v
		}(i, t)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"servers": out})
}

// adminHostingContainerMetrics returns recent per-container samples for
// every running instance, read from the server marked as running them.
func (s *Server) adminHostingContainerMetrics(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 200 {
			limit = n
		}
	}
	targets, err := s.loadMonitorTargets(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	var target *hosting.MonitorTarget
	for i := range targets {
		if targets[i].Instances {
			target = &targets[i]
		}
	}
	if target == nil {
		writeJSON(w, http.StatusOK, map[string]any{"containers": []any{}, "error": "no monitoring server is marked as running customer instances"})
		return
	}
	instances, err := s.db.Hosting().ListAll(r.Context(), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	var running []store.HostingInstance
	for _, h := range instances {
		if h.DokployAppName != "" && (h.Status == store.HostingActive || h.Status == store.HostingExpired) {
			running = append(running, h)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	client := s.monitorClient(*target)
	out := make([]map[string]any, len(running))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, h := range running {
		wg.Add(1)
		go func(i int, h store.HostingInstance) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			v := map[string]any{
				"instance_id": h.ID, "app_name": h.DokployAppName, "fqdn": h.FQDN,
				"status": string(h.Status), "samples": []hosting.ContainerMetric{},
			}
			samples, err := client.Container(ctx, h.DokployAppName, limit)
			if err != nil {
				v["error"] = err.Error()
			} else if samples != nil {
				v["samples"] = samples
			}
			out[i] = v
		}(i, h)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"server": target.Name, "containers": out})
}

// monitorFetcher is the monitoring surface; tests swap it via monitorFactory.
type monitorFetcher interface {
	Server(ctx context.Context, limit int) ([]hosting.ServerMetric, error)
	Container(ctx context.Context, appName string, limit int) ([]hosting.ContainerMetric, error)
}

func (s *Server) monitorClient(t hosting.MonitorTarget) monitorFetcher {
	if s.monitorFactory != nil {
		return s.monitorFactory(t)
	}
	return hosting.NewMonitorClient(t)
}
