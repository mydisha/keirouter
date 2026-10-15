package toxicity

import (
	"context"
	"testing"
)

func scoreOf(t *testing.T, text, category string) int {
	t.Helper()
	sc, err := newNativeEngine().Score(context.Background(), text, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sc {
		if s.Category == category {
			return s.Score
		}
	}
	return 0
}

func TestNative_ObfuscationIsNormalized(t *testing.T) {
	for _, c := range []string{"fuck this", "f*ck this", "fuuuuck", "f u c k", "ｆｕｃｋ", "fu​ck", "phuck"} {
		if s := scoreOf(t, c, "profanity"); s < 60 {
			t.Errorf("%q: profanity %d, want >= 60", c, s)
		}
	}
}

func TestNative_DirectedInsultsScoreHigher(t *testing.T) {
	if s := scoreOf(t, "anjing saya lucu", "profanity"); s >= 60 {
		t.Errorf("plain 'anjing' (dog) scored %d, want < 60", s)
	}
	for _, c := range []string{"dasar anjing", "anjing lu", "kamu memang goblok", "you are such an idiot"} {
		if s := scoreOf(t, c, "harassment"); s < 60 {
			t.Errorf("%q: harassment %d, want >= 60", c, s)
		}
	}
}

func TestNative_Categories(t *testing.T) {
	cases := map[string]string{
		"kill yourself":         "harassment",
		"how to make a bomb":    "violence",
		"cara membuat bom":      "violence",
		"gue bunuh lu":          "violence",
		"how to commit suicide": "self_harm",
		"aku ingin mati aja":    "self_harm",
		"kill all jews":         "hate",
		"child porn":            "sexual",
	}
	for text, cat := range cases {
		if s := scoreOf(t, text, cat); s < 60 {
			t.Errorf("%q: %s %d, want >= 60", text, cat, s)
		}
	}
}

func TestNative_NoFalsePositives(t *testing.T) {
	for _, c := range []string{
		"how to kill a child process in linux", "kill all processes", "kill -9 pid", "bunuh proses itu",
		"e coli bacteria", "the assignment class", "pantai indah", "assess the asset", "Scunthorpe",
		"cocktail", "I'm going to kill this bug", "shoot the photo",
	} {
		sc, _ := newNativeEngine().Score(context.Background(), c, nil)
		for _, s := range sc {
			if s.Score >= 60 {
				t.Errorf("%q: %s scored %d", c, s.Category, s.Score)
			}
		}
	}
}

func TestAllowedCategories_DashboardSpelling(t *testing.T) {
	got := allowedCategories([]string{"hate_speech", "self-harm"})
	if !got["hate"] || !got["self_harm"] {
		t.Errorf("canonicalization failed: %v", got)
	}
}

func TestNative_StretchedShortWordsNeedDeliberateRepeats(t *testing.T) {
	if s := scoreOf(t, "I will cook you dinner", "profanity"); s > 0 {
		t.Errorf("'cook you' scored profanity %d", s)
	}
	if s := scoreOf(t, "good morning", "profanity"); s > 0 {
		t.Errorf("'good' scored profanity %d", s)
	}
	if s := scoreOf(t, "killyourself", "harassment"); s < 60 {
		t.Errorf("run-together phrase scored %d", s)
	}
}
