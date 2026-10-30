package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSnowballDanishRules(t *testing.T) {
	for _, tc := range [][2]string{
		{"hunde", "hund"}, {"hundene", "hund"}, {"huse", "hus"}, {"huset", "hus"}, {"husets", "hus"}, {"bilerne", "bil"}, {"biler", "bil"}, {"katte", "kat"}, {"kat", "kat"},
		{"arbejde", "arbejd"}, {"bestemmelse", "bestem"}, {"indtagelse", "indtag"}, {"indtræffe", "indtræf"}, {"undersøgelser", "undersøg"}, {"hurtigst", "hurt"}, {"meningsløst", "meningsløs"},
		{"babhed", "bab"}, {"babethed", "bab"}, {"babered", "bab"}, {"babe", "bab"}, {"baberede", "bab"}, {"babende", "bab"}, {"baberende", "bab"}, {"babene", "bab"}, {"baberne", "bab"}, {"babere", "bab"}, {"baben", "bab"}, {"babheden", "bab"}, {"baberen", "bab"}, {"baber", "bab"}, {"babheder", "bab"}, {"baberer", "bab"},
		{"babheds", "bab"}, {"babes", "bab"}, {"babendes", "bab"}, {"baberendes", "bab"}, {"babenes", "bab"}, {"babernes", "bab"}, {"baberes", "bab"}, {"babens", "bab"}, {"babhedens", "bab"}, {"baberens", "bab"}, {"babers", "bab"}, {"babets", "bab"}, {"baberets", "bab"}, {"babet", "bab"}, {"baberet", "bab"}, {"babs", "bab"},
		{"babgd", "babg"}, {"babdt", "babd"}, {"babgt", "babg"}, {"babkt", "babk"}, {"friskt", "frisk"}, {"lagt", "lagt"},
		{"babig", "bab"}, {"bablig", "bab"}, {"babelig", "bab"}, {"babels", "babel"}, {"babløst", "babløs"}, {"babgdig", "babg"}, {"babigst", "bab"}, {"igst", "ig"}, {"ligst", "lig"},
		{"babb", "bab"}, {"abb", "abb"}, {"babbb", "babb"}, {"babjj", "babj"}, {"babvv", "babv"}, {"bab11", "bab11"}, {"babéé", "babéé"}, {"babææ", "babææ"},
		{"ben", "ben"}, {"heden", "hed"}, {"bælige", "bæl"}, {"løst", "løst"}, {"babås", "babå"}, {"babæs", "babæs"}, {"babøs", "babøs"}, {"babus", "babus"}, {"babws", "babws"},
		{"cd'en", "cd'en"}, {"abc'", "abc'"}, {"hundene!", "hundene!"}, {"hundene\u0301", "hundene\u0301"}, {"bøger", "bøg"}, {"bØger", "bØger"}, {"cafés", "cafés"},
	} {
		if got := danishStem(tc[0]); got != tc[1] {
			t.Errorf("%q -> %q, want %q", tc[0], got, tc[1])
		}
	}
	// els normally loses its final s in main_suffix first; exercise the
	// released other_suffix branch independently, without changing stage order.
	for _, tc := range []struct {
		word string
		r1   int
		want string
	}{
		{"babels", 3, "bab"}, {"bælig", 4, "bæl"}, {"babgdig", 3, "babg"}, {"babløst", 3, "babløs"}, {"igst", 4, "ig"},
	} {
		if got := danishOtherSuffix(tc.word, tc.r1); got != tc.want {
			t.Fatal("other suffix", tc, got)
		}
	}
}

func TestSnowballDanishResources(t *testing.T) {
	ctx := t.Context()
	english, err := CompileSnowballDictionary(ctx, "english", "english")
	if err != nil {
		t.Fatal(err)
	}
	danish, err := DefaultDanishSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if len(english.words) != 127 || len(danish.words) != 94 || &english.words[0] == &danish.words[0] {
		t.Fatal("distinct released resource backing")
	}
	stops := strings.Fields("og i jeg det at en den til er som på de med han af for ikke der var mig sig men et har om vi min havde ham hun nu over da fra du ud sin dem os op man hans hvor eller hvad skal selv her alle vil blev kunne ind når være dog noget ville jo deres efter ned skulle denne end dette mit også under have dig anden hende mine alt meget sit sine vor mod disse hvis din nogle hos blive mange ad bliver hendes været thi jer sådan")
	if len(stops) != 94 {
		t.Fatal("independent released stopword inventory", len(stops))
	}
	check := func(d *SnowballDictionary, input, want string) {
		t.Helper()
		got, err := d.Lexize(ctx, input)
		if err != nil || !got.Recognized || want == "" && len(got.Lexemes) != 0 || want != "" && (len(got.Lexemes) != 1 || got.Lexemes[0].Text != want) {
			t.Fatal(input, got, want, err)
		}
		for _, lexeme := range got.Lexemes {
			if lexeme.Prefix || lexeme.Variant != 0 {
				t.Fatal("unexpected Danish lexeme flags", lexeme)
			}
		}
	}
	for _, word := range stops {
		check(danish, word, "")
		mixed := []byte(word)
		for i, c := range mixed {
			if c >= 'a' && c <= 'z' {
				mixed[i] -= 'a' - 'A'
			}
		}
		check(danish, string(mixed), "")
	}
	for range 3 {
		for _, name := range []string{"english", "danish"} {
			d, err := CompileSnowballDictionary(ctx, name, name)
			if err != nil {
				t.Fatal(err)
			}
			s, err := CompileSimpleDictionary(ctx, true, name)
			if err != nil {
				t.Fatal(err)
			}
			original := english
			if name == "danish" {
				original = danish
			}
			if &d.words[0] != &original.words[0] || &s.words[0] != &original.words[0] {
				t.Fatal("resource sharing changed", name)
			}
			if name == "danish" {
				check(d, "OG", "")
				check(d, "THE", "the")
			} else {
				check(d, "THE", "")
				check(d, "OG", "og")
			}
		}
	}
	check(danish, "vores", "vor")
	check(danish, "vor", "")
	check(danish, "PÅ", "pÅ")
	check(danish, "HUNDENE", "hund")
	check(danish, "BØGER", "bØger")
	check(danish, "CAFÉS", "cafÉs")
	for _, tc := range [][2]string{
		{"bab" + strings.Repeat("a", 995) + "er", "bab" + strings.Repeat("a", 995)},
		{"BAB" + strings.Repeat("A", 996) + "er", "bab" + strings.Repeat("a", 996) + "er"},
		{"bab" + strings.Repeat("æ", 497) + "aer", "bab" + strings.Repeat("æ", 497) + "a"},
		{"BAB" + strings.Repeat("æ", 498) + "er", "bab" + strings.Repeat("æ", 498) + "er"},
	} {
		check(danish, tc[0], tc[1])
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "danish", "danish"); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := danish.Lexize(canceled, "HUNDENE"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, resource := range []string{"danish_missing", "Danish", "../danish"} {
		if d, err := CompileSnowballDictionary(ctx, "danish", resource); d != nil || err == nil {
			t.Fatal(resource, d, err)
		}
	}
	free, err := CompileSnowballDictionary(ctx, "danish", "")
	if err != nil || free.words != nil {
		t.Fatal("resource-free runtime", free, err)
	}
	check(free, "OG", "og")
	check(free, "anden", "and")
	check(free, "vores", "vor")
	dictionaryStopwords.Lock()
	_, emptyCached := dictionaryStopwords.words[""]
	_, missingCached := dictionaryStopwords.words["danish_missing"]
	englishBacking := dictionaryStopwords.words["english"]
	danishBacking := dictionaryStopwords.words["danish"]
	dictionaryStopwords.Unlock()
	if emptyCached || missingCached || len(englishBacking) != 127 || len(danishBacking) != 94 || &englishBacking[0] != &english.words[0] || &danishBacking[0] != &danish.words[0] {
		t.Fatal("failed/empty compile changed immutable resource cache")
	}
	a, err := CompileSnowballDictionary(ctx, "DaNiSh", "danish")
	if err != nil || a.provider != danish.provider || !a.Configured("DaNiSh", "danish") || a.Configured("danish", "danish") {
		t.Fatal("provider spelling", a, err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(danish, "HUNDENE", "hund")
				check(danish, "OG", "")
				check(english, "THE", "")
				check(english, "OG", "og")
			}
		})
	}
	wg.Wait()
}
