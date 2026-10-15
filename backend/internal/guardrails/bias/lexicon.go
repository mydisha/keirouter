package bias

import "github.com/mydisha/keirouter/backend/internal/guardrails"

// Group vocabularies per category, as lexicon slots.
const (
	genderGroups    = "women|woman|girls|females|wives|men|boys|males|husbands|perempuan|wanita|cewek|cewe|pria|cowok|cowo|emak|ibu|istri|suami"
	ethnicGroups    = "asians|africans|europeans|americans|arabs|blacks|whites|latinos|hispanics|mexicans|indians|chinese|japanese|immigrants|jawa|batak|cina|china|tionghoa|padang|minang|madura|papua|ambon|bugis|sunda|betawi|arab|aseng|pribumi|bule|india|keturunan"
	religiousGroups = "muslims|christians|catholics|hindus|buddhists|jews|atheists|muslim|kristen|katolik|hindu|buddha|yahudi|ateis|islam|nasrani|kafir|ahmadiyah|syiah|konghucu"
	politicalGroups = "liberals|conservatives|leftists|rightists|democrats|republicans|socialists|communists|libtards|kadrun|cebong|kampret|buzzer|komunis|oposisi"

	quantifiers = "all|every|semua|setiap|seluruh"
	collectives = "orang|kaum|umat|suku|bangsa|para|the"
	// Copulas and absolute predicates that turn a group mention into a
	// generalization: "are", "always", "never", "itu", "pasti", "selalu".
	predicates = "are|is|always|never|cannot|cant|should|tend|itu|memang|pasti|selalu|tidak|gak|nggak|cuma|hanya|adalah|emang"
	essentials = "naturally|inherently|genetically|biologically|innately|instinctively"
)

// generalization builds the terms shared by every category: quantified
// generalizations ("all X are", "semua orang X pasti"), essentialist claims
// ("X are naturally"), and contemptuous framing ("dasar X", "typical X").
func generalization(groups string) []guardrails.Term {
	return []guardrails.Term{
		{Phrase: quantifiers + " " + collectives + "? " + groups + " " + predicates, Weight: 70},
		{Phrase: groups + " are|is " + essentials, Weight: 80},
		{Phrase: groups + " by nature", Weight: 75},
		{Phrase: "dasar " + collectives + "? " + groups, Weight: 70},
		{Phrase: "typical|khas " + groups, Weight: 50},
		{Phrase: "those|these|mereka " + collectives + "? " + groups + " always|never|selalu|pasti|memang", Weight: 65},
	}
}

func categoryTerms(groups string, extra ...guardrails.Term) []guardrails.Term {
	return append(generalization(groups), extra...)
}

var genderTerms = categoryTerms(genderGroups,
	guardrails.Term{Phrase: genderGroups + " belong|belongs in|at the? kitchen|home|house", Weight: 85},
	guardrails.Term{Phrase: genderGroups + " should stay at? home", Weight: 80},
	guardrails.Term{Phrase: genderGroups + " cannot|cant|shouldnt|arent", Weight: 60},
	guardrails.Term{Phrase: genderGroups + " can|aren|shouldn|don t", Weight: 60},
	guardrails.Term{Phrase: genderGroups + " are not good at", Weight: 60},
	guardrails.Term{Phrase: "naturally suited for " + genderGroups, Weight: 60},
	guardrails.Term{Phrase: "tempat|tempatnya " + genderGroups + " di|ada? dapur|kasur|rumah|sumur", Weight: 85},
	guardrails.Term{Phrase: genderGroups + " lebih cocok untuk|di", Weight: 55},
	guardrails.Term{Phrase: genderGroups + " tidak|gak|nggak pantas|layak|cocok", Weight: 65},
	guardrails.Term{Phrase: "kodrat|kodratnya " + genderGroups, Weight: 60},
	guardrails.Term{Phrase: "like a girl", Weight: 40},
)

var ethnicTerms = categoryTerms(ethnicGroups,
	guardrails.Term{Phrase: "people of that race", Weight: 60},
	guardrails.Term{Phrase: "ras itu memang|pasti", Weight: 65},
	guardrails.Term{Phrase: "suku itu memang|pasti", Weight: 65},
	guardrails.Term{Phrase: "go back to your country", Weight: 75},
	guardrails.Term{Phrase: "pulang ke negaramu|negara asalmu", Weight: 75},
)

var religiousTerms = categoryTerms(religiousGroups,
	guardrails.Term{Phrase: "that|this religion is", Weight: 55},
	guardrails.Term{Phrase: "those believers are", Weight: 60},
	guardrails.Term{Phrase: "agama itu memang|pasti|sesat", Weight: 65},
	guardrails.Term{Phrase: "umatnya pasti|memang|selalu", Weight: 65},
	guardrails.Term{Phrase: "agama sesat", Weight: 60},
)

var politicalTerms = categoryTerms(politicalGroups,
	guardrails.Term{Phrase: "libtard|libtards|feminazi|feminazis|snowflakes", Weight: 70},
	guardrails.Term{Phrase: "the radical left|right", Weight: 50},
	guardrails.Term{Phrase: "leftists|right wingers|rightwingers always", Weight: 60},
	guardrails.Term{Phrase: "semua|setiap kaum kiri|kanan", Weight: 65},
	guardrails.Term{Phrase: "kelompok|dasar cebong|kampret|kadrun", Weight: 70},
	guardrails.Term{Phrase: "clearly biased toward|towards", Weight: 45},
)
