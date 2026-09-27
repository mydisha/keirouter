package gateway

import "testing"

func TestValidateChainName(t *testing.T) {
	valid := []string{
		"deepseek-v4.1-flash",
		"deepseek-v4-1-flash",
		"production-fallback",
		"gpt-4o",
		"a.b.c",
		"A1_b-c.d",
	}
	for _, name := range valid {
		if err := validateChainName(name); err != nil {
			t.Errorf("validateChainName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",
		".leading",
		"-leading",
		"_leading",
		"has/slash",
		"has:colon",
		"chain:foo",
		"has space",
		"has@at",
		"has?question",
		"has#hash",
		"has\\backslash",
	}
	for _, name := range invalid {
		if err := validateChainName(name); err == nil {
			t.Errorf("validateChainName(%q) = nil, want error", name)
		}
	}
}
