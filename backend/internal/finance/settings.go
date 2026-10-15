package finance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// AlertSettingsKey is the settings key holding AlertSettings as JSON.
const AlertSettingsKey = "finance_alerts"

// AlertSettings configures low-balance alerts for upstream accounts.
type AlertSettings struct {
	Enabled           bool   `json:"enabled"`
	DiscordWebhookURL string `json:"discord_webhook_url"`
}

// LoadAlertSettings reads the alert settings; a missing key yields the zero value.
func LoadAlertSettings(ctx context.Context, get func(context.Context, string) (string, error)) (AlertSettings, error) {
	raw, err := get(ctx, AlertSettingsKey)
	if errors.Is(err, store.ErrNotFound) || (err == nil && raw == "") {
		return AlertSettings{}, nil
	}
	if err != nil {
		return AlertSettings{}, err
	}
	var s AlertSettings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return AlertSettings{}, fmt.Errorf("finance: decode alert settings: %w", err)
	}
	return s, nil
}

// SaveAlertSettings persists the alert settings.
func SaveAlertSettings(ctx context.Context, set func(context.Context, string, string) error, s AlertSettings) error {
	raw, err := json.Marshal(s)
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
