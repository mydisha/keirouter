package gateway

import (
	"context"
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

// maxTopupMicros caps a single top-up at 1,000,000 USD to bound operator error.
const maxTopupMicros int64 = 1_000_000 * 1_000_000

// usdToMicros converts a JSON decimal amount into integer micro-USD without
// ever going through float64, so no rounding drift can enter the ledger.
func usdToMicros(n json.Number) (int64, error) {
	r, ok := new(big.Rat).SetString(n.String())
	if !ok {
		return 0, errors.New("amount_usd is not a number")
	}
	if r.Sign() <= 0 {
		return 0, errors.New("amount_usd must be positive")
	}
	r.Mul(r, big.NewRat(1_000_000, 1))
	if !r.IsInt() {
		return 0, errors.New("amount_usd has more than 6 decimal places")
	}
	if !r.Num().IsInt64() {
		return 0, errors.New("amount_usd is too large")
	}
	micros := r.Num().Int64()
	if micros > maxTopupMicros {
		return 0, errors.New("amount_usd exceeds the per-request limit")
	}
	return micros, nil
}

type topupRequest struct {
	AmountUSD      json.Number `json:"amount_usd"`
	Reason         string      `json:"reason"`
	IdempotencyKey string      `json:"idempotency_key"`
}

func (s *Server) adminTopupKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == s.bansosKeyID(r.Context()) {
		writeError(w, http.StatusBadRequest, "the bansos key is managed from the Bansos page")
		return
	}
	s.topupKeyCore(w, r, id)
}

// topupKeyCore credits a key's api_key budget and records the ledger row. It is
// shared by the generic key endpoint and the bansos endpoint.
func (s *Server) topupKeyCore(w http.ResponseWriter, r *http.Request, keyID string) {
	key, err := s.identity.Get(r.Context(), keyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	var body topupRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	amountMicros, err := usdToMicros(body.AmountUSD)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	idem := strings.TrimSpace(body.IdempotencyKey)
	if idem == "" {
		idem = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	reason := strings.TrimSpace(body.Reason)
	if len(reason) > 500 {
		writeError(w, http.StatusBadRequest, "reason must be 500 characters or fewer")
		return
	}

	ctx := r.Context()

	// Fast replay path: if this idempotency key was already used, return it.
	if idem != "" {
		if existing, gerr := s.db.Topups().GetByIdempotencyKey(ctx, key.ID, idem); gerr == nil {
			budget, berr := s.resolveTopupBudget(ctx, key.ID)
			if berr != nil {
				writeError(w, http.StatusInternalServerError, sanitizeError(s.log, berr, "internal server error"))
				return
			}
			s.writeTopupResponse(w, http.StatusOK, existing, budget)
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

	// Serialize all top-ups for this key so concurrent requests cannot race on
	// budget creation or on the read-then-write limit snapshot. On Postgres
	// this takes a transaction-scoped advisory lock; on SQLite the write lock
	// already serializes.
	if err := s.budgets.LockKeyTopup(ctx, tx, key.ID); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	// Resolve the key's api_key budget, creating a total-period one if absent.
	var budget store.Budget
	{
		budgets, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, key.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
		if len(budgets) == 0 {
			now := time.Now()
			budget = store.Budget{
				ID: uuid.NewString(), TenantID: adminTenant, ScopeKind: store.ScopeAPIKey,
				ScopeID: key.ID, LimitMicros: 0, Period: "total", AlertPct: 80,
				HardCutoff: true, CreatedAt: now, UpdatedAt: now,
			}
			if err := s.budgets.CreateOnTx(ctx, tx, budget); err != nil {
				writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
				return
			}
		} else {
			budget = budgets[0]
		}
	}

	// Atomic increment; overflow is detected under the row lock before writing.
	before, after, err := s.budgets.IncrementLimitOnTx(ctx, tx, budget.ID, amountMicros)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusConflict, "budget changed concurrently; retry")
		case errors.Is(err, store.ErrLimitOverflow):
			writeError(w, http.StatusBadRequest, "top-up would overflow the budget limit")
		default:
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		}
		return
	}

	rec := store.KeyTopup{
		ID: uuid.NewString(), TenantID: adminTenant, KeyID: key.ID, BudgetID: budget.ID,
		AmountMicros: amountMicros, Reason: reason,
		LimitBeforeMicros: before, LimitAfterMicros: after,
		IdempotencyKey: idem, Actor: "dashboard", CreatedAt: time.Now(),
	}
	if err := s.db.Topups().CreateOnTx(ctx, tx, rec); err != nil {
		// A unique-index violation means a concurrent request already applied
		// this idempotency key. Return the winner instead of double-crediting.
		if idem != "" {
			if existing, gerr := s.db.Topups().GetByIdempotencyKey(ctx, key.ID, idem); gerr == nil {
				// Report the winner's committed limit, not our pre-increment
				// snapshot: the returned row carries the value it persisted.
				budget.LimitMicros = existing.LimitAfterMicros
				s.writeTopupResponse(w, http.StatusOK, existing, budget)
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
	// Report the post-increment limit for the create path.
	budget.LimitMicros = after
	s.writeTopupResponse(w, http.StatusCreated, rec, budget)
}

// resolveTopupBudget returns the key's current api_key budget. Callers use it
// on replay paths where the budget was not resolved within an open transaction.
func (s *Server) resolveTopupBudget(ctx context.Context, keyID string) (store.Budget, error) {
	budgets, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, keyID)
	if err != nil {
		return store.Budget{}, err
	}
	if len(budgets) == 0 {
		return store.Budget{}, store.ErrNotFound
	}
	return budgets[0], nil
}

func (s *Server) writeTopupResponse(w http.ResponseWriter, status int, rec store.KeyTopup, budget store.Budget) {
	writeJSON(w, status, map[string]any{
		"topup": map[string]any{
			"id":               rec.ID,
			"key_id":           rec.KeyID,
			"amount_usd":       float64(rec.AmountMicros) / 1_000_000,
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

func (s *Server) adminListKeyTopups(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "id")
	if _, err := s.identity.Get(r.Context(), keyID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	topups, err := s.db.Topups().ListByKey(r.Context(), keyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	out := make([]map[string]any, 0, len(topups))
	for _, t := range topups {
		out = append(out, map[string]any{
			"id": t.ID, "amount_usd": float64(t.AmountMicros) / 1_000_000,
			"reason": t.Reason, "limit_before_usd": float64(t.LimitBeforeMicros) / 1_000_000,
			"limit_after_usd": float64(t.LimitAfterMicros) / 1_000_000, "created_at": t.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"topups": out})
}
