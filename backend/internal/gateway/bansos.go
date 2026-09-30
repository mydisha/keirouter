package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

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
