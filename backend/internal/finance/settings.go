package finance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// AlertSettingsKey is the settings key holding AlertSettings as JSON.
const AlertSettingsKey = "finance_alerts"

// AlertSettings configures low-balance alerts for upstream accounts.
type AlertSettings struct {
	Enabled           bool
	DiscordWebhookURL string
}

// SecretBox seals and opens secrets with the master key (*crypto.Sealer).
type SecretBox interface {
	SealString(string) (crypto.Sealed, error)
	OpenString(crypto.Sealed) (string, error)
}

// storedAlertSettings is the persisted form. The webhook URL carries its
// token, so it is sealed; a {wrapped_dek, ciphertext} object is also what the
// full backup re-keys to the backup passphrase. LegacyURL is the plaintext
// field written before sealing existed: read, never written.
type storedAlertSettings struct {
	Enabled        bool           `json:"enabled"`
	DiscordWebhook *crypto.Sealed `json:"discord_webhook,omitempty"`
	LegacyURL      string         `json:"discord_webhook_url,omitempty"`
}

// LoadAlertSettings reads the alert settings; a missing key yields the zero
// value. box may be nil only when no sealed webhook is stored.
func LoadAlertSettings(ctx context.Context, get func(context.Context, string) (string, error), box SecretBox) (AlertSettings, error) {
	raw, err := get(ctx, AlertSettingsKey)
	if errors.Is(err, store.ErrNotFound) || (err == nil && raw == "") {
		return AlertSettings{}, nil
	}
	if err != nil {
		return AlertSettings{}, err
	}
	var st storedAlertSettings
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return AlertSettings{}, fmt.Errorf("finance: decode alert settings: %w", err)
	}
	s := AlertSettings{Enabled: st.Enabled, DiscordWebhookURL: st.LegacyURL}
	if st.DiscordWebhook != nil {
		if box == nil {
			return AlertSettings{}, errors.New("finance: webhook is sealed but no sealer is configured")
		}
		u, err := box.OpenString(*st.DiscordWebhook)
		if err != nil {
			return AlertSettings{}, errors.New("finance: cannot open the sealed webhook (master key mismatch?)")
		}
		s.DiscordWebhookURL = u
	}
	return s, nil
}

// SaveAlertSettings persists the alert settings, sealing the webhook URL.
func SaveAlertSettings(ctx context.Context, set func(context.Context, string, string) error, box SecretBox, s AlertSettings) error {
	st := storedAlertSettings{Enabled: s.Enabled}
	if s.DiscordWebhookURL != "" {
		if box == nil {
			return errors.New("finance: cannot store the webhook without a sealer")
		}
		sealed, err := box.SealString(s.DiscordWebhookURL)
		if err != nil {
			return fmt.Errorf("finance: seal webhook: %w", err)
		}
		st.DiscordWebhook = &sealed
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return set(ctx, AlertSettingsKey, string(raw))
}

var discordWebhookPrefixes = []string{
	"https://discord.com/api/webhooks/",
	"https://discordapp.com/api/webhooks/",
}

func splitDiscordWebhook(u string) (prefix, rest string, ok bool) {
	for _, p := range discordWebhookPrefixes {
		if strings.HasPrefix(u, p) {
			return p, strings.TrimPrefix(u, p), true
		}
	}
	return "", "", false
}

// ValidateDiscordWebhook accepts only Discord webhook URLs with an id and token.
func ValidateDiscordWebhook(u string) error {
	_, rest, ok := splitDiscordWebhook(u)
	if !ok {
		return errors.New("webhook URL must start with https://discord.com/api/webhooks/ or https://discordapp.com/api/webhooks/")
	}
	id, token, _ := strings.Cut(rest, "/")
	if id == "" || token == "" || strings.ContainsAny(u, " \t\r\n") {
		return errors.New("webhook URL must include the webhook id and token")
	}
	return nil
}

// MaskDiscordWebhook keeps the URL up to the webhook id and hides the token.
func MaskDiscordWebhook(u string) string {
	if u == "" {
		return ""
	}
	prefix, rest, ok := splitDiscordWebhook(u)
	if !ok {
		return "****"
	}
	id, _, _ := strings.Cut(rest, "/")
	return prefix + id + "/****"
}
