package gateway

import (
	"encoding/json"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/currency"
)

func TestCurrencyStatusShape(t *testing.T) {
	// The GET handler serializes the same status shape the UI expects.
	st := currencyStatus{
		Config:        currency.Defaults(currency.Settings{}),
		EffectiveRate: 0,
		Source:        currency.SourceNone,
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"config", "effective_rate", "source", "fetched_at", "last_error"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing key %q in %s", k, raw)
		}
	}
}
