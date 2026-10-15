package gateway

import "github.com/mydisha/keirouter/backend/internal/guardrails"

// guardrailTemplate is one starter policy surfaced in the dashboard's "From
// template" picker. The config is a full guardrails.Policy so the frontend
// can drop it into the editor without further massaging.
type guardrailTemplate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Config      guardrails.Policy `json:"config"`
}

// guardrailTemplates returns the curated catalog. Keep this list short — every
// template adds cognitive load to the picker — and prefer templates that map
// to clearly distinct use cases.
func guardrailTemplates() []guardrailTemplate {
	enabled := true

	piiID := &guardrails.PIIConfig{
		Enabled:  true,
		Types:    []string{"EMAIL_ADDRESS", "PHONE_NUMBER", "ID_NIK", "ID_KK", "ID_NPWP", "ID_PASSPORT", "ID_BPJS", "CREDIT_CARD"},
		Strategy: guardrails.PIIStrategyReplace,
		MinScore: 0.6,
	}
	piiRedact := &guardrails.PIIConfig{
		Enabled:    true,
		Strategy:   guardrails.PIIStrategyRedact,
		MinScore:   0.6,
		ScanOutput: true,
		BlockTypes: []string{"API_KEY", "PRIVATE_KEY"},
	}
	piiSecrets := &guardrails.PIIConfig{
		Enabled:  true,
		Types:    []string{"API_KEY", "PRIVATE_KEY", "PASSWORD", "CREDIT_CARD", "ID_NIK", "ID_NPWP"},
		Strategy: guardrails.PIIStrategyReplace,
		MinScore: 0.6,
	}
	piiBlock := &guardrails.PIIConfig{
		Enabled:  true,
		Strategy: guardrails.PIIStrategyBlock,
		MinScore: 0.5,
	}
	injectionBlock := &guardrails.InjectionConfig{
		Enabled:           true,
		SeverityThreshold: guardrails.SeverityMedium,
		Action:            guardrails.ActionBlock,
		Canary:            true,
	}
	injectionWarn := &guardrails.InjectionConfig{
		Enabled:           true,
		SeverityThreshold: guardrails.SeverityLow,
		Action:            guardrails.ActionLogOnly,
	}
	injectionIndirect := &guardrails.InjectionConfig{
		Enabled:           true,
		SeverityThreshold: guardrails.SeverityHigh,
		Action:            guardrails.ActionWarn,
		ScanToolResults:   true,
		Spotlight:         true,
	}
	toxicityWarn := &guardrails.ToxicityConfig{
		Enabled:   true,
		Threshold: 60,
		Action:    guardrails.ActionLogOnly,
	}

	return []guardrailTemplate{
		{
			ID:          "indonesia-pii",
			Name:        "Indonesia PII",
			Description: "Mask Indonesian identifiers (NIK, KK, NPWP, passport, BPJS) plus email, phone and card numbers with typed placeholders, across the whole conversation.",
			Config: guardrails.Policy{
				Enabled: &enabled,
				PII:     piiID,
			},
		},
		{
			ID:          "strict-safety",
			Name:        "Strict safety",
			Description: "Block on any PII match, block prompt injection at medium severity with a system-prompt leak canary, and block toxic prompts.",
			Config: guardrails.Policy{
				Enabled:   &enabled,
				PII:       piiBlock,
				Injection: injectionBlock,
				Toxicity: &guardrails.ToxicityConfig{
					Enabled: true, Threshold: 60, Action: guardrails.ActionBlock,
				},
			},
		},
		{
			ID:          "compliance-audit",
			Name:        "Compliance audit (log-only)",
			Description: "Every detector enabled at action=log_only — useful as a dry run before tightening to warn/block.",
			Config: guardrails.Policy{
				Enabled: &enabled,
				PII:     piiRedact,
				Injection: &guardrails.InjectionConfig{
					Enabled: true, SeverityThreshold: guardrails.SeverityLow, Action: guardrails.ActionLogOnly,
				},
				Toxicity: toxicityWarn,
				Bias: &guardrails.BiasConfig{
					Enabled: true, Threshold: 50, Action: guardrails.ActionLogOnly,
				},
			},
		},
		{
			ID:          "public-chatbot",
			Name:        "Public chatbot",
			Description: "Redact PII in both directions and refuse leaked API or private keys, block prompt injection and toxic prompts.",
			Config: guardrails.Policy{
				Enabled:   &enabled,
				PII:       piiRedact,
				Injection: injectionBlock,
				Toxicity: &guardrails.ToxicityConfig{
					Enabled: true, Threshold: 70, Action: guardrails.ActionBlock,
				},
			},
		},
		{
			ID:          "coding-agent",
			Name:        "Coding agent",
			Description: "Replace credentials, card numbers and Indonesian tax/ID numbers before they reach the provider, mark tool output as untrusted data, and flag high-severity injection hidden in tool results (fetched pages, files) without breaking the agent loop.",
			Config: guardrails.Policy{
				Enabled:   &enabled,
				PII:       piiSecrets,
				Injection: injectionIndirect,
			},
		},
		{
			ID:          "compliance-passive",
			Name:        "Compliance — alerts only",
			Description: "Lightweight log_only profile for environments that want visibility without breaking traffic.",
			Config: guardrails.Policy{
				Enabled:   &enabled,
				PII:       piiRedact,
				Injection: injectionWarn,
			},
		},
	}
}
