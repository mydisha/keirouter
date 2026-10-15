package consolelog

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"
)

func TestRingKeepsNewestInOrder(t *testing.T) {
	b := New()
	for i := 0; i < MaxEntries+25; i++ {
		b.Log("INFO", fmt.Sprintf("line %d", i), "")
	}
	got := b.Entries()
	if len(got) != MaxEntries {
		t.Fatalf("len = %d, want %d", len(got), MaxEntries)
	}
	if got[0].Message != "line 25" || got[len(got)-1].Message != fmt.Sprintf("line %d", MaxEntries+24) {
		t.Fatalf("unexpected window: first=%q last=%q", got[0].Message, got[len(got)-1].Message)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Seq != got[i-1].Seq+1 {
			t.Fatalf("seq gap at %d: %d -> %d", i, got[i-1].Seq, got[i].Seq)
		}
	}
}

func TestSubscribeSnapshotAndLiveFeedDoNotOverlap(t *testing.T) {
	b := New()
	b.Log("INFO", "before", "")
	l := NewListener(8)
	hist := b.Subscribe(l)
	defer b.Unsubscribe(l)
	b.Log("WARN", "after", "")

	if len(hist) != 1 || hist[0].Message != "before" {
		t.Fatalf("history = %+v", hist)
	}
	ev := <-l.C
	if ev.Entry.Message != "after" || ev.Entry.Seq != hist[0].Seq+1 {
		t.Fatalf("live event = %+v", ev.Entry)
	}
}

func TestClearResetsAndNotifies(t *testing.T) {
	b := New()
	l := NewListener(8)
	b.Subscribe(l)
	b.Log("INFO", "x", "")
	<-l.C
	b.Clear()
	if ev := <-l.C; !ev.Clear {
		t.Fatalf("expected clear event, got %+v", ev)
	}
	if n := len(b.Entries()); n != 0 {
		t.Fatalf("entries after clear = %d", n)
	}
	b.Log("INFO", "y", "")
	if got := b.Entries(); len(got) != 1 || got[0].Message != "y" {
		t.Fatalf("after clear = %+v", got)
	}
}

func TestNilBufferIsSafe(t *testing.T) {
	var b *Buffer
	b.Log("INFO", "ignored", "")
	b.Clear()
	if b.Entries() != nil || b.Epoch() != "" {
		t.Fatal("nil buffer should be inert")
	}
}

func TestTeeMirrorsWarningsOnly(t *testing.T) {
	var out bytes.Buffer
	base := slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelInfo}))
	b := New()
	log := Tee(base, b, slog.LevelWarn, "oauth").With("provider", "claude")

	log.Info("refreshed")
	log.Warn("oauth keepalive: refresh failed", "account", "acc-1", "err", "invalid_grant")
	log.Debug("hidden")

	got := b.Entries()
	if len(got) != 1 {
		t.Fatalf("mirrored %d entries, want 1: %+v", len(got), got)
	}
	e := got[0]
	if e.Level != "WARN" || e.Kind != "oauth" || e.Provider != "claude" || e.Message != "oauth keepalive: refresh failed" {
		t.Fatalf("entry = %+v", e)
	}
	if want := "provider: claude\naccount: acc-1\nerr: invalid_grant"; e.Detail != want {
		t.Fatalf("detail = %q, want %q", e.Detail, want)
	}
	if !bytes.Contains(out.Bytes(), []byte("refreshed")) || !bytes.Contains(out.Bytes(), []byte("refresh failed")) {
		t.Fatalf("base handler missed records: %s", out.String())
	}
}
