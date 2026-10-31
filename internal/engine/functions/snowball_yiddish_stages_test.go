package functions

import "testing"

func yiddishRuleStages(t *testing.T) {
	// Nested among witnesses use ens, avoiding longer Tes+ene/ener matches.
	for _, tc := range [][2]string{
		{"גאנג", "גײ"}, {"נומ", "נעמ"}, {"מיט", "מײד"}, {"ביט", "בײט"}, {"ביס", "בײס"},
		{"װיז", "װײז"}, {"טריב", "טרײב"}, {"ליט", "לײט"}, {"קליב", "קלײב"}, {"ריב", "רײב"}, {"ריס", "רײס"},
		{"שװיג", "שװײג"}, {"שמיס", "שמײס"}, {"שניט", "שנײד"}, {"שריב", "שרײב"},
		{"בונד", "בינד"}, {"װוטש", "װיטש"}, {"זונג", "זינג"}, {"טרונק", "טרינק"}, {"צװונג", "צװינג"}, {"שלונג", "שלינג"},
		{"בױג", "בײג"}, {"הױב", "הײב"}, {"פארלױר", "פארליר"}, {"שטאנ", "שטײ"}, {"שװױר", "שװער"},
	} {
		w := yiddishWord{text: "זאבאב"+tc[0]+"ענס", p1: 6}
		w.c = w.length(); w.primary()
		want := "זאבאב"+tc[1]
		if w.result() != want || w.c != len(want) || w.p1 != 6 { t.Fatal("nested", tc, w) }
	}
	for _, ending := range []string{"ונג", "הײט", "קײט", "יקײט", "שאפט", "ל"} {
		for _, p1 := range []int{10, 11} {
			w := yiddishWord{text: "זאבאב"+ending, p1: p1}; w.c = w.length(); w.secondary()
			want := "זאבאב"
			if p1 == 11 { want += ending }
			if w.result() != want || w.c != len(want) || w.p1 != p1 { t.Fatal("secondary", ending, p1, w) }
		}
	}
	for _, ending := range []string{"יג", "יק", "יש", "ניק", "דיק", "בליק", "גליק", "נדיק", "ענדיק"} {
		for _, p1 := range []int{10, 11} {
			w := yiddishWord{text: "זאבאב"+ending, p1: p1}; w.c = w.length(); w.tertiary()
			want := "זאבאב"
			if p1 == 11 || ending == "בליק" || ending == "גליק" { want += ending }
			if w.result() != want || w.c != len(want) || w.p1 != p1 { t.Fatal("tertiary", ending, p1, w) }
		}
	}
	for _, tc := range [][2]string{{"וו", "װ"}, {"ווּ", "וו"}, {"וי", "ױ"}, {"ויִ", "וי"}, {"יי", "ײ"}, {"ייִ", "יי"}, {"ךםןףץ", "כמנפצ"}, {"א\u05ba\u05c7", "א\u05ba\u05c7"}, {"א\u05b0\u05b1\u05b2\u05b3\u05b4\u05b5\u05b6\u05b7\u05b8\u05b9\u05bb\u05bc\u05bf\u05c1\u05c2", "א"}} {
		w := yiddishWord{text: tc[0]}; w.prelude()
		if w.result() != tc[1] || w.c != 0 { t.Fatal("prelude", tc, w) }
	}
	// Whole-token no-op and suffix-only deletion retain immutable input backing.
	w := yiddishWord{text: "running"}; w.prelude(); w.markRegions(); w.c = w.length(); w.primary(); w.secondary(); w.tertiary(); w.cleanup()
	if w.result() != "running" || w.buf != nil { t.Fatal("unnecessary mutable scratch", w) }
	w = yiddishWord{text: "זאבאבנ", c: 12, p1: 6}; w.primary()
	if w.result() != "זאבאב" || w.buf != nil { t.Fatal("suffix deletion copied input", w) }
}
