package capability

import (
	"regexp"
	"strings"
	"testing"
)

// regexpGlob is the anchored, case-insensitive regexp the glob matcher
// replaced; it is kept here as the reference the matcher must agree with.
func regexpGlob(pattern string) *regexp.Regexp {
	parts := strings.Split(pattern, "*")
	for i, s := range parts {
		parts[i] = regexp.QuoteMeta(s)
	}
	return regexp.MustCompile("(?i)^" + strings.Join(parts, ".*") + "$")
}

func globCorpus() []string {
	corpus := []string{
		"", "*", "a", "aba", "abba", "mock-model", "MOCK/Mock-Model",
		"anthropic/claude-opus-4.7", "Claude-Opus-4-7-20260101", "claude-claude-opus",
		"gpt-4o", "GPT-4O-MINI", "openai/gpt-4o-2024-08-06", "@cf/meta/llama-3",
		"ünïcode-模型-🔑", strings.Repeat("claude-", 200) + "x",
	}
	for k := range modelCapabilities {
		corpus = append(corpus, k, strings.ToUpper(k))
	}
	for _, byModel := range providerCapabilities {
		for k := range byModel {
			corpus = append(corpus, k)
		}
	}
	for _, p := range patternCapabilities {
		for _, fill := range []string{"", "x", "-Mid-", "*"} {
			s := strings.ReplaceAll(p.pattern, "*", fill)
			corpus = append(corpus, s, strings.ToUpper(s), "pre/"+s, s+"-suffix")
		}
	}
	return corpus
}

func TestGlobMatchesRegexpForEveryPattern(t *testing.T) {
	patterns := []string{"ab*ba", "*", "**", "a*", "*a", "a*b*a", "exact", "*a*a*"}
	for _, p := range patternCapabilities {
		patterns = append(patterns, p.pattern)
	}
	corpus := globCorpus()
	for _, pat := range patterns {
		re, g := regexpGlob(pat), compileGlob(pat)
		for _, s := range corpus {
			if want, got := re.MatchString(s), g.match(strings.ToLower(s)); want != got {
				t.Errorf("pattern %q input %q: regexp=%v glob=%v", pat, s, want, got)
			}
		}
	}
}

func TestResolveUnknownModelFallsToDefault(t *testing.T) {
	r := Resolve("custom-openai-mock", "mock/mock-model")
	if r.Source != SourceDefault {
		t.Fatalf("source = %v, want default", r.Source)
	}
	r = Resolve("", "Anthropic/CLAUDE-OPUS-4-7-latest")
	if r.Source != SourcePattern || !r.Profile.Vision {
		t.Fatalf("mixed-case claude id: %+v", r)
	}
}

func BenchmarkResolveUnknownModel(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = Resolve("custom-openai-mock", "mock/mock-model")
	}
}
