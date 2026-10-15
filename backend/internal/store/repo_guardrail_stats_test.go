package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGuardrailStats(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	since := now.Add(-time.Hour)

	n := 0
	usage := func(rec UsageRecord) {
		n++
		rec.ID = fmt.Sprintf("u%d", n)
		rec.RequestID = fmt.Sprintf("r%d", n)
		rec.TenantID = DefaultTenantID
		if rec.Provider == "" {
			rec.Provider = "openai"
		}
		if rec.Model == "" {
			rec.Model = "gpt-4o"
		}
		if rec.Status == "" {
			rec.Status = "success"
		}
		if rec.CreatedAt.IsZero() {
			rec.CreatedAt = now.Add(-10 * time.Minute)
		}
		require.NoError(t, db.Usage().Record(ctx, rec))
	}
	usage(UsageRecord{APIKeyID: "k1"}) // no policy applied
	usage(UsageRecord{APIKeyID: "k1", GuardrailAction: "allow", GuardrailMS: 2})
	usage(UsageRecord{APIKeyID: "k1", GuardrailAction: "mask", GuardrailDetector: "pii", GuardrailDirection: "inbound", GuardrailMS: 4})
	usage(UsageRecord{APIKeyID: "k2", Status: "blocked", ErrorKind: "policy_blocked",
		GuardrailAction: "block", GuardrailDetector: "injection", GuardrailDirection: "inbound", GuardrailMS: 40})
	usage(UsageRecord{APIKeyID: "k2", Model: "claude", Status: "blocked", ErrorKind: "policy_blocked",
		GuardrailAction: "block", GuardrailDetector: "toxicity", GuardrailDirection: "outbound", GuardrailMidStream: true, GuardrailMS: 10})
	usage(UsageRecord{APIKeyID: "k1", GuardrailAction: "log_only", GuardrailDetector: "topics", GuardrailMS: 6})
	// A block recorded before per-request outcomes existed still counts.
	usage(UsageRecord{APIKeyID: "k2", Status: "blocked", ErrorKind: "policy_blocked"})
	// LLM judge calls are overhead, not traffic.
	usage(UsageRecord{APIKeyID: "k2", Client: "guardrails-judge", Model: "judge", PromptTokens: 100, CompletionTokens: 5, CostNanos: 3000})
	// Outside the window.
	usage(UsageRecord{APIKeyID: "k1", GuardrailAction: "block", CreatedAt: now.Add(-3 * time.Hour)})

	logs := []GuardrailLog{
		{ID: "g1", RequestID: "r3", Detector: "pii", Direction: "inbound", Action: "mask", Reason: "pii: masked EMAIL_ADDRESS",
			Findings: `[{"entity":"EMAIL_ADDRESS","score":1,"start":0,"end":5}]`},
		{ID: "g2", RequestID: "r4", Detector: "injection", Direction: "inbound", Action: "block", Reason: "injection: override",
			Findings: `[{"entity":"PROMPT_INJECTION","score":0.9,"start":0,"end":5}]`},
		// A streamed response masks once per release: two rows, one request.
		{ID: "g3", RequestID: "r5", Detector: "pii", Direction: "outbound", Action: "mask", Reason: "pii: masked EMAIL_ADDRESS",
			Findings: `[{"entity":"EMAIL_ADDRESS","score":1,"start":0,"end":5}]`},
		{ID: "g4", RequestID: "r5", Detector: "pii", Direction: "outbound", Action: "mask", Reason: "pii: masked EMAIL_ADDRESS",
			Findings: `[{"entity":"EMAIL_ADDRESS","score":1,"start":0,"end":5},{"entity":"PHONE_NUMBER","score":1,"start":9,"end":19}]`},
		// Test-panel rows are not traffic.
		{ID: "g5", RequestID: GuardrailTestRequestPrefix + "abc", Detector: "pii", Direction: "inbound", Action: "block",
			Findings: `[{"entity":"CREDIT_CARD","score":1,"start":0,"end":5}]`},
	}
	for i := range logs {
		logs[i].TenantID = DefaultTenantID
		logs[i].CreatedAt = now.Add(-5 * time.Minute)
	}
	require.NoError(t, db.GuardrailLogs().BatchInsert(ctx, logs))

	st, err := db.GuardrailStats().Stats(ctx, DefaultTenantID, GuardrailStatsFilter{Since: since, Buckets: 6})
	require.NoError(t, err)

	tot := st.Totals
	require.EqualValues(t, 7, tot.Requests)
	require.EqualValues(t, 5, tot.Evaluated)
	require.EqualValues(t, 3, tot.Blocked)
	require.EqualValues(t, 1, tot.BlockedInbound)
	require.EqualValues(t, 1, tot.BlockedOutbound)
	require.EqualValues(t, 1, tot.BlockedMidStream)
	require.EqualValues(t, 1, tot.Masked)
	require.EqualValues(t, 1, tot.LogOnly)
	require.EqualValues(t, 12, tot.AvgMS) // (2+4+40+10+6)/5
	require.EqualValues(t, 6, tot.P50MS)
	require.EqualValues(t, 10, tot.P95MS)

	require.Equal(t, GuardrailJudge{Calls: 1, Tokens: 105, CostNanos: 3000}, st.Judge)

	var tlRequests, tlBlocked int64
	for _, b := range st.Timeline {
		tlRequests += b.Requests
		tlBlocked += b.Blocked
	}
	require.EqualValues(t, 7, tlRequests)
	require.EqualValues(t, 3, tlBlocked)

	require.Len(t, st.ByDetector, 3)
	byKey := map[string]GuardrailDetectorRow{}
	for _, d := range st.ByDetector {
		byKey[d.Detector+"/"+d.Direction+"/"+d.Action] = d
	}
	require.Equal(t, GuardrailDetectorRow{Detector: "pii", Direction: "outbound", Action: "mask", Decisions: 2, Requests: 1},
		byKey["pii/outbound/mask"])

	require.Equal(t, GuardrailReasonRow{Detector: "pii", Action: "mask", Reason: "pii: masked EMAIL_ADDRESS", Requests: 2},
		st.TopReasons[0])

	require.Equal(t, []GuardrailEntityRow{
		{Entity: "EMAIL_ADDRESS", Count: 3},
		{Entity: "PHONE_NUMBER", Count: 1},
		{Entity: "PROMPT_INJECTION", Count: 1},
	}, st.TopEntities)
	require.False(t, st.EntitySampled)

	require.Equal(t, []GuardrailTargetRow{
		{Key: "k2", Requests: 3, Blocked: 3},
		{Key: "k1", Requests: 4, Masked: 1, LogOnly: 1},
	}, st.TopAPIKeys)
	require.Equal(t, "gpt-4o", st.TopModels[0].Key)

	// Filters narrow both sources; the judge keeps only the key filter.
	st, err = db.GuardrailStats().Stats(ctx, DefaultTenantID, GuardrailStatsFilter{Since: since, APIKeyID: "k2", Model: "claude"})
	require.NoError(t, err)
	require.EqualValues(t, 1, st.Totals.Requests)
	require.EqualValues(t, 1, st.Totals.BlockedMidStream)
	require.EqualValues(t, 1, st.Judge.Calls)
	require.Empty(t, st.ByDetector)
}
