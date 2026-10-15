package guardrails

import "testing"

func TestFold_WordsView(t *testing.T) {
	cases := map[string]string{
		"F.U.C.K you!!":       "fuck you",
		"f u c k":             "fuck",
		"sh!t and $hit":       "shit and shit",
		"k1ll 2024 people":    "kill 2024 people",
		"f*ck":                "f*ck",
		"ｆｕｃｋ":                "fuck",
		"fu​ck":               "fuck",
		"іgnоrе":              "ignore",
		"Ïgnörè":              "ignore",
		"hello!":              "hello",
		"**bold** text":       "bold** text",
		"laki-laki":           "laki laki",
		"𝐢𝐠𝐧𝐨𝐫𝐞 instructions": "ignore instructions",
	}
	for in, want := range cases {
		if got := Fold(in, FoldOptions{Lower: true, Words: true}).Text; got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFold_SpanMapsToOriginal(t *testing.T) {
	src := "say F U\u200b C K now"
	f := Fold(src, FoldOptions{Lower: true, Words: true})
	if f.Text != "say fuck now" {
		t.Fatalf("view = %q", f.Text)
	}
	s, e := f.Span(4, 8)
	if got := src[s:e]; got != "F U\u200b C K" {
		t.Errorf("span maps to %q", got)
	}
}

func TestFold_KeepsCaseWhenAsked(t *testing.T) {
	if got := Fold("ＡＢＣ１２３", FoldOptions{}).Text; got != "ABC123" {
		t.Errorf("got %q", got)
	}
}
