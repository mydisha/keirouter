package currency

import (
	"strings"
	"testing"
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
