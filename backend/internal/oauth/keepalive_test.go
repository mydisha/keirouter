package oauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/mydisha/keirouter/backend/internal/vault"
)

func TestKeepAliveRefreshesNearExpiryAccountsWithBoundedParallelism(t *testing.T) {
	var calls, active, peak atomic.Int32
	m := &TokenManager{vault: &vault.Vault{}, accounts: &store.AccountRepo{}}
	m.refresh = func(_ context.Context, acc store.Account) (store.Account, error) {
		calls.Add(1)
		current := active.Add(1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
		return acc, nil
	}
	k := &KeepAlive{
		tokenMgr:    m,
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		maxParallel: 2,
	}

	near := time.Now().Add(10 * time.Minute)
	far := time.Now().Add(time.Hour)
	accounts := make([]store.Account, 0, 9)
	for i := 0; i < 6; i++ {
		accounts = append(accounts, store.Account{ID: string(rune('a' + i)), Provider: "kiro", AuthKind: store.AuthOAuth, TokenExpiresAt: &near})
	}
	accounts = append(accounts,
		store.Account{ID: "far", Provider: "kiro", AuthKind: store.AuthOAuth, TokenExpiresAt: &far},
		store.Account{ID: "unknown", Provider: "kiro", AuthKind: store.AuthOAuth},
		store.Account{ID: "disabled", Provider: "kiro", AuthKind: store.AuthOAuth, TokenExpiresAt: &near, Disabled: true},
	)

	refreshed, skipped, failed, reconnect := k.refreshAccounts(context.Background(), accounts)
	if refreshed != 6 || skipped != 3 || failed != 0 || reconnect != 0 {
		t.Fatalf("unexpected stats: refreshed=%d skipped=%d failed=%d reconnect=%d", refreshed, skipped, failed, reconnect)
	}
	if calls.Load() != 6 {
		t.Fatalf("refresh calls = %d, want 6", calls.Load())
	}
	if peak.Load() < 2 || peak.Load() > 2 {
		t.Fatalf("peak concurrency = %d, want exactly 2", peak.Load())
	}
}

func TestKeepAliveFailureDoesNotStopOtherAccounts(t *testing.T) {
	var calls atomic.Int32
	m := &TokenManager{vault: &vault.Vault{}, accounts: &store.AccountRepo{}}
	m.refresh = func(_ context.Context, acc store.Account) (store.Account, error) {
		calls.Add(1)
		if acc.ID == "bad" {
			return acc, context.DeadlineExceeded
		}
		return acc, nil
	}
	k := &KeepAlive{tokenMgr: m, log: slog.New(slog.NewTextHandler(io.Discard, nil)), maxParallel: 2}
	near := time.Now().Add(time.Minute)
	accounts := []store.Account{
		{ID: "bad", Provider: "kiro", AuthKind: store.AuthOAuth, TokenExpiresAt: &near},
		{ID: "good", Provider: "kiro", AuthKind: store.AuthOAuth, TokenExpiresAt: &near},
	}

	refreshed, _, failed, _ := k.refreshAccounts(context.Background(), accounts)
	if refreshed != 1 || failed != 1 || calls.Load() != 2 {
		t.Fatalf("unexpected outcome: refreshed=%d failed=%d calls=%d", refreshed, failed, calls.Load())
	}
}

func TestKeepAliveReportFailuresGroupsAndDedupes(t *testing.T) {
	var buf bytes.Buffer
	k := &KeepAlive{log: slog.New(slog.NewTextHandler(&buf, nil))}
	undecryptable := fmt.Errorf("oauth: no refresh token for account a1: vault: open refresh token: unwrap dek: %w", crypto.ErrMalformedCiphertext)
	fails := []refreshResult{
		{acc: store.Account{ID: "a1", Provider: "kiro"}, err: undecryptable},
		{acc: store.Account{ID: "a2", Provider: "qoder"}, err: fmt.Errorf("wrap: %w", crypto.ErrMalformedCiphertext)},
		{acc: store.Account{ID: "a3", Provider: "kiro"}, err: errors.New("oauth: refresh failed for account a3: invalid_grant")},
	}

	k.reportFailures(fails)
	out := buf.String()
	if got := strings.Count(out, "level=WARN"); got != 2 {
		t.Fatalf("want 2 grouped warnings, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "accounts=2") || !strings.Contains(out, "provider=kiro,qoder") {
		t.Fatalf("decrypt failures not grouped:\n%s", out)
	}
	if strings.Contains(out, "a3: invalid_grant") || !strings.Contains(out, "account=a3") {
		t.Fatalf("account id not stripped from cause:\n%s", out)
	}

	buf.Reset()
	k.reportFailures(fails)
	if !k.failedRepeated || buf.Len() != 0 {
		t.Fatalf("identical failure set should be demoted below info, got:\n%s", buf.String())
	}
}
