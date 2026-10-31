package functions

import (
	"strings"
	"testing"
	"unsafe"
)

func TestSnowballRomanianRules(t *testing.T) {
	// Test-owned release transcriptions, not the implementation's rule tables.
	// Isolate stages before prelude/later stages obscure their exact actions.
	for _, group := range []struct{ endings, replacement string }{
		{"ul ului", ""}, {"aua", "a"}, {"ea ele elor", "e"},
		{"ii iua iei iile iilor ilor ile", "i"}, {"atei", "at"}, {"ație ația", "ați"},
	} {
		for ending := range strings.FieldsSeq(group.endings) {
			w := romanianWord{prefix: "bacac" + ending}
			if !w.article(3) || w.prefix+w.tail != "bacac"+group.replacement {
				t.Error("article", ending, w)
			}
			rejected := romanianWord{prefix: "bacac" + ending}
			if rejected.article(6) || rejected.prefix+rejected.tail != "bacac"+ending {
				t.Error("article R1", ending, rejected)
			}
		}
	}
	for _, group := range []struct{ endings, replacement string }{
		{"abilitate abilitati abilităi abilități", "abil"}, {"ibilitate", "ibil"},
		{"ivitate ivitati ivităi ivități", "iv"},
		{"icitate icitati icităi icități icator icatori iciv iciva icive icivi icivă ical icala icale icali icală", "ic"},
		{"ativ ativa ative ativi ativă ațiune atoare ator atori ătoare ător ători", "at"},
		{"itiv itiva itive itivi itivă ițiune itoare itor itori", "it"},
	} {
		for ending := range strings.FieldsSeq(group.endings) {
			w := romanianWord{prefix: "babab" + ending}
			if !w.combine(3) || w.prefix+w.tail != "babab"+group.replacement {
				t.Error("combining", ending, w)
			}
			rejected := romanianWord{prefix: "babab" + ending}
			if rejected.combine(6) || rejected.prefix+rejected.tail != "babab"+ending {
				t.Error("combining R1", ending, rejected)
			}
		}
	}
	for _, group := range []struct{ endings, prefix, result string }{
		{"at ata ată ati ate ut uta ută uti ute it ita ită iti ite ic ica ice ici ică abil abila abile abili abilă ibil ibila ibile ibili ibilă oasa oasă oase os osi oși ant anta ante anti antă ator atori itate itati ităi ități iv iva ive ivi ivă", "babab", "babab"},
		{"iune iuni", "bababț", "bababt"},
		{"ism isme ist ista iste isti istă iști", "babab", "bababist"},
	} {
		for ending := range strings.FieldsSeq(group.endings) {
			w := romanianWord{prefix: group.prefix + ending}
			if !w.standard(5) || w.prefix+w.tail != group.result {
				t.Error("standard", ending, w)
			}
			rejected := romanianWord{prefix: group.prefix + ending}
			if rejected.standard(len(group.prefix)+1) || rejected.prefix+rejected.tail != group.prefix+ending {
				t.Error("standard R2", ending, rejected)
			}
		}
	}
	for _, endings := range []string{
		"are ere ire âre ind ând indu ându eze ească ez ezi ează esc ești ește ăsc ăști ăște am ai au eam eai ea eați eau iam iai ia iați iau ui ași arăm arăți ară uși urăm urăți ură iși irăm irăți iră âi âși ârăm ârăți âră asem aseși ase aserăm aserăți aseră isem iseși ise iserăm iserăți iseră âsem âseși âse âserăm âserăți âseră usem useși use userăm userăți useră",
		"ăm ați em eți im iți âm âți seși serăm serăți seră sei se sesem seseși sese seserăm seserăți seseră",
	} {
		for ending := range strings.FieldsSeq(endings) {
			w := romanianWord{prefix: "babab" + ending}
			if !w.verb(3) || w.prefix+w.tail != "babab" {
				t.Error("verb", ending, w)
			}
			rejected := romanianWord{prefix: "babab" + ending}
			if rejected.verb(rejected.length()) {
				t.Error("verb outside RV", ending, rejected)
			}
		}
	}
	for ending := range strings.FieldsSeq("a e i ie ă") {
		w := romanianWord{prefix: "babab" + ending}
		if !w.vowel(3) || w.prefix+w.tail != "babab" {
			t.Error("vowel", ending, w)
		}
	}
	for _, tc := range []struct {
		input      string
		rv, r1, r2 int
	}{
		{"", 0, 0, 0}, {"a", 1, 1, 1}, {"bababare", 3, 3, 5},
		{"abie", 3, 2, 4}, {"aababa", 3, 3, 5}, {"brăbaba", 4, 5, 7},
		{"ăâbc", 5, 5, 6}, {"bcdf", 4, 4, 4},
	} {
		s := romanianPrelude(tc.input)
		r1 := romanianRegion(s, 0)
		if rv, r2 := romanianRV(s), romanianRegion(s, r1); rv != tc.rv || r1 != tc.r1 || r2 != tc.r2 {
			t.Error("fixed UTF8 regions", tc, rv, r1, r2)
		}
	}
	for _, tc := range [][2]string{
		{"şţaiua", "șțaIua"}, {"aiaia", "aIaIa"}, {"ȘŢ", "ȘŢ"},
	} {
		if got := romanianPrelude(tc[0]); got != tc[1] {
			t.Error("normalization/prelude", tc, got)
		}
	}
	for _, tc := range []struct {
		input   string
		stage   string
		region  int
		success bool
		want    string
	}{
		{"tabile", "article", 3, false, "tabile"},
		{"ababilitate", "combine", 3, false, "ababilitate"},
		{"bababile", "standard", 5, false, "bababile"},
		{"bababiune", "standard", 5, false, "bababiune"},
		{"abețiune", "standard", 4, true, "abet"},
		{"cabare", "verb", 3, false, "cabare"},
		{"bababaare", "verb", 3, false, "bababaare"},
		{"bababuare", "verb", 3, true, "bababu"},
		{"cabase", "verb", 3, false, "cabase"},
		// RV excludes longest aseră but admits shorter unconditional seră.
		{"baseră", "verb", 2, true, "ba"},
		{"abie", "vowel", 3, false, "abie"},
	} {
		w := romanianWord{prefix: tc.input}
		var success bool
		switch tc.stage {
		case "article":
			success = w.article(tc.region)
		case "combine":
			success = w.combine(tc.region)
		case "standard":
			success = w.standard(tc.region)
		case "verb":
			success = w.verb(tc.region)
		case "vowel":
			success = w.vowel(tc.region)
		}
		if success != tc.success || w.prefix+w.tail != tc.want {
			t.Error("phase boundary", tc, success, w)
		}
	}
	for _, tc := range [][2]string{
		{"abruptă", "abrupt"}, {"abrutizare", "abrutiz"}, {"abrutizanţi", "abrutizanț"},
		{"ocolul", "ocol"}, {"ocolește", "ocol"}, {"absenteişti", "absenteist"},
		{"bababaua", "bababau"}, {"bababiua", "bababiu"}, {"bababile", "bababil"},
		{"bababibile", "bababib"}, {"bababibil", "babab"}, {"bababativitate", "babab"},
		{"ababilitate", "ababil"}, {"abilitate", "abil"}, {"bababareic", "bababar"},
		{"bababțiune", "bababt"}, {"bababistă", "bababist"}, {"aia", "aia"}, {"baia", "bai"},
		{"abruptă!", "abruptă!"}, {"abrupta\u0306", "abrupta\u0306"}, {"café", "café"},
		{"IU", "iu"}, {"running", "running"}, {"only", "only"},
	} {
		if got := romanianStem(tc[0]); got != tc[1] {
			t.Error("whole kernel", tc, got)
		}
	}
	for _, r := range "aeiouăâî" {
		if !romanianVowel(r) {
			t.Error("released vowel", r)
		}
	}
	for _, r := range "AEIOUáéíóúyșț" {
		if romanianVowel(r) {
			t.Error("not a released vowel", r)
		}
	}
	unchanged := "bcdf"
	if got := romanianStem(unchanged); unsafe.StringData(got) != unsafe.StringData(unchanged) {
		t.Fatal("unchanged word copied")
	}
}
