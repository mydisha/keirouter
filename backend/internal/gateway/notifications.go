package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// landingNotificationsKey is the settings-store key for landing notifications.
const landingNotificationsKey = "landing_notifications"

// maxLandingNotifications caps how many notifications the landing page shows.
const maxLandingNotifications = 5

// LandingNotification is one announcement shown in the landing popup.
// Title and Body may contain limited HTML; they are sanitized at render time.
type LandingNotification struct {
	ID    string `json:"id"`
	Tag   string `json:"tag"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Href  string `json:"href"`
}

// defaultLandingNotifications preserves the pre-settings hardcoded content so
// an unconfigured deployment looks unchanged.
func defaultLandingNotifications() []LandingNotification {
	return []LandingNotification{
		{ID: "deepseek-v4.1-flash", Tag: "BARU", Title: "DeepSeek V4.1 Flash", Body: "Model flash baru dengan konteks 1.000.000 token dan harga per-1M sangat rendah."},
		{ID: "pixel-canary", Tag: "STEALTH", Title: "Pixel Canary", Body: "Akses sementara selama uji coba. Kuota terbatas dan dapat berubah tanpa pemberitahuan."},
		{ID: "space-bunny-alpha", Tag: "STEALTH", Title: "Space Bunny Alpha", Body: "Masih tersedia untuk sementara. Harga tetap Rp 1 / 1M selama periode stealth."},
	}
}

// newNotificationID returns an 8-char lowercase hex id.
func newNotificationID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "notif"
	}
	return hex.EncodeToString(b)
}

// loadLandingNotifications reads stored notifications. When the key is absent or
// unreadable it returns the defaults; an explicitly stored empty list is kept.
func (s *Server) loadLandingNotifications(ctx context.Context) []LandingNotification {
	if s.settings == nil {
		return defaultLandingNotifications()
	}
	raw, err := s.settings.Get(ctx, landingNotificationsKey)
	if err != nil || raw == "" {
		return defaultLandingNotifications()
	}
	var stored struct {
		Notifications []LandingNotification `json:"notifications"`
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return defaultLandingNotifications()
	}
	if stored.Notifications == nil {
		stored.Notifications = []LandingNotification{}
	}
	return stored.Notifications
}

// saveLandingNotifications persists the full list.
func (s *Server) saveLandingNotifications(ctx context.Context, items []LandingNotification) error {
	if s.settings == nil {
		return fmt.Errorf("settings store not configured")
	}
	if items == nil {
		items = []LandingNotification{}
	}
	raw, err := json.Marshal(struct {
		Notifications []LandingNotification `json:"notifications"`
	}{Notifications: items})
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, landingNotificationsKey, string(raw))
}

// validateLandingNotifications enforces the structural contract. HTML content
// is not parsed here; it is sanitized at render time.
func validateLandingNotifications(items []LandingNotification) error {
	if len(items) > maxLandingNotifications {
		return fmt.Errorf("at most %d notifications are allowed", maxLandingNotifications)
	}
	for i, n := range items {
		if strings.TrimSpace(n.Title) == "" {
			return fmt.Errorf("notification %d: title is required", i+1)
		}
		if len(n.Title) > 200 {
			return fmt.Errorf("notification %d: title must be at most 200 characters", i+1)
		}
		if len(n.Body) > 2000 {
			return fmt.Errorf("notification %d: body must be at most 2000 characters", i+1)
		}
		if len(n.Tag) > 40 {
			return fmt.Errorf("notification %d: tag must be at most 40 characters", i+1)
		}
		if n.Href != "" {
			u, err := url.Parse(n.Href)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("notification %d: href must be an absolute http(s) URL", i+1)
			}
			if len(n.Href) > 2048 {
				return fmt.Errorf("notification %d: href must be at most 2048 characters", i+1)
			}
		}
	}
	return nil
}

// ---- admin endpoints --------------------------------------------------------

func (s *Server) adminGetNotifications(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"notifications": s.loadLandingNotifications(r.Context())})
}

func (s *Server) adminUpdateNotifications(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	var body struct {
		Notifications []LandingNotification `json:"notifications"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	items := body.Notifications
	if items == nil {
		items = []LandingNotification{}
	}
	// Assign new ids to any item without one (create path).
	for i := range items {
		if strings.TrimSpace(items[i].ID) == "" {
			items[i].ID = newNotificationID()
		}
	}
	if err := validateLandingNotifications(items); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.saveLandingNotifications(r.Context(), items); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": items})
}

// ---- public endpoint --------------------------------------------------------

func (s *Server) publicNotifications(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"notifications": s.loadLandingNotifications(r.Context())})
}
