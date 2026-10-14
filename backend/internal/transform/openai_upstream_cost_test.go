package transform

import (
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func TestOpenAIParsesSurplusBuyerCost(t *testing.T) {
	body := `{"id":"x","model":"m","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
	  "usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"buyer_cost_micro":37}}`
	for name, parse := range map[string]func() (float64, bool, error){
		"bytes": func() (float64, bool, error) {
			r, err := OpenAICodec{}.ParseResponse([]byte(body), "m")
			if err != nil {
				return 0, false, err
			}
			return r.Usage.UpstreamCostMicro, r.Usage.UpstreamCostReported, nil
		},
		"reader": func() (float64, bool, error) {
			r, err := OpenAICodec{}.ParseResponseFrom(strings.NewReader(body), "m")
			if err != nil {
				return 0, false, err
			}
			return r.Usage.UpstreamCostMicro, r.Usage.UpstreamCostReported, nil
		},
	} {
		v, ok, err := parse()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !ok || v != 37 {
			t.Fatalf("%s: cost = %v reported=%v, want 37 true", name, v, ok)
		}
	}
}

func TestOpenAIStreamParsesSurplusBuyerCost(t *testing.T) {
	line := `{"id":"x","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"buyer_cost_micro":0}}`
	chunks, err := OpenAICodec{}.ParseStreamLine([]byte(line), "m")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range chunks {
		if c.Usage != nil {
			found = true
			// A reported zero is a real (free) cost, distinct from unreported.
			if !c.Usage.UpstreamCostReported || c.Usage.UpstreamCostMicro != 0 {
				t.Fatalf("usage = %+v, want reported zero", c.Usage)
			}
		}
	}
	if !found {
		t.Fatal("no usage chunk")
	}
}

func TestOpenAIUsageWithoutBuyerCostIsUnreported(t *testing.T) {
	body := `{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	r, err := OpenAICodec{}.ParseResponse([]byte(body), "m")
	if err != nil {
		t.Fatal(err)
	}
	if r.Usage.UpstreamCostReported {
		t.Fatal("absent buyer_cost_micro must be unreported")
	}
}

func TestOpenAIRequestAsksUsageOnlyForUpstreamBilledStreams(t *testing.T) {
	base := core.ChatRequest{Model: "m", Stream: true, Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}}}
	plain, err := renderOAIRequest(&base, reasoningNone)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "stream_options") {
		t.Fatalf("default stream must not send stream_options: %s", plain)
	}
	billed := base
	billed.UpstreamUsage = true
	body, err := renderOAIRequest(&billed, reasoningNone)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"stream_options":{"include_usage":true}`) {
		t.Fatalf("upstream-billed stream must ask for usage: %s", body)
	}
}
