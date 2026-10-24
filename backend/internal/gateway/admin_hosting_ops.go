package gateway

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
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
	orders, err := s.db.PaymentOrders().ListByProduct(ctx, store.ProductHosting)
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
	var mrr int64 // monthly revenue of the running instances at today's prices
	for _, h := range instances {
		counts[string(h.Status)]++
		if h.Status == store.HostingActive || h.Status == store.HostingExpired {
			running++
			if p, ok := st.PeriodPrice(st.InstanceRegion(h.Region, h.NodeID)); ok {
				mrr += p
			}
		}
	}
	// Break-even uses the average price of what is running, or the cheapest
	// region when nothing runs yet.
	avgPrice := int64(0)
	if running > 0 {
		avgPrice = mrr / int64(running)
	} else {
		for _, rg := range st.Regions {
			if rg.Enabled && rg.PriceIDR > 0 && (avgPrice == 0 || rg.PriceIDR < avgPrice) {
				avgPrice = rg.PriceIDR
			}
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
	if avgPrice > 0 {
		breakEven = (cur.CostIDR + avgPrice - 1) / avgPrice
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
			"mrr_idr":               mrr,
			"price_idr":             avgPrice,
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
