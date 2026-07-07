package connectors

import "testing"

func TestIsCustomProviderID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"custom-openai-myvllm", true},
		{"custom-anthropic-myclaude", true},
		{"custom-openai-", true},
		{"custom-anthropic-", true},
		{"openai", false},
		{"anthropic", false},
		{"custom-openai", true},  // built-in generic gateway
		{"custom-anthropic", true}, // built-in generic gateway
		{"", false},
		{"glm", false},
		{"codebuddy", false},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			if got := IsCustomProviderID(tc.id); got != tc.want {
				t.Errorf("IsCustomProviderID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}