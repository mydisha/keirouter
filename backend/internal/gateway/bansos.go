package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/budget"
	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// bansosSettingsKey is the settings-store key holding the single bansos config.
const bansosSettingsKey = "bansos"

const (
	bansosModeUnlimited = "unlimited"
	bansosModeCredit    = "credit"
)

// bansosConfig is the persisted bansos state. It references a dedicated API key
// plus the resources that key uses (plan, budget, model access). SealedKey is
// the envelope-encrypted plaintext so the public page can reveal it.
type bansosConfig struct {
	KeyID         string        `json:"key_id"`
	PlanID        string        `json:"plan_id"`
	Active        bool          `json:"active"`
	Mode          string        `json:"mode"`
	SealedKey     crypto.Sealed `json:"sealed_key"`
	MaskedDisplay string        `json:"masked_display"`
	AllowedModels []string      `json:"allowed_models"`
	RPM           int64         `json:"rpm"`
	TPM           int64         `json:"tpm"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// loadBansos reads the bansos config. ok=false when never configured.
func (s *Server) loadBansos(ctx context.Context) (bansosConfig, bool, error) {
	if s.settings == nil {
		return bansosConfig{}, false, errors.New("settings store not configured")
	}
	raw, err := s.settings.Get(ctx, bansosSettingsKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return bansosConfig{}, false, nil
		}
		return bansosConfig{}, false, err
	}
	var cfg bansosConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return bansosConfig{}, false, err
	}
	return cfg, true, nil
}

// saveBansos persists the bansos config.
func (s *Server) saveBansos(ctx context.Context, cfg bansosConfig) error {
	if s.settings == nil {
		return errors.New("settings store not configured")
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, bansosSettingsKey, string(raw))
}

// bansosKeyID returns the bansos API key id, or "" when unconfigured.
func (s *Server) bansosKeyID(ctx context.Context) string {
	cfg, ok, err := s.loadBansos(ctx)
	if err != nil || !ok {
		return ""
	}
	return cfg.KeyID
}

// bansosCreateRequest is the POST /api/bansos body.
type bansosCreateRequest struct {
	Mode           string       `json:"mode"`
	AllowedModels  []string     `json:"allowed_models"`
	RPM            int64        `json:"rpm"`
	TPM            int64        `json:"tpm"`
	CreditLimitUSD *json.Number `json:"credit_limit_usd"`
}

// bansosCreditView reports the key's api_key budget balances, or null when the
// bansos has no budget (unlimited).
func (s *Server) bansosCreditView(ctx context.Context, keyID string) (map[string]any, error) {
	if keyID == "" {
		return nil, nil
	}
	budgets, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, keyID)
	if err != nil {
		return nil, err
	}
	if len(budgets) == 0 {
		return nil, nil
	}
	b := budgets[0]
	since := budget.PeriodStart(b.Period, time.Now())
	costMicros, _, err := s.usage.SpendAndTokens(ctx, b.ScopeKind, b.ScopeID, since)
	if err != nil {
		return nil, err
	}
	remaining := b.LimitMicros - costMicros
	if remaining < 0 {
		remaining = 0
	}
	return map[string]any{
		"limit_usd":     float64(b.LimitMicros) / 1_000_000,
		"spent_usd":     float64(costMicros) / 1_000_000,
		"remaining_usd": float64(remaining) / 1_000_000,
		"period":        b.Period,
	}, nil
}

// bansosStatePayload builds the shared (plaintext-free) state shape.
func (s *Server) bansosStatePayload(ctx context.Context, cfg bansosConfig) (map[string]any, error) {
	credit, err := s.bansosCreditView(ctx, cfg.KeyID)
	if err != nil {
		return nil, err
	}
	models := cfg.AllowedModels
	if models == nil {
		models = []string{}
	}
	return map[string]any{
		"exists":         true,
		"key_id":         cfg.KeyID,
		"plan_id":        cfg.PlanID,
		"active":         cfg.Active,
		"mode":           cfg.Mode,
		"masked_display": cfg.MaskedDisplay,
		"allowed_models": models,
		"rpm":            cfg.RPM,
		"tpm":            cfg.TPM,
		"credit":         credit,
		"updated_at":     cfg.UpdatedAt,
	}, nil
}

func (s *Server) adminGetBansos(w http.ResponseWriter, r *http.Request) {
	cfg, ok, err := s.loadBansos(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"exists": false, "active": false})
		return
	}
	payload, err := s.bansosStatePayload(r.Context(), cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) adminCreateBansos(w http.ResponseWriter, r *http.Request) {
	if _, ok, err := s.loadBansos(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	} else if ok {
		writeError(w, http.StatusConflict, "bansos already exists")
		return
	}
	var body bansosCreateRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	models := normalizeModelPatterns(body.AllowedModels)
	if len(models) == 0 {
		writeError(w, http.StatusBadRequest, "at least one allowed model is required")
		return
	}
	mode := body.Mode
	if mode == "" {
		mode = bansosModeUnlimited
	}
	if mode != bansosModeUnlimited && mode != bansosModeCredit {
		writeError(w, http.StatusBadRequest, "mode must be credit or unlimited")
		return
	}
	if body.RPM < 0 || body.TPM < 0 {
		writeError(w, http.StatusBadRequest, "rate limits must not be negative")
		return
	}
	var creditMicros int64
	if mode == bansosModeCredit {
		if body.CreditLimitUSD == nil {
			writeError(w, http.StatusBadRequest, "credit_limit_usd is required in credit mode")
			return
		}
		m, err := usdLimitToMicros(*body.CreditLimitUSD)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		creditMicros = m
	}

	issued, err := s.identity.Generate(adminTenant, "", "bansos")
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	sealed, err := s.vault.Sealer().SealString(issued.Plaintext)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}

	now := time.Now()
	plan := store.Plan{
		ID: uuid.NewString(), TenantID: adminTenant, Name: "bansos",
		Description: "Bansos rate limits", RPMLimit: body.RPM, TPMLimit: body.TPM,
		Period: "total", AlertPct: 80, HardCutoff: true, CreatedAt: now, UpdatedAt: now,
	}
	issued.Record.PlanID = plan.ID
	issued.Record.Disabled = true

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "transaction start failed")
		return
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.db.Plans().CreateOnTx(r.Context(), tx, plan); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if err := s.identity.Keys().CreateOnTx(r.Context(), tx, issued.Record); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if err := s.identity.Keys().SetAllowedModelsOnTx(r.Context(), tx, issued.Record.ID, models); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if mode == bansosModeCredit {
		if err := s.budgets.CreateOnTx(r.Context(), tx, store.Budget{
			ID: uuid.NewString(), TenantID: adminTenant, ScopeKind: store.ScopeAPIKey,
			ScopeID: issued.Record.ID, LimitMicros: creditMicros, Period: "total",
			AlertPct: 80, HardCutoff: true, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "transaction commit failed")
		return
	}

	cfg := bansosConfig{
		KeyID: issued.Record.ID, PlanID: plan.ID, Active: false, Mode: mode,
		SealedKey: sealed, MaskedDisplay: issued.Record.Display,
		AllowedModels: models, RPM: body.RPM, TPM: body.TPM,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.saveBansos(r.Context(), cfg); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if s.budgetEngine != nil {
		s.budgetEngine.InvalidateBudgetCache()
	}
	s.identity.InvalidateAuthCacheForKey(issued.Record.ID)

	payload, err := s.bansosStatePayload(r.Context(), cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	payload["key"] = issued.Plaintext
	writeJSON(w, http.StatusCreated, payload)
}

// adminUpdateBansos patches the bansos config. All fields are optional; an
// omitted field is left unchanged. The resulting state must never be active
// with zero allowed models, so activation and model clearing are validated
// together as a fail-closed invariant before any mutation.
func (s *Server) adminUpdateBansos(w http.ResponseWriter, r *http.Request) {
	cfg, ok, err := s.loadBansos(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "bansos not configured")
		return
	}
	var body struct {
		Active         *bool        `json:"active"`
		Mode           *string      `json:"mode"`
		AllowedModels  *[]string    `json:"allowed_models"`
		RPM            *int64       `json:"rpm"`
		TPM            *int64       `json:"tpm"`
		CreditLimitUSD *json.Number `json:"credit_limit_usd"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	ctx := r.Context()

	effectiveActive := cfg.Active
	if body.Active != nil {
		effectiveActive = *body.Active
	}
	effectiveModels := cfg.AllowedModels
	if body.AllowedModels != nil {
		effectiveModels = normalizeModelPatterns(*body.AllowedModels)
	}
	if effectiveActive && len(effectiveModels) == 0 {
		writeError(w, http.StatusBadRequest, "at least one allowed model is required")
		return
	}

	if body.AllowedModels != nil {
		if err := s.identity.Keys().SetAllowedModels(ctx, cfg.KeyID, effectiveModels); err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
		cfg.AllowedModels = effectiveModels
	}

	if body.RPM != nil || body.TPM != nil || body.Mode != nil {
		plan, err := s.db.Plans().Get(ctx, cfg.PlanID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
		if body.RPM != nil {
			if *body.RPM < 0 {
				writeError(w, http.StatusBadRequest, "rpm must not be negative")
				return
			}
			plan.RPMLimit = *body.RPM
			cfg.RPM = *body.RPM
		}
		if body.TPM != nil {
			if *body.TPM < 0 {
				writeError(w, http.StatusBadRequest, "tpm must not be negative")
				return
			}
			plan.TPMLimit = *body.TPM
			cfg.TPM = *body.TPM
		}
		plan.UpdatedAt = time.Now()
		if err := s.db.Plans().Update(ctx, plan); err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
	}

	if body.Mode != nil {
		mode := *body.Mode
		if mode != bansosModeUnlimited && mode != bansosModeCredit {
			writeError(w, http.StatusBadRequest, "mode must be credit or unlimited")
			return
		}
		if mode != cfg.Mode {
			if mode == bansosModeUnlimited {
				budgets, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, cfg.KeyID)
				if err != nil {
					writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
					return
				}
				for _, b := range budgets {
					if err := s.budgets.Delete(ctx, b.ID); err != nil {
						writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
						return
					}
				}
			} else {
				var limitMicros int64
				if body.CreditLimitUSD != nil {
					m, err := usdLimitToMicros(*body.CreditLimitUSD)
					if err != nil {
						writeError(w, http.StatusBadRequest, err.Error())
						return
					}
					limitMicros = m
				}
				existing, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, cfg.KeyID)
				if err != nil {
					writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
					return
				}
				if len(existing) == 0 {
					now := time.Now()
					if err := s.budgets.Create(ctx, store.Budget{
						ID: uuid.NewString(), TenantID: adminTenant, ScopeKind: store.ScopeAPIKey,
						ScopeID: cfg.KeyID, LimitMicros: limitMicros, Period: "total",
						AlertPct: 80, HardCutoff: true, CreatedAt: now, UpdatedAt: now,
					}); err != nil {
						writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
						return
					}
				}
			}
			cfg.Mode = mode
		}
	} else if body.CreditLimitUSD != nil && cfg.Mode == bansosModeCredit {
		// Absolute limit edit while staying in credit mode.
		m, err := usdLimitToMicros(*body.CreditLimitUSD)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		budgets, err := s.budgets.ListByScope(ctx, store.ScopeAPIKey, cfg.KeyID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
		if len(budgets) > 0 {
			b := budgets[0]
			b.LimitMicros = m
			b.UpdatedAt = time.Now()
			if err := s.budgets.Update(ctx, b); err != nil {
				writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
				return
			}
		}
	}

	if body.Active != nil {
		if err := s.identity.SetDisabled(ctx, cfg.KeyID, !*body.Active); err != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
			return
		}
		cfg.Active = *body.Active
	}

	cfg.UpdatedAt = time.Now()
	if err := s.saveBansos(ctx, cfg); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if s.budgetEngine != nil {
		s.budgetEngine.InvalidateBudgetCacheForScope(store.ScopeAPIKey, cfg.KeyID)
	}
	payload, err := s.bansosStatePayload(ctx, cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func normalizeModelPatterns(in []string) []string {
	out := make([]string, 0, len(in))
	for _, m := range in {
		if t := strings.TrimSpace(m); t != "" {
			out = append(out, t)
		}
	}
	return out
}
