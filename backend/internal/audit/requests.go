package audit

import (
	"strconv"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// RequestScopeName is the instrumentation scope of exported request records.
const RequestScopeName = "github.com/mydisha/keirouter/requests"

// RequestEventName is the event.name of every exported request record.
const RequestEventName = "keirouter.request"

// UsageExporter ships one record per gateway request (the usage row the meter
// wrote). It never sees prompt or response content.
type UsageExporter interface {
	EnqueueUsage(rec store.UsageRecord)
	Stop(deadline time.Duration)
	Stats() ExporterStats
}

// RequestExporterConfig derives the request exporter's config from the audit
// exporter's: same collector, headers and resource, its own scope. Request
// volume is far higher than audit volume, so it gets its own, larger queue and
// can never crowd audit records out.
func RequestExporterConfig(audit OTLPConfig) OTLPConfig {
	cfg := audit
	cfg.Scope = RequestScopeName
	cfg.QueueSize = 65536
	return cfg
}

// EnqueueUsage queues one request record without blocking.
func (e *OTLPExporter) EnqueueUsage(rec store.UsageRecord) {
	e.enqueue(usageItem{rec})
}

type usageItem struct{ rec store.UsageRecord }

// logRecord maps a usage row onto an OTLP log record. Attribute names follow
// the OpenTelemetry GenAI semantic conventions where one exists
// (gen_ai.provider.name, gen_ai.usage.input_tokens, error.type, ...) and the
// keirouter.* namespace otherwise.
func (it usageItem) logRecord(observedUnixNano string) otlpLogRecord {
	r := it.rec
	attrs := make([]otlpKeyValue, 0, 32)
	str := func(k, v string) {
		if v != "" {
			attrs = append(attrs, otlpKeyValue{Key: k, Value: strVal(v)})
		}
	}
	num := func(k string, v int64) {
		attrs = append(attrs, otlpKeyValue{Key: k, Value: intVal(v)})
	}
	pos := func(k string, v int64) {
		if v > 0 {
			num(k, v)
		}
	}
	flag := func(k string, v bool) {
		if v {
			attrs = append(attrs, otlpKeyValue{Key: k, Value: boolVal(true)})
		}
	}

	str("event.name", RequestEventName)
	str("log.record.uid", r.ID)
	str("keirouter.request.id", r.RequestID)
	str("keirouter.request.status", r.Status)
	str("error.type", r.ErrorKind)
	str("keirouter.tenant.id", r.TenantID)
	str("keirouter.project.id", r.ProjectID)
	str("keirouter.api_key.id", r.APIKeyID)
	str("keirouter.client", r.Client)

	str("gen_ai.provider.name", r.Provider)
	str("gen_ai.response.model", r.Model)
	str("keirouter.account.id", r.AccountID)
	str("keirouter.chain.id", r.ChainID)
	pos("keirouter.fallback.count", int64(r.FallbackCount))
	flag("keirouter.cache.hit", r.CacheHit)

	num("gen_ai.usage.input_tokens", int64(r.PromptTokens))
	num("gen_ai.usage.output_tokens", int64(r.CompletionTokens))
	pos("keirouter.usage.cached_tokens", int64(r.CachedTokens))
	pos("keirouter.usage.cache_write_tokens", int64(r.CacheWriteTokens))
	pos("keirouter.usage.reasoning_tokens", int64(r.ReasoningTokens))
	str("keirouter.usage.source", r.UsageSource)
	pos("keirouter.usage.tokens_saved", int64(r.SlimTokensSaved+r.HeadroomTokensSaved))

	if r.CostNanos > 0 {
		num("keirouter.cost.nanos", r.CostNanos)
		attrs = append(attrs, otlpKeyValue{Key: "keirouter.cost.usd", Value: doubleVal(float64(r.CostNanos) / 1e9)})
	}
	str("keirouter.pricing.status", r.PricingStatus)

	pos("keirouter.latency.ms", int64(latencyMS(r)))
	pos("keirouter.upstream.latency.ms", int64(r.UpstreamLatencyMS))
	pos("keirouter.ttft.ms", int64(r.TTFTMS))

	str("keirouter.guardrail.action", r.GuardrailAction)
	str("keirouter.guardrail.detector", r.GuardrailDetector)
	str("keirouter.guardrail.direction", r.GuardrailDirection)
	flag("keirouter.guardrail.mid_stream", r.GuardrailMidStream)
	pos("keirouter.guardrail.ms", int64(r.GuardrailMS))

	sev := requestSeverity(r.Status)
	return otlpLogRecord{
		TimeUnixNano:         strconv.FormatInt(r.CreatedAt.UnixNano(), 10),
		ObservedTimeUnixNano: observedUnixNano,
		SeverityNumber:       severityNumber(sev),
		SeverityText:         sev,
		EventName:            RequestEventName,
		Body:                 strVal(requestSummary(r)),
		Attributes:           attrs,
	}
}

func latencyMS(r store.UsageRecord) int {
	if r.EndToEndLatencyMS > 0 {
		return r.EndToEndLatencyMS
	}
	return r.LatencyMS
}

// requestSeverity marks failures as errors and refusals as warnings.
func requestSeverity(status string) string {
	switch status {
	case "failed":
		return SeverityError
	case "blocked", "cancelled":
		return SeverityWarn
	default:
		return SeverityInfo
	}
}

// requestSummary is the human-readable record body, e.g.
// "openai/gpt-4o success 1200 in 300 out 820ms key key_123".
func requestSummary(r store.UsageRecord) string {
	var b strings.Builder
	b.WriteString(r.Provider)
	b.WriteByte('/')
	b.WriteString(r.Model)
	b.WriteByte(' ')
	b.WriteString(r.Status)
	if r.ErrorKind != "" {
		b.WriteString(" (" + r.ErrorKind + ")")
	}
	b.WriteString(" " + strconv.Itoa(r.PromptTokens) + " in " + strconv.Itoa(r.CompletionTokens) + " out")
	if ms := latencyMS(r); ms > 0 {
		b.WriteString(" " + strconv.Itoa(ms) + "ms")
	}
	if r.APIKeyID != "" {
		b.WriteString(" key " + r.APIKeyID)
	}
	return b.String()
}
