package gateway

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// usdLimitToMicros converts an absolute USD limit (which may be zero) into
// integer micro-USD without going through float64. Unlike usdToMicros it allows
// 0 (an explicit "no budget" limit) but still rejects negatives and values above
// the per-request cap.
func usdLimitToMicros(n json.Number) (int64, error) {
	r, ok := new(big.Rat).SetString(n.String())
	if !ok {
		return 0, errors.New("limit_usd is not a number")
	}
	if r.Sign() < 0 {
		return 0, errors.New("limit_usd must not be negative")
	}
	r.Mul(r, big.NewRat(1_000_000, 1))
	if !r.IsInt() {
		return 0, errors.New("limit_usd has more than 6 decimal places")
	}
	if !r.Num().IsInt64() {
		return 0, errors.New("limit_usd is too large")
	}
	micros := r.Num().Int64()
	if micros > maxTopupMicros {
		return 0, errors.New("limit_usd exceeds the per-request limit")
	}
	return micros, nil
}

type adjustLimitRequest struct {
	LimitUSD       json.Number `json:"limit_usd"`
	Reason         string      `json:"reason"`
	IdempotencyKey string      `json:"idempotency_key"`
}

// adminAdjustKeyLimit sets an API key's budget limit to an absolute USD value.
// It exists to correct operator mistakes (e.g. undoing an over-sized top-up) and
// records every change in the append-only key_limit_adjustments ledger.
func (s *Server) adminAdjustKeyLimit(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "id")
	key, err := s.identity.Get(r.Context(), keyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	var body adjustLimitRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	newLimitMicros, err := usdLimitToMicros(body.LimitUSD)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	idem := strings.TrimSpace(body.IdempotencyKey)
	if idem == "" {
		idem = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	// A reason is mandatory here: an adjustment is a manual correction and must
	// be attributable, unlike an ordinary top-up.
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	if len(reason) > 500 {
		writeError(w, http.StatusBadRequest, "reason must be 500 characters or fewer")
		return
	}

	ctx := r.Context()

	// Fast replay path: an already-used idempotency key returns the recorded
	// adjustment without touching the budget again.
	if idem != "" {
		if existing, gerr := s.db.LimitAdjustments().GetByIdempotencyKey(ctx, key.ID, idem); gerr == nil {
			budget, berr := s.resolveTopupBudget(ctx, key.ID)
			if berr != nil {
				writeError(w, http.StatusInternalServerError, sanitizeError(s.log, berr, "internal server error"))
				return
			}
			s.writeAdjustmentResponse(w, http.StatusOK, existing, budget)
			return
		} else if !errors.Is(gerr, store.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, gerr, "internal server error"))
			return
		}
	}

	tx, err := s.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	defer func() { _ = tx.Rollback() }()

	// Same per-key advisory lock as top-up: a correction cannot race a top-up
	// (or another correction) into a lost read-then-write.
	if err := s.budgets.LockKeyTopup(ctx, tx, key.ID); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	var budget store.Budget
	var before, after int64
	{
		budgets, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, key.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
		if len(budgets) == 0 {
			// No budget yet: create it at the requested limit. before=0 because
			// the key had no credit before this adjustment.
			now := time.Now()
			budget = store.Budget{
				ID: uuid.NewString(), TenantID: adminTenant, ScopeKind: store.ScopeAPIKey,
				ScopeID: key.ID, LimitMicros: newLimitMicros, Period: "total", AlertPct: 80,
				HardCutoff: true, CreatedAt: now, UpdatedAt: now,
			}
			if err := s.budgets.CreateOnTx(ctx, tx, budget); err != nil {
				writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
				return
			}
			before, after = 0, newLimitMicros
		} else {
			budget = budgets[0]
			before, after, err = s.budgets.SetLimitOnTx(ctx, tx, budget.ID, newLimitMicros)
			if err != nil {
				switch {
				case errors.Is(err, store.ErrNotFound):
					writeError(w, http.StatusConflict, "budget changed concurrently; retry")
				case errors.Is(err, store.ErrInvalidLimit):
					writeError(w, http.StatusBadRequest, "limit must not be negative")
				default:
					writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
				}
				return
			}
			if after == before {
				// A concurrent request may have already applied this exact
				// idempotency key and set the limit to the value we wanted;
				// re-check (READ COMMITTED sees the winner's committed row)
				// so a retry replays instead of erroring.
				if idem != "" {
					if existing, gerr := s.db.LimitAdjustments().GetByIdempotencyKey(ctx, key.ID, idem); gerr == nil {
						budget.LimitMicros = existing.LimitAfterMicros
						s.writeAdjustmentResponse(w, http.StatusOK, existing, budget)
						return
					}
				}
				writeError(w, http.StatusBadRequest, "limit is unchanged")
				return
			}
		}
	}

	rec := store.KeyLimitAdjustment{
		ID: uuid.NewString(), TenantID: adminTenant, KeyID: key.ID, BudgetID: budget.ID,
		DeltaMicros: after - before, Reason: reason,
		LimitBeforeMicros: before, LimitAfterMicros: after,
		IdempotencyKey: idem, Actor: "dashboard", CreatedAt: time.Now(),
	}
	if err := s.db.LimitAdjustments().CreateOnTx(ctx, tx, rec); err != nil {
		// A unique-index violation means a concurrent request already applied
		// this idempotency key; return the winner instead of applying twice.
		if idem != "" {
			if existing, gerr := s.db.LimitAdjustments().GetByIdempotencyKey(ctx, key.ID, idem); gerr == nil {
				budget.LimitMicros = existing.LimitAfterMicros
				s.writeAdjustmentResponse(w, http.StatusOK, existing, budget)
				return
			}
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if s.budgetEngine != nil {
		s.budgetEngine.InvalidateBudgetCacheForScope(store.ScopeAPIKey, key.ID)
	}
	budget.LimitMicros = after
	s.writeAdjustmentResponse(w, http.StatusCreated, rec, budget)
}

func (s *Server) writeAdjustmentResponse(w http.ResponseWriter, status int, rec store.KeyLimitAdjustment, budget store.Budget) {
	writeJSON(w, status, map[string]any{
		"adjustment": map[string]any{
			"id":               rec.ID,
			"key_id":           rec.KeyID,
			"delta_usd":        float64(rec.DeltaMicros) / 1_000_000,
			"reason":           rec.Reason,
			"limit_before_usd": float64(rec.LimitBeforeMicros) / 1_000_000,
			"limit_after_usd":  float64(rec.LimitAfterMicros) / 1_000_000,
			"created_at":       rec.CreatedAt,
		},
		"budget": map[string]any{
			"id":           budget.ID,
			"limit_micros": budget.LimitMicros,
			"period":       budget.Period,
			"hard_cutoff":  budget.HardCutoff,
		},
	})
}

func (s *Server) adminListKeyLimitAdjustments(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "id")
	if _, err := s.identity.Get(r.Context(), keyID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	adjustments, err := s.db.LimitAdjustments().ListByKey(r.Context(), keyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(adjustments))
	for _, a := range adjustments {
		out = append(out, map[string]any{
			"id": a.ID, "delta_usd": float64(a.DeltaMicros) / 1_000_000,
			"reason": a.Reason, "limit_before_usd": float64(a.LimitBeforeMicros) / 1_000_000,
			"limit_after_usd": float64(a.LimitAfterMicros) / 1_000_000, "created_at": a.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"adjustments": out})
}
