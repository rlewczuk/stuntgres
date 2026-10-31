package functions

import "testing"

func TestSnowballSerbianRules(t *testing.T) {
	if len(serbianStep1) != 130 || len(serbianStep2) != 2035 || len(serbianStep3) != 26 {
		t.Fatal("incomplete released Serbian rule tables")
	}
	testSerbianStep1Witnesses(t)
	testSerbianStep2Witnesses0(t)
	testSerbianStep2Witnesses1(t)
	testSerbianStep2Witnesses2(t)
	testSerbianStep2Witnesses3(t)
	testSerbianStep3Witnesses(t)
	// Independent SBL transitions also exercise lookup across retained segments.
	crossing := serbianWord{prefix: "|ski", tail: "jima"}
	if !crossing.step(serbianStep2[:], serbianStep2Max, 1, true) || crossing.result() != "|sk" {
		t.Fatal("Step_2 lookup across prefix/replacement boundary", crossing)
	}
	retained := serbianWord{prefix: "abnormalan"}
	if !retained.step(serbianStep1[:], serbianStep1Max, 0, true) || retained.result() != "abnormalni" {
		t.Fatal("Step_1 replacement", retained)
	}
	if !retained.step(serbianStep2[:], serbianStep2Max, 2, true) || retained.result() != "abnormaln" {
		t.Fatal("Step_2 retains part of the first replacement", retained)
	}
	for _, tc := range []struct { input string; stage, region int; noDiacritics, accepted bool; want string }{
		{"|skijima", 2, 1, true, true, "|sk"},
		{"|skijima", 2, 2, true, false, "|skijima"},
		{"|enom", 3, 1, true, true, "|"},
		{"|enom", 3, 2, true, false, "|enom"},
		{"žacima", 2, 0, false, false, "žacima"},
		{"|cajan", 1, 0, true, true, "|cajni"},
		{"žcajan", 1, 0, false, false, "žcajan"},
		{"|baci", 1, 0, true, true, "|baci"},
	} {
		w := serbianWord{prefix: tc.input}
		rules, maxLength := serbianStep1[:], serbianStep1Max
		if tc.stage == 2 { rules, maxLength = serbianStep2[:], serbianStep2Max }
		if tc.stage == 3 { rules, maxLength = serbianStep3[:], serbianStep3Max }
		ok := w.step(rules, maxLength, tc.region, tc.noDiacritics)
		if got := w.prefix+w.tail; got != tc.want || ok != tc.accepted { t.Errorf("%+v: %q/%t", tc, got, ok) }
	}
	for _, pair := range [][2]string{
		{"abadžijama", "abadžij"}, {"abakusa", "abakus"}, {"abdiciranjem", "abdiciran"},
		{"abnormalnost", "abnormaln"}, {"abnormalnosti", "abnormalnost"},
		{"abnormalnostima", "abnormalnost"}, {"acetat", "aceta"}, {"acetata", "acetat"},
		{"rad", "rad"}, {"рад", "rad"}, {"РАД", "РАД"},
		{"ba", "ba"}, {"baba", "bab"}, {"rba", "rba"}, {"rbba", "rbb"},
		// loga/oga cross p1=2: neither stage retries the shorter final a.
		{"lozi", "loga"}, {"pesi", "peh"},
	} { if got := serbianStem(pair[0]); got != pair[1] { t.Errorf("%q: %q want %q", pair[0], got, pair[1]) } }
}

func TestSnowballSerbianRegions(t *testing.T) {
	for _, tc := range []struct { word string; p1 int; plain bool }{
		{"", 0, true}, {"a", 1, true}, {"aa", 1, true}, {"aab", 3, true},
		{"ba", 2, true}, {"baba", 2, true}, {"rba", 3, true}, {"rbba", 2, true},
		{"r", 1, true}, {"rr", 2, true}, {"rrbba", 3, true},
		{"črbbb", 3, false}, {"čba", 4, false}, {"Čba", 4, true},
	} {
		p1, plain := serbianRegions(tc.word)
		if p1 != tc.p1 || plain != tc.plain { t.Errorf("%q: %d/%t want %d/%t", tc.word, p1, plain, tc.p1, tc.plain) }
	}
	for _, pair := range [][2]string{
		{"а", "a"}, {"б", "b"}, {"в", "v"}, {"г", "g"}, {"д", "d"}, {"ђ", "đ"},
		{"е", "e"}, {"ж", "ž"}, {"з", "z"}, {"и", "i"}, {"ј", "j"}, {"к", "k"},
		{"л", "l"}, {"љ", "lj"}, {"м", "m"}, {"н", "n"}, {"њ", "nj"}, {"о", "o"},
		{"п", "p"}, {"р", "r"}, {"с", "s"}, {"т", "t"}, {"ћ", "ć"}, {"у", "u"},
		{"ф", "f"}, {"х", "h"}, {"ц", "c"}, {"ч", "č"}, {"џ", "dž"}, {"ш", "š"},
		{"АБВГДЂЕЖЗИЈКЛЉМНЊОПРСТЋУФХЦЧЏШ", "АБВГДЂЕЖЗИЈКЛЉМНЊОПРСТЋУФХЦЧЏШ"},
	} { if got := serbianTransliterate(pair[0]); got != pair[1] { t.Errorf("transliterate %q: %q want %q", pair[0], got, pair[1]) } }
	for _, pair := range [][2]string{
		{"bijeb", "beb"}, {"bjeb", "beb"}, {"djb", "đb"},
		{"bijebijeb", "bebeb"}, {"bjebjeb", "bebeb"}, {"djdj", "đđ"},
		{"!ijeb", "!ijeb"}, {"bijex", "bijex"}, {"xijeb", "xijeb"},
		{"bje!", "bje!"}, {"bijé b", "bijé b"}, {"čijeđ", "čeđ"},
	} { if got := serbianPrelude(pair[0]); got != pair[1] { t.Errorf("prelude %q: %q want %q", pair[0], got, pair[1]) } }
}
