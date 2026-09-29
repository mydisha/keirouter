package currency

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseUSDIDR(t *testing.T) {
	body := []byte(`{"result":"success","base_code":"USD","rates":{"USD":1,"IDR":16665.5}}`)
	rate, err := ParseUSDIDR(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rate != 16665.5 {
		t.Fatalf("rate = %v, want 16665.5", rate)
	}
}

func TestParseUSDIDRRejectsFailureAndMissingIDR(t *testing.T) {
	cases := []string{
		`{"result":"error","rates":{"IDR":16665}}`,
		`{"result":"success","rates":{"EUR":0.87}}`,
		`{"result":"success","rates":{"IDR":0}}`,
		`not json`,
	}
	for _, c := range cases {
		if _, err := ParseUSDIDR([]byte(c)); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestDefaults(t *testing.T) {
	d := Defaults(Settings{})
	if !d.AutoRefreshEnabled || d.RefreshIntervalH != DefaultIntervalH || d.SourceURL != DefaultSourceURL {
		t.Fatalf("unexpected defaults: %+v", d)
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(Defaults(Settings{})); err != nil {
		t.Fatalf("defaults should validate: %v", err)
	}
	bad := Defaults(Settings{}); bad.RefreshIntervalH = 0
	if err := Validate(bad); err == nil || !strings.Contains(err.Error(), "refresh_interval_h") {
		t.Fatalf("interval 0 should fail, got %v", err)
	}
	bad = Defaults(Settings{}); bad.OverrideEnabled = true; bad.OverrideRate = 0
	if err := Validate(bad); err == nil {
		t.Fatal("override enabled with zero rate should fail")
	}
	bad = Defaults(Settings{}); bad.SourceURL = "ftp://x"
	if err := Validate(bad); err == nil {
		t.Fatal("non-http source url should fail")
	}
}

func TestDefaultsPreservesExplicitAutoRefreshFalse(t *testing.T) {
	// A persisted config that is non-zero (so it isn't treated as fresh) but has
	// auto-refresh explicitly false must stay false.
	s := Defaults(Settings{RefreshIntervalH: 6, AutoRefreshEnabled: false})
	if s.AutoRefreshEnabled {
		t.Fatal("explicit auto_refresh_enabled=false must be preserved")
	}
	if s.RefreshIntervalH != 6 {
		t.Fatalf("interval = %d, want 6", s.RefreshIntervalH)
	}
}

func TestResolve(t *testing.T) {
	// Override wins when enabled and positive.
	rate, src, ok := Resolve(Settings{OverrideEnabled: true, OverrideRate: 17000, Rate: 16000, Source: SourceAPI})
	if !ok || rate != 17000 || src != SourceOverride {
		t.Fatalf("override: got rate=%v src=%q ok=%v", rate, src, ok)
	}
	// Falls back to the fetched rate.
	rate, src, ok = Resolve(Settings{Rate: 16000, Source: SourceAPI})
	if !ok || rate != 16000 || src != SourceAPI {
		t.Fatalf("fetched: got rate=%v src=%q ok=%v", rate, src, ok)
	}
	// Invalid override falls back to fetched rate.
	rate, src, ok = Resolve(Settings{OverrideEnabled: true, OverrideRate: 0, Rate: 16000, Source: SourceAPI})
	if !ok || rate != 16000 || src != SourceAPI {
		t.Fatalf("invalid override fallback: got rate=%v src=%q ok=%v", rate, src, ok)
	}
	// Nothing available.
	if _, _, ok := Resolve(Settings{}); ok {
		t.Fatal("empty settings should not resolve")
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"success","rates":{"IDR":16600}}`))
	}))
	defer srv.Close()

	svc := New(nil)
	rate, err := svc.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if rate != 16600 {
		t.Fatalf("rate = %v, want 16600", rate)
	}
}

func TestFetchRejectsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := New(nil).Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestLoadSaveRoundTripNilRepo(t *testing.T) {
	svc := New(nil)
	// With no repo, Load returns defaults and Save is a no-op (best-effort).
	if got := svc.Load(context.Background()); got.RefreshIntervalH != DefaultIntervalH {
		t.Fatalf("load defaults: %+v", got)
	}
	if err := svc.Save(context.Background(), Defaults(Settings{})); err != nil {
		t.Fatalf("save without repo: %v", err)
	}
}

func TestRefreshKeepsOldRateOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	var saved []Settings
	svc := New(nil)
	svc.persist = func(_ context.Context, s Settings) error { saved = append(saved, s); return nil }
	svc.initial = Settings{SourceURL: srv.URL, Rate: 16000, Source: SourceAPI, FetchedAt: time.Now().UTC().Format(time.RFC3339), RefreshIntervalH: 24}

	out, err := svc.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if out.Rate != 16000 {
		t.Fatalf("old rate must be kept, got %v", out.Rate)
	}
	if out.LastError == "" {
		t.Fatal("last_error must be set")
	}
	if len(saved) == 0 || saved[len(saved)-1].LastError == "" {
		t.Fatal("failure state must be persisted")
	}
}

func TestRefreshDoesNotWipeRateOnReadError(t *testing.T) {
	var saved []Settings
	svc := New(nil)
	svc.persist = func(_ context.Context, s Settings) error { saved = append(saved, s); return nil }
	svc.initial = Settings{SourceURL: "http://example.invalid", Rate: 16000, Source: SourceAPI, FetchedAt: time.Now().UTC().Format(time.RFC3339), RefreshIntervalH: 24}
	svc.readErr = errors.New("boom")

	if _, err := svc.Refresh(context.Background()); err == nil {
		t.Fatal("expected error when the settings read fails")
	}
	for _, s := range saved {
		if s.Rate == 0 {
			t.Fatalf("stored rate must not be wiped, persisted %+v", s)
		}
	}
	if len(saved) != 0 {
		t.Fatalf("no persist should happen on a read error, got %d", len(saved))
	}
}
