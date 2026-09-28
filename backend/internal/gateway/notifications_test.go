package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func newNotificationsGateway(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	t.Cleanup(func() { _ = db.Close() })
	idSvc := identity.New(db.APIKeys())
	return New(Deps{Config: config.Default(), DB: db, Settings: db.Settings(), Identity: idSvc})
}

func TestLoadLandingNotificationsDefaultsWhenUnset(t *testing.T) {
	s := newNotificationsGateway(t)
	got := s.loadLandingNotifications(context.Background())
	require.Len(t, got, 3)
	require.Equal(t, "DeepSeek V4.1 Flash", got[0].Title)
}

func TestLoadLandingNotificationsRespectsExplicitEmpty(t *testing.T) {
	s := newNotificationsGateway(t)
	require.NoError(t, s.saveLandingNotifications(context.Background(), []LandingNotification{}))
	got := s.loadLandingNotifications(context.Background())
	require.Empty(t, got)
}

func TestValidateLandingNotifications(t *testing.T) {
	ok := func() []LandingNotification {
		return []LandingNotification{{ID: "a1b2c3d4", Title: "Hi", Body: "<b>x</b>"}}
	}
	require.NoError(t, validateLandingNotifications(ok()))

	// more than 5
	tooMany := make([]LandingNotification, 6)
	for i := range tooMany {
		tooMany[i] = LandingNotification{ID: "a1b2c3d4", Title: "Hi"}
	}
	require.Error(t, validateLandingNotifications(tooMany))

	// blank title
	require.Error(t, validateLandingNotifications([]LandingNotification{{ID: "a1b2c3d4", Title: "  "}}))

	// bad href scheme
	require.Error(t, validateLandingNotifications([]LandingNotification{{ID: "a1b2c3d4", Title: "Hi", Href: "javascript:alert(1)"}}))
	require.Error(t, validateLandingNotifications([]LandingNotification{{ID: "a1b2c3d4", Title: "Hi", Href: "/relative"}}))
	require.NoError(t, validateLandingNotifications([]LandingNotification{{ID: "a1b2c3d4", Title: "Hi", Href: "https://example.com"}}))

	// oversize title
	big := strings.Repeat("x", 201)
	require.Error(t, validateLandingNotifications([]LandingNotification{{ID: "a1b2c3d4", Title: big}}))
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := newNotificationsGateway(t)
	in := []LandingNotification{{ID: "a1b2c3d4", Tag: "BARU", Title: "T", Body: "<em>b</em>", Href: "https://x.dev"}}
	require.NoError(t, s.saveLandingNotifications(context.Background(), in))
	got := s.loadLandingNotifications(context.Background())
	require.Equal(t, in, got)
}

func TestNewNotificationIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newNotificationID()
		require.Len(t, id, 8)
		require.False(t, seen[id])
		seen[id] = true
	}
}

func TestAdminNotificationsRoundTrip(t *testing.T) {
	s := newNotificationsGateway(t)

	// GET returns defaults first.
	rec := httptest.NewRecorder()
	s.adminGetNotifications(rec, httptest.NewRequest(http.MethodGet, "/settings/notifications", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "DeepSeek V4.1 Flash")

	// POST replaces.
	body := `{"notifications":[{"id":"a1b2c3d4","title":"Halo","body":"<b>hi</b>","href":"https://x.dev"}]}`
	rec = httptest.NewRecorder()
	s.adminUpdateNotifications(rec, httptest.NewRequest(http.MethodPost, "/settings/notifications", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code)

	// GET reflects the replacement.
	rec = httptest.NewRecorder()
	s.adminGetNotifications(rec, httptest.NewRequest(http.MethodGet, "/settings/notifications", nil))
	require.Contains(t, rec.Body.String(), "Halo")
	require.NotContains(t, rec.Body.String(), "DeepSeek V4.1 Flash")
}

func TestAdminNotificationsRejectsInvalid(t *testing.T) {
	s := newNotificationsGateway(t)
	rec := httptest.NewRecorder()
	s.adminUpdateNotifications(rec, httptest.NewRequest(http.MethodPost, "/settings/notifications",
		strings.NewReader(`{"notifications":[{"title":""}]}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPublicNotificationsNoAuth(t *testing.T) {
	s := newNotificationsGateway(t)
	rec := httptest.NewRecorder()
	s.publicNotifications(rec, httptest.NewRequest(http.MethodGet, "/v1/public/notifications", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "notifications")
}
