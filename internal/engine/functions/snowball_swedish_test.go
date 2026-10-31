package functions

import (
	"strings"
	"testing"
	"unsafe"
)

func TestSnowballSwedishRules(t *testing.T) {
	// Independently transcribed historical vocabulary and released rule witnesses.
	cases := [][2]string{
		{"abborrar", "abborr"}, {"abborrarna", "abborr"}, {"abborre", "abborr"}, {"abborrpinne", "abborrpin"},
		{"abrahams", "abraham"}, {"ackompanjerade", "ackompanjer"}, {"adelig", "ade"}, {"adelsfröken", "adelsfrök"},
		{"adelskalendern", "adelskalend"}, {"adelskalendrar", "adelskalendr"}, {"adelskapen", "adelskap"},
		{"adelsmän", "adelsmän"}, {"adelsvapen", "adelsvap"}, {"adertonhundratalet", "adertonhundratalet"},
		{"adjunkt", "adjunk"}, {"adjunkten", "adjunk"}, {"adjö", "adjö"}, {"adlades", "adl"}, {"adlig", "adl"},
		{"adligt", "adl"}, {"adopterade", "adopter"}, {"adress", "adress"}, {"adressen", "adress"}, {"adresserat", "adresser"},
		{"hundar", "hund"}, {"hunden", "hund"}, {"hundens", "hund"}, {"katterna", "katt"}, {"flickorna", "flick"}, {"snabbaste", "snabb"},
		{"", ""}, {"a", "a"}, {"as", "as"}, {"abs", "abs"}, {"hus", "hus"}, {"ben", "ben"}, {"bba", "bba"},
		{"balig", "bal"}, {"heterna", "het"}, {"åba", "åba"}, {"åbade", "åbad"}, {"åbcade", "åbc"},
		{"babels", "babel"}, {"babelsen", "bab"}, {"babdden", "babd"}, {"babliglig", "bablig"},
		{"babig", "bab"}, {"bablig", "bab"}, {"babfullt", "babfull"}, {"meningslöst", "meningslös"},
		{"babiöst", "babiös"}, {"babböst", "babböst"}, {"babbb", "babbb"}, {"babb", "babb"},
		{"hundarna!", "hundarna!"}, {"hundarna\u0301", "hundarna\u0301"}, {"café", "café"}, {"cafés", "cafés"}, {"running", "running"},
	}
	for _, suffix := range strings.Fields("a arna erna heterna orna ad e ade ande arne are aste en anden aren heten ern ar er heter or as arnas ernas ornas es ades andes ens arens hetens erns at andet het ast") {
		cases = append(cases, [2]string{"bac" + suffix, "bac"})
	}
	for _, pair := range strings.Fields("dd gd nn dt gt kt tt") {
		cases = append(cases, [2]string{"bac" + pair, "bac" + pair[:1]})
	}
	for _, predecessor := range "bcdfghjklmnoprtvy" {
		prefix := "bac" + string(predecessor)
		cases = append(cases, [2]string{prefix + "s", prefix})
	}
	for _, predecessor := range "iuäåöqwxzsé" {
		word := "bac" + string(predecessor) + "s"
		cases = append(cases, [2]string{word, word})
	}
	for _, predecessor := range "iklnprtuv" {
		prefix := "bac" + string(predecessor)
		cases = append(cases, [2]string{prefix + "öst", prefix + "ös"})
	}
	for _, tc := range cases {
		got := swedishStem(tc[0])
		if got != tc[1] {
			t.Errorf("%q: got %q, want %q", tc[0], got, tc[1])
		}
		if got != "" && unsafe.StringData(got) != unsafe.StringData(tc[0]) {
			t.Errorf("%q: tail deletion copied input backing", tc[0])
		}
	}
	for _, tc := range []struct {
		word string
		want int
	}{{"", 0}, {"aa", 2}, {"bacarna", 3}, {"åba", 4}, {"åbcade", 4}, {"åååbade", 7}, {"bba", 3}} {
		if got := swedishRegion(tc.word); got != tc.want {
			t.Errorf("region %q: got %d, want %d", tc.word, got, tc.want)
		}
	}
}
