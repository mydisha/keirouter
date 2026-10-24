package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/mydisha/keirouter/backend/internal/hosting"
)

// hostingRegionStocks returns every region with its remaining stock.
func (s *Server) hostingRegionStocks(ctx context.Context, st hosting.Settings) ([]hosting.RegionStock, error) {
	repo := s.db.Hosting()
	used, err := repo.CountByNode(ctx)
	if err != nil {
		return nil, err
	}
	waiting, err := repo.CountWaitingByRegion(ctx, time.Now().Add(-hosting.CheckoutWindow))
	if err != nil {
		return nil, err
	}
	return st.RegionStocks(used, waiting), nil
}

// hostingRegionStock returns one region's stock, or ok false when the
// region is unknown.
func (s *Server) hostingRegionStock(ctx context.Context, st hosting.Settings, region string) (hosting.RegionStock, bool, error) {
	stocks, err := s.hostingRegionStocks(ctx, st)
	if err != nil {
		return hosting.RegionStock{}, false, err
	}
	for _, rs := range stocks {
		if rs.ID == region {
			return rs, true, nil
		}
	}
	return hosting.RegionStock{}, false, nil
}

// publicRegionView is what anyone may see about a region: price and how
// many slots are left. Node names, counts and domains stay private.
func publicRegionView(rs hosting.RegionStock, open bool) map[string]any {
	soldOut := rs.SoldOut || !open
	return map[string]any{
		"id":        rs.ID,
		"name":      rs.Name,
		"price_idr": rs.PriceIDR,
		"stock":     rs.Stock, // -1 = unlimited
		"sold_out":  soldOut,
	}
}

// publicHosting serves the hosting landing page: whether ordering is open,
// and each enabled region's price and stock.
func (s *Server) publicHosting(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadHostingSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	open := st.Enabled && s.cfg.Payment.Enabled && s.paymentClient != nil
	regions := []map[string]any{}
	if st.Enabled {
		stocks, err := s.hostingRegionStocks(r.Context(), st)
		if err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
		for _, rs := range stocks {
			if rs.Enabled && rs.PriceIDR > 0 {
				regions = append(regions, publicRegionView(rs, open))
			}
		}
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     open,
		"period_days": hosting.PeriodDays,
		"terms_url":   hostingTermsPath,
		"regions":     regions,
	})
}
