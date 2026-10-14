package market

import (
	"context"
	"testing"
)

func TestSettingsDefaultsAndClamp(t *testing.T) {
	get := func(context.Context, string) (string, error) { return "", nil }
	s := LoadSettings(context.Background(), get)
	if s.AutoRefresh || s.RefreshIntervalSeconds != 30 || s.MarkupPercent != 10 {
		t.Fatalf("unexpected defaults %+v", s)
	}
	getBad := func(context.Context, string) (string, error) {
		return `{"auto_refresh":true,"refresh_interval_seconds":0,"markup_percent":-5}`, nil
	}
	s2 := LoadSettings(context.Background(), getBad)
	if s2.RefreshIntervalSeconds != 30 || s2.MarkupPercent != 10 {
		t.Fatalf("expected clamped defaults %+v", s2)
	}
}
func TestEffectiveMarkupPrefersChainOverride(t *testing.T) {
	if got := EffectiveMarkup(nil, 10); got != 10 {
		t.Fatalf("nil override = %v, want global 10", got)
	}
	zero, v := 0.0, 25.0
	if got := EffectiveMarkup(&zero, 10); got != 0 {
		t.Fatalf("explicit 0 = %v, want 0", got)
	}
	if got := EffectiveMarkup(&v, 10); got != 25 {
		t.Fatalf("override = %v, want 25", got)
	}
	bad := -5.0
	if got := EffectiveMarkup(&bad, 10); got != 10 {
		t.Fatalf("invalid override = %v, want global", got)
	}
}
