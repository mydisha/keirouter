package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/finance"
	"github.com/mydisha/keirouter/backend/internal/store"
)

const backupPass = "move-servers-2026"

// seedSealedSource stores one secret in each master-key-sealed location.
func seedSealedSource(t *testing.T, s *Server) {
	t.Helper()
	ctx := context.Background()
	sealer := s.vault.Sealer()
	sec, err := sealer.SealString("sk-surplus-upstream")
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, s.db.Accounts().Create(ctx, store.Account{
		ID: "acc1", TenantID: store.DefaultTenantID, Provider: "custom-openai-surplus", Label: "Surplus-1",
		AuthKind: "api_key", SecretWrappedDEK: sec.WrappedDEK, SecretCiphertext: sec.Ciphertext,
		Metadata: "{}", Priority: 100, CreatedAt: now, UpdatedAt: now,
	}))
	issued, err := s.identity.Create(ctx, store.DefaultTenantID, "", "portal-user-key")
	require.NoError(t, err)
	pk, err := sealer.SealString(issued.Plaintext)
	require.NoError(t, err)
	require.NoError(t, s.db.PortalUsers().Upsert(ctx, store.PortalUser{
		GoogleSub: "sub-1", Email: "u@example.com", KeyID: issued.Record.ID, SealedKey: pk, CreatedAt: now, UpdatedAt: now,
	}))
	bk, err := sealer.SealString("kr_bansos_plain")
	require.NoError(t, err)
	raw, _ := json.Marshal(map[string]any{"key_id": "b1", "sealed_key": bk, "notice_rate": 30})
	require.NoError(t, s.settings.Set(ctx, bansosSettingsKey, string(raw)))
}

func doBackup(t *testing.T, s *Server, pass string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"passphrase": pass})
	rec := httptest.NewRecorder()
	s.adminFullBackup(rec, httptest.NewRequest(http.MethodPost, "/api/settings/backup", bytes.NewReader(body)))
	return rec
}

func doRestore(t *testing.T, s *Server, file []byte, pass string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/restore", bytes.NewReader(file))
	req.Header.Set("X-Backup-Passphrase", pass)
	rec := httptest.NewRecorder()
	s.adminFullRestore(rec, req)
	return rec
}

func TestFullBackupRestoreAcrossMasterKeys(t *testing.T) {
	src := newPortalTestServer(t)
	seedSealedSource(t, src)

	rec := doBackup(t, src, backupPass)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	file := rec.Body.Bytes()
	for _, leak := range []string{"sk-surplus-upstream", "kr_bansos_plain"} {
		require.NotContains(t, string(file), leak, "plaintext secret leaked into the backup")
	}

	dst := newPortalTestServer(t) // fresh master key
	rec = doRestore(t, dst, file, backupPass)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"restart_required":true`)

	ctx := context.Background()
	acc, err := dst.db.Accounts().Get(ctx, "acc1")
	require.NoError(t, err)
	plain, err := dst.vault.Sealer().OpenString(crypto.Sealed{WrappedDEK: acc.SecretWrappedDEK, Ciphertext: acc.SecretCiphertext})
	require.NoError(t, err, "account secret must be re-sealed under the target master key")
	require.Equal(t, "sk-surplus-upstream", plain)

	pu, err := dst.db.PortalUsers().GetBySub(ctx, "sub-1")
	require.NoError(t, err)
	revealed, err := dst.vault.Sealer().OpenString(pu.SealedKey)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(revealed, "kr_"))
	// The restored key hash still authenticates the same plaintext key.
	_, err = dst.identity.Authenticate(ctx, revealed)
	require.NoError(t, err, "user API key must keep working after restore")

	cfg, err := dst.settings.Get(ctx, bansosSettingsKey)
	require.NoError(t, err)
	var b struct {
		SealedKey  crypto.Sealed `json:"sealed_key"`
		NoticeRate int           `json:"notice_rate"`
	}
	require.NoError(t, json.Unmarshal([]byte(cfg), &b))
	bp, err := dst.vault.Sealer().OpenString(b.SealedKey)
	require.NoError(t, err)
	require.Equal(t, "kr_bansos_plain", bp)
	require.Equal(t, 30, b.NoticeRate)
}

func TestFullRestoreWrongPassphraseChangesNothing(t *testing.T) {
	src := newPortalTestServer(t)
	seedSealedSource(t, src)
	file := doBackup(t, src, backupPass).Body.Bytes()

	dst := newPortalTestServer(t)
	rec := doRestore(t, dst, file, "not-the-passphrase")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	_, err := dst.db.Accounts().Get(context.Background(), "acc1")
	require.Error(t, err, "nothing may be restored with a wrong passphrase")
}

func TestFullBackupRequiresStrongPassphrase(t *testing.T) {
	s := newPortalTestServer(t)
	require.Equal(t, http.StatusBadRequest, doBackup(t, s, "").Code)
	require.Equal(t, http.StatusBadRequest, doBackup(t, s, "short").Code)
}

// The finance alert webhook carries its token, so it must leave the backup
// only re-sealed under the passphrase and come back under the target key.
func TestFullBackupKeepsFinanceWebhookSealed(t *testing.T) {
	src := newPortalTestServer(t)
	ctx := context.Background()
	const hook = "https://discord.com/api/webhooks/42/WEBHOOKSECRETTOKEN"
	require.NoError(t, finance.SaveAlertSettings(ctx, src.settings.Set, src.financeBox(),
		finance.AlertSettings{Enabled: true, DiscordWebhookURL: hook}))

	rec := doBackup(t, src, backupPass)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	file := rec.Body.Bytes()
	require.NotContains(t, string(file), "WEBHOOKSECRETTOKEN", "webhook token leaked into the backup")
	require.NotContains(t, string(file), "discord.com/api/webhooks", "webhook URL leaked into the backup")

	dst := newPortalTestServer(t) // fresh master key
	rec = doRestore(t, dst, file, backupPass)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	got, err := finance.LoadAlertSettings(ctx, dst.settings.Get, dst.financeBox())
	require.NoError(t, err, "webhook must be re-sealed under the target master key")
	require.Equal(t, hook, got.DiscordWebhookURL)
	require.True(t, got.Enabled)
}
