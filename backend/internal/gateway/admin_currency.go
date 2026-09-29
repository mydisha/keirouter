package gateway

import (
	"errors"
	"net/http"

	"github.com/mydisha/keirouter/backend/internal/currency"
)

// currencyStatus is the GET/refresh response shape consumed by the UI.
type currencyStatus struct {
	Config        currency.Settings `json:"config"`
	EffectiveRate float64           `json:"effective_rate"`
	Source        string            `json:"source"`
	FetchedAt     string            `json:"fetched_at"`
	LastError     string            `json:"last_error"`
}

func currencyStatusOf(s currency.Settings) currencyStatus {
	rate, source, _ := currency.Resolve(s)
	return currencyStatus{Config: s, EffectiveRate: rate, Source: source, FetchedAt: s.FetchedAt, LastError: s.LastError}
}

func (s *Server) adminGetCurrency(w http.ResponseWriter, r *http.Request) {
	if s.currencySvc == nil {
		writeJSON(w, http.StatusOK, currencyStatusOf(currency.Defaults(currency.Settings{})))
		return
	}
	writeJSON(w, http.StatusOK, currencyStatusOf(s.currencySvc.Load(r.Context())))
}

type currencyPatch struct {
	AutoRefreshEnabled *bool    `json:"auto_refresh_enabled"`
	RefreshIntervalH   *int     `json:"refresh_interval_h"`
	OverrideEnabled    *bool    `json:"override_enabled"`
	OverrideRate       *float64 `json:"override_rate"`
	SourceURL          *string  `json:"source_url"`
}

func (s *Server) adminUpdateCurrency(w http.ResponseWriter, r *http.Request) {
	if s.currencySvc == nil {
		writeError(w, http.StatusServiceUnavailable, "currency service unavailable")
		return
	}
	var patch currencyPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	// Strict load + mutate + save under the service mutex. A read/write failure
	// is a server error; only Validate failures are the client's fault.
	cur, err := s.currencySvc.Update(r.Context(), func(c *currency.Settings) {
		if patch.AutoRefreshEnabled != nil {
			c.AutoRefreshEnabled = *patch.AutoRefreshEnabled
		}
		if patch.RefreshIntervalH != nil {
			c.RefreshIntervalH = *patch.RefreshIntervalH
		}
		if patch.OverrideEnabled != nil {
			c.OverrideEnabled = *patch.OverrideEnabled
		}
		if patch.OverrideRate != nil {
			c.OverrideRate = *patch.OverrideRate
		}
		if patch.SourceURL != nil {
			c.SourceURL = *patch.SourceURL
		}
	})
	if err != nil {
		if errors.Is(err, currency.ErrInvalidConfig) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, currencyStatusOf(cur))
}

func (s *Server) adminRefreshCurrency(w http.ResponseWriter, r *http.Request) {
	if s.currencySvc == nil {
		writeError(w, http.StatusServiceUnavailable, "currency service unavailable")
		return
	}
	out, err := s.currencySvc.Refresh(r.Context())
	// A failed upstream fetch is a 200 with last_error set so the UI can show it.
	if err != nil && out.LastError == "" {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, currencyStatusOf(out))
}
