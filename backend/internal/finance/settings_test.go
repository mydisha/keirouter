package finance

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

const testWebhook = "https://discord.com/api/webhooks/123/SECRETTOKEN"

func testSealer(t *testing.T) *crypto.Sealer {
	t.Helper()
	mk, err := crypto.GenerateMasterKey()
	require.NoError(t, err)
	s, err := crypto.NewSealer(mk)
	require.NoError(t, err)
	return s
}

type memSettings map[string]string

func (m memSettings) get(_ context.Context, k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", store.ErrNotFound
	}
	return v, nil
}

func (m memSettings) set(_ context.Context, k, v string) error { m[k] = v; return nil }

func TestAlertSettings_WebhookStoredSealed(t *testing.T) {
	ctx := context.Background()
	box := testSealer(t)
	m := memSettings{}
	require.NoError(t, SaveAlertSettings(ctx, m.set, box, AlertSettings{Enabled: true, DiscordWebhookURL: testWebhook}))

	raw := m[AlertSettingsKey]
	require.NotContains(t, raw, "SECRETTOKEN")
	require.NotContains(t, raw, "discord.com")
	require.Contains(t, raw, `"wrapped_dek"`, "backup re-keys settings objects carrying wrapped_dek + ciphertext")

	got, err := LoadAlertSettings(ctx, m.get, box)
	require.NoError(t, err)
	require.Equal(t, AlertSettings{Enabled: true, DiscordWebhookURL: testWebhook}, got)
}

func TestAlertSettings_LegacyPlaintextStillLoads(t *testing.T) {
	ctx := context.Background()
	m := memSettings{AlertSettingsKey: `{"enabled":true,"discord_webhook_url":"` + testWebhook + `"}`}
	got, err := LoadAlertSettings(ctx, m.get, testSealer(t))
	require.NoError(t, err)
	require.Equal(t, testWebhook, got.DiscordWebhookURL)
	require.True(t, got.Enabled)
}

func TestAlertSettings_EmptyWebhookNeedsNoSealer(t *testing.T) {
	ctx := context.Background()
	m := memSettings{}
	require.NoError(t, SaveAlertSettings(ctx, m.set, nil, AlertSettings{}))
	got, err := LoadAlertSettings(ctx, m.get, nil)
	require.NoError(t, err)
	require.Equal(t, AlertSettings{}, got)
}

func TestAlertSettings_SealedWithoutSealerFails(t *testing.T) {
	ctx := context.Background()
	m := memSettings{}
	require.NoError(t, SaveAlertSettings(ctx, m.set, testSealer(t), AlertSettings{DiscordWebhookURL: testWebhook}))
	_, err := LoadAlertSettings(ctx, m.get, nil)
	require.Error(t, err)
	require.Error(t, SaveAlertSettings(ctx, m.set, nil, AlertSettings{DiscordWebhookURL: testWebhook}))
}
