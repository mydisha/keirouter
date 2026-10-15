package finance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

type alertFake struct {
	settings AlertSettings
	bals     []AccountBalance
	postErr  error
	posts    []string
	urls     []string
	fired    map[string]time.Time
	setCalls int
}

func (f *alertFake) deps() AlertDeps {
	if f.fired == nil {
		f.fired = map[string]time.Time{}
	}
	return AlertDeps{
		Load:     func(context.Context) (AlertSettings, error) { return f.settings, nil },
		Balances: func(context.Context) ([]AccountBalance, error) { return f.bals, nil },
		SetFired: func(_ context.Context, id string, t time.Time) error {
			f.setCalls++
			f.fired[id] = t
			return nil
		},
		Post: func(_ context.Context, url, content string) error {
			if f.postErr != nil {
				return f.postErr
			}
			f.urls = append(f.urls, url)
			f.posts = append(f.posts, content)
			return nil
		},
		Now: func() time.Time { return time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC) },
	}
}

func lowAcct(id string, low bool, fired time.Time) AccountBalance {
	return AccountBalance{
		Account:    store.UpstreamAccount{ID: id, Name: "Acme", Status: "active", LowBalanceMicros: 10_000_000, AlertFiredAt: fired},
		BalanceUSD: 4.5, Low: low,
	}
}

func enabled() AlertSettings {
	return AlertSettings{Enabled: true, DiscordWebhookURL: "https://discord.com/api/webhooks/1/tok"}
}

func TestCheckAlerts_FiresOnce(t *testing.T) {
	f := &alertFake{settings: enabled(), bals: []AccountBalance{lowAcct("a", true, time.Time{})}}
	if err := CheckAlerts(context.Background(), f.deps()); err != nil {
		t.Fatal(err)
	}
	if len(f.posts) != 1 || f.fired["a"].IsZero() {
		t.Fatalf("posts=%d fired=%v", len(f.posts), f.fired)
	}
	f.bals = []AccountBalance{lowAcct("a", true, f.fired["a"])}
	if err := CheckAlerts(context.Background(), f.deps()); err != nil {
		t.Fatal(err)
	}
	if len(f.posts) != 1 {
		t.Fatalf("reposted: %d", len(f.posts))
	}
}

func TestCheckAlerts_Rearms(t *testing.T) {
	f := &alertFake{settings: enabled(), bals: []AccountBalance{lowAcct("a", false, time.Unix(100, 0))}}
	if err := CheckAlerts(context.Background(), f.deps()); err != nil {
		t.Fatal(err)
	}
	if len(f.posts) != 0 || f.setCalls != 1 || !f.fired["a"].IsZero() {
		t.Fatalf("posts=%d set=%d fired=%v", len(f.posts), f.setCalls, f.fired)
	}
}

func TestCheckAlerts_PostFailureStaysArmed(t *testing.T) {
	f := &alertFake{settings: enabled(), postErr: errors.New("boom"), bals: []AccountBalance{lowAcct("a", true, time.Time{})}}
	_ = CheckAlerts(context.Background(), f.deps())
	if f.setCalls != 0 {
		t.Fatal("SetFired called after failed post")
	}
}

func TestCheckAlerts_DisabledOrNoURL(t *testing.T) {
	for _, s := range []AlertSettings{
		{Enabled: false, DiscordWebhookURL: "https://discord.com/api/webhooks/1/tok"},
		{Enabled: true},
	} {
		f := &alertFake{settings: s, bals: []AccountBalance{lowAcct("a", true, time.Time{})}}
		if err := CheckAlerts(context.Background(), f.deps()); err != nil {
			t.Fatal(err)
		}
		if len(f.posts) != 0 || f.setCalls != 0 {
			t.Fatalf("settings %+v acted", s)
		}
	}
}

func TestCheckAlerts_SkipsPlannedAndNoThreshold(t *testing.T) {
	planned := lowAcct("p", true, time.Time{})
	planned.Account.Status = "planned"
	nothr := lowAcct("n", true, time.Time{})
	nothr.Account.LowBalanceMicros = 0
	f := &alertFake{settings: enabled(), bals: []AccountBalance{planned, nothr}}
	if err := CheckAlerts(context.Background(), f.deps()); err != nil {
		t.Fatal(err)
	}
	if len(f.posts) != 0 || f.setCalls != 0 {
		t.Fatalf("posts=%d set=%d", len(f.posts), f.setCalls)
	}
}

func TestAlertMessage(t *testing.T) {
	b := lowAcct("a", true, time.Time{})
	if got, want := AlertMessage(b), "Saldo Acme tinggal $4.50 (batas $10.00)."; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	d := 3.9
	b.DaysLeft = &d
	if got := AlertMessage(b); !strings.HasSuffix(got, " Perkiraan habis dalam 3 hari.") {
		t.Fatalf("got %q", got)
	}
}

func TestPostDiscord(t *testing.T) {
	var method, ctype string
	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, ctype = r.Method, r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := PostDiscord(context.Background(), srv.URL, "halo"); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || ctype != "application/json" || body["content"] != "halo" || len(body) != 1 {
		t.Fatalf("method=%q ctype=%q body=%v", method, ctype, body)
	}
}
