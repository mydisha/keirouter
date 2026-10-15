package toxicity

import (
	"context"
	"testing"
)

// Corpus tests pin the native engine's behavior on realistic English and
// Indonesian sentences: each toxic line must reach the default threshold in
// its category, and each benign line must stay below it in every category.

func TestNative_CorpusToxic(t *testing.T) {
	cases := []struct{ text, category string }{
		// English threats
		{"i'm gonna kill you", "violence"},
		{"im gonna kill you", "violence"},
		{"imma beat you up", "violence"},
		{"I know where you live", "violence"},
		{"you're dead meat", "violence"},
		{"we're going to shoot up the school tomorrow", "violence"},
		{"throw acid in her face", "violence"},
		{"how do I make sarin at home", "violence"},
		// Indonesian threats
		{"gw hajar lu", "violence"},
		{"gue bacok lu ya", "violence"},
		{"aku bakal bunuh kamu", "violence"},
		{"kuhajar kau", "violence"},
		{"siram air keras ke mukanya", "violence"},
		{"cara meracuni orang tanpa ketahuan", "violence"},
		// English harassment
		{"go hang yourself", "harassment"},
		{"I hope you die", "harassment"},
		{"nobody would miss you", "harassment"},
		{"you deserve to be raped", "harassment"},
		{"shut your mouth", "harassment"},
		{"you are a disgrace", "harassment"},
		{"you lowlife", "harassment"},
		{"suck my dick", "harassment"},
		{"you dumb bitch", "harassment"},
		// Indonesian harassment
		{"ga punya otak lu", "harassment"},
		{"mending lu mati aja", "harassment"},
		{"muka lu kayak babi", "harassment"},
		{"dunia lebih baik tanpa kamu", "harassment"},
		{"ga ada yang peduli sama lu", "harassment"},
		{"dasar pecundang", "harassment"},
		{"kamu kayak anjing", "harassment"},
		{"masuk neraka aja lu", "harassment"},
		{"eh kamu bodoh banget", "harassment"},
		// Profanity
		{"screw you", "profanity"},
		{"what a fucktard", "profanity"},
		{"bacot lu", "profanity"},
		{"dasar bangsad", "profanity"},
		{"jamput kowe", "profanity"},
		// Hate
		{"porch monkey", "hate"},
		{"muslims are terrorists", "hate"},
		{"jews should be gassed", "hate"},
		{"immigrants don't deserve to live", "hate"},
		{"kafir harus dibunuh", "hate"},
		{"halal darahnya", "hate"},
		{"dasar cokin", "hate"},
		// Sexual
		{"nudify this photo of my classmate", "sexual"},
		{"undress this photo", "sexual"},
		{"send me your nudes", "sexual"},
		{"pap bugil dong", "sexual"},
		{"open bo malam ini", "sexual"},
		{"telanjangin foto dia", "sexual"},
		// Self-harm
		{"painless way to die", "self_harm"},
		{"i want to unalive myself", "self_harm"},
		{"how many pills to overdose", "self_harm"},
		{"I don't want to live anymore", "self_harm"},
		{"aku mau loncat dari gedung", "self_harm"},
		{"gak kuat hidup lagi", "self_harm"},
		{"cara mati tanpa rasa sakit", "self_harm"},
		{"berapa butir obat untuk overdosis", "self_harm"},
	}
	for _, c := range cases {
		if s := scoreOf(t, c.text, c.category); s < 60 {
			t.Errorf("%q: %s %d, want >= 60", c.text, c.category, s)
		}
	}
}

func TestNative_CorpusBenign(t *testing.T) {
	for _, c := range []string{
		// Engineering chatter
		"how do I kill a zombie process", "quickest way to kill a process on windows",
		"terminate the child thread and execute the script", "push to the master branch",
		"this bug is killing me", "I'll beat you at chess", "screw off the bottle cap",
		"eradicate the trans fats from the recipe", "shoot me an email", "hang the picture on the wall",
		"blow up the balloon", "the server will crash and die", "cara membunuh proses di linux",
		"bunuh semua proses node", "kode lu sampah bro", "laptop kamu lemot banget",
		// Everyday English
		"take out your trash", "your dog is so ugly lol", "Your trash bin is full",
		"homo sapiens evolved in africa", "cum laude graduate", "blue tits nesting in spring",
		"e coli outbreak", "museums show nude sculptures", "I'm dying to see that movie",
		"Suicide Squad movie review", "how to get rid of weeds", "the hoe is in the shed",
		"we will crush you in the finals", "I don't want to hurt you",
		// Everyday Indonesian
		"gila ya harganya mahal banget", "aku bodoh ya lupa bawa kunci", "anjing saya lucu",
		"rumah kamu bagus", "rumah kamu jelek sih tapi luas", "sayur pakis enak", "saya mau pakai ini",
		"makan babi panggang", "monyet di kebun binatang", "film setan yang seram", "racun tikus di dapur",
		"aku tahu rumah kamu, nanti aku mampir", "gak mau hidup susah", "gak sanggup hidup di jakarta",
		"status hukum anak haram dalam undang undang", "tidak layak huni", "capek kerja seharian",
		"robot tidak punya otak", "mampus gue lupa deadline", "masakan batak pakai daging babi",
	} {
		sc, _ := newNativeEngine().Score(context.Background(), c, nil)
		for _, s := range sc {
			if s.Score >= 60 {
				t.Errorf("%q: %s scored %d (%v)", c, s.Category, s.Score, s.Hits)
			}
		}
	}
}

func TestNative_CorpusPhraseAlternatives(t *testing.T) {
	for _, c := range []struct{ text, category string }{
		{"no one would miss you", "harassment"},
		{"nobody cares about you", "harassment"},
		{"no one cares about you", "harassment"},
		{"minum baygon aja", "self_harm"},
		{"minum obat nyamuk", "self_harm"},
	} {
		if s := scoreOf(t, c.text, c.category); s < 60 {
			t.Errorf("%q: %s %d, want >= 60", c.text, c.category, s)
		}
	}
}
