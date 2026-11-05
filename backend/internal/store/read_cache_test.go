package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSettingsGetSeesOwnWritesImmediately(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := db.Settings()

	_, err := repo.Get(ctx, "endpoint.settings")
	require.ErrorIs(t, err, ErrNotFound) // caches the miss

	require.NoError(t, repo.Set(ctx, "endpoint.settings", `{"a":1}`))
	v, err := repo.Get(ctx, "endpoint.settings")
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, v)

	// A second repo value shares the DB, and so the cache.
	require.NoError(t, db.Settings().Set(ctx, "endpoint.settings", `{"a":"ünïcode 🔑"}`))
	v, err = repo.Get(ctx, "endpoint.settings")
	require.NoError(t, err)
	require.Equal(t, `{"a":"ünïcode 🔑"}`, v)

	require.NoError(t, repo.Delete(ctx, "endpoint.settings"))
	_, err = repo.Get(ctx, "endpoint.settings")
	require.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, repo.Set(ctx, "empty", ""))
	v, err = repo.Get(ctx, "empty")
	require.NoError(t, err, "an empty value is a hit, not a miss")
	require.Empty(t, v)
}

func TestSettingsRawWriteVisibleAfterInvalidateOrTTL(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := db.Settings()
	require.NoError(t, repo.Set(ctx, "routing.x", "old"))
	_, err := repo.Get(ctx, "routing.x")
	require.NoError(t, err)

	// A write that bypasses SettingsRepo, like another replica would make.
	_, err = db.sql.ExecContext(ctx, db.rebind(`UPDATE settings SET value = ? WHERE key = ?`), "new", "routing.x")
	require.NoError(t, err)

	v, _ := repo.Get(ctx, "routing.x")
	require.Equal(t, "old", v, "served from cache within the TTL")

	db.InvalidateSettingsCache()
	v, _ = repo.Get(ctx, "routing.x")
	require.Equal(t, "new", v)

	_, err = db.sql.ExecContext(ctx, db.rebind(`UPDATE settings SET value = ? WHERE key = ?`), "newer", "routing.x")
	require.NoError(t, err)
	db.settingsCache.mu.Lock()
	e := db.settingsCache.entries["routing.x"]
	e.expires = time.Now().Add(-time.Millisecond)
	db.settingsCache.entries["routing.x"] = e
	db.settingsCache.mu.Unlock()
	v, _ = repo.Get(ctx, "routing.x")
	require.Equal(t, "newer", v, "an expired entry is re-read")
}

func TestReadCacheReadRacingWriteIsNotStored(t *testing.T) {
	c := newSettingsCache()
	now := time.Now()
	_, hit, gen := c.get("k", now) // a reader starts and queries the DB...
	require.False(t, hit)
	c.invalidate("k") // ...a write lands meanwhile...
	c.put("k", settingValue{value: "stale", found: true}, gen, now)
	_, hit, _ = c.get("k", now)
	require.False(t, hit, "the value read before the write must not be cached")
}

func TestReadCacheBounded(t *testing.T) {
	c := newSettingsCache()
	now := time.Now()
	for i := range c.maxEntries + 10 {
		_, _, gen := c.get("k", now)
		c.put(time.Duration(i).String(), settingValue{found: true}, gen, now)
	}
	require.LessOrEqual(t, len(c.entries), c.maxEntries)
}
