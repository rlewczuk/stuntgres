package functions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestSnowballNorwegianRules(t *testing.T) {
	// Independent release actions, not the production suffix tables.
	cases := [][2]string{
		{"aabakken", "aabakk"}, {"absorberes", "absorber"}, {"adkomstdokumenter", "adkomstdokument"},
		{"administrative", "administrativ"}, {"administrativt", "administrativ"}, {"administrert", "administrer"},
		{"adopsjonspenger", "adopsjonspeng"}, {"adresse", "adress"}, {"adresser", "adress"},
		{"advokatfullmektig", "advokatfullmekt"}, {"akkumulerte", "akkumuler"},
		{"aksjelov", "aksj"}, {"aksjelova", "aksj"}, {"aksjeloven", "aksj"}, {"aksjelovens", "aksj"},
		{"aksjerettslig", "aksjeretts"}, {"aksjeselskapsrettslege", "aksjeselskapsretts"},
		{"bacert", "bacer"}, {"bacerte", "bacer"}, {"bacdt", "bacd"}, {"bacvt", "bacv"},
		{"bacelighetene", "bac"}, {"bacdtene", "bacd"}, {"baclegleg", "bacleg"},
		{"bacels", "bacel"}, {"bacelsene", "bac"},
		{"baca", "bac"}, {"aba", "aba"}, {"aben", "aben"}, {"abene", "aben"},
		{"abert", "abert"}, {"aberte", "abert"}, {"badt", "badt"},
		{"bacgd", "bacgd"}, {"bacgt", "bacgt"}, {"backt", "backt"},
		{"baks", "baks"}, {"bakks", "bakk"}, {"abks", "abk"}, {"abers", "aber"},
		{"bankers", "bank"}, {"tappers", "tapp"}, {"bacêks", "bacêk"},
		{"bacòks", "bacòk"}, {"bacóks", "bacók"}, {"bacôks", "bacôk"},
		{"cd'en", "cd'en"}, {"båtene!", "båtene!"}, {"running", "running"},
	}
	for _, suffix := range strings.Fields("a e ede ande ende ane ene hetene en heten ar er heter as es edes endes enes hetenes ens hetens ers ets et het ast leg eleg ig eig lig elig lov elov slov hetslov") {
		cases = append(cases, [2]string{"bac" + suffix, "bac"})
	}
	for _, predecessor := range "bcdfghjlmnoprtvyz" {
		prefix := "bac" + string(predecessor)
		cases = append(cases, [2]string{prefix + "s", prefix})
	}
	for _, predecessor := range "iuæåøqwxs" {
		word := "bac" + string(predecessor) + "s"
		cases = append(cases, [2]string{word, word})
	}
	for _, tc := range cases {
		if got := norwegianStem(tc[0]); got != tc[1] {
			t.Errorf("%q: got %q, want %q", tc[0], got, tc[1])
		}
	}
	for _, tc := range []struct {
		word string
		want int
	}{{"", 0}, {"aa", 2}, {"bacene", 3}, {"båtene", 4}, {"åååbene", 7}, {"cd'en", 5}} {
		if got := norwegianRegion(tc.word); got != tc.want {
			t.Errorf("region %q: got %d, want %d", tc.word, got, tc.want)
		}
	}
}

func TestSnowballNorwegianResources(t *testing.T) {
	ctx := t.Context()
	d, err := DefaultNorwegianSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	// Literal independently transcribed REL_18_6 stoplist, including duplicates.
	stops := strings.Fields("og i jeg det at en et den til er som på de med han av ikke ikkje der så var meg seg men ett har om vi min mitt ha hadde hun nå over da ved fra du ut sin dem oss opp man kan hans hvor eller hva skal selv sjøl her alle vil bli ble blei blitt kunne inn når være kom noen noe ville dere som deres kun ja etter ned skulle denne for deg si sine sitt mot å meget hvorfor dette disse uten hvordan ingen din ditt blir samme hvilken hvilke sånn inni mellom vår hver hvem vors hvis både bare enn fordi før mange også slik vært være båe begge siden dykk dykkar dei deira deires deim di då eg ein eit eitt elles honom hjå ho hoe henne hennar hennes hoss hossen ikkje ingi inkje korleis korso kva kvar kvarhelst kven kvi kvifor me medan mi mine mykje no nokon noka nokor noko nokre si sia sidan so somt somme um upp vere vore verte vort varte vart")
	frequencies := map[string]int{}
	for _, word := range stops {
		frequencies[word]++
	}
	if len(stops) != 176 || len(frequencies) != 172 {
		t.Fatal("independent released inventory", len(stops), len(frequencies))
	}
	for word, count := range frequencies {
		want := 1
		if word == "ikkje" || word == "si" || word == "som" || word == "være" {
			want = 2
		}
		if count != want {
			t.Fatal("duplicate frequency", word, count, want)
		}
	}
	slices.Sort(stops)
	if !slices.Equal(d.words, stops) {
		t.Fatal("full sorted non-deduplicated release list", d.words)
	}
	check := func(runtime *SnowballDictionary, input, want string) {
		t.Helper()
		got, err := runtime.Lexize(ctx, input)
		if err != nil || !got.Recognized || want == "" && len(got.Lexemes) != 0 || want != "" && (len(got.Lexemes) != 1 || got.Lexemes[0].Text != want) {
			t.Error(input, got, want, err)
		}
		for _, lexeme := range got.Lexemes {
			if lexeme.Prefix || lexeme.Variant != 0 {
				t.Error("unexpected lexeme flags", lexeme)
			}
		}
	}
	for _, word := range stops {
		check(d, word, "")
		upper := []byte(word)
		for i, c := range upper {
			if c >= 'a' && c <= 'z' {
				upper[i] -= 'a' - 'A'
			}
		}
		check(d, string(upper), "")
	}
	for _, tc := range [][2]string{
		{"PÅ", "pÅ"}, {"Å", "Å"}, {"VÆRE", "vÆre"}, {"båtene", "båt"}, {"BÅTENE", "bÅten"},
		{"pa", "pa"}, {"pa\u030a", "pa\u030a"}, {"ikkes", "ikk"}, {"", ""}, {" ", " "}, {"båtene!", "båtene!"},
		{strings.Repeat("A", 997) + "ene", strings.Repeat("a", 997) + "en"},
		{strings.Repeat("A", 998) + "ene", strings.Repeat("a", 998) + "ene"},
		{strings.Repeat("å", 498) + "bene", strings.Repeat("å", 498) + "b"},
		{strings.Repeat("å", 499) + "bene", strings.Repeat("å", 499) + "bene"},
	} {
		check(d, tc[0], tc[1])
	}
	free, err := CompileSnowballDictionary(ctx, "norwegian", "")
	if err != nil || free.words != nil {
		t.Fatal("resource-free runtime", free, err)
	}
	check(free, "ikke", "ikk")
	mixed, err := CompileSnowballDictionary(ctx, "NoRwEgIaN", "norwegian")
	if err != nil || mixed.provider != d.provider || !mixed.Configured("NoRwEgIaN", "norwegian") || mixed.Configured("norwegian", "norwegian") {
		t.Fatal("configured spelling and provider authority", mixed, err)
	}
	simple, err := CompileSimpleDictionary(ctx, true, "norwegian")
	if err != nil {
		t.Fatal(err)
	}
	english, err := CompileSnowballDictionary(ctx, "english", "norwegian")
	if err != nil {
		t.Fatal(err)
	}
	if &mixed.words[0] != &d.words[0] || &simple.words[0] != &d.words[0] || &english.words[0] != &d.words[0] {
		t.Fatal("exact resource backing not shared")
	}
	check(english, "ikke", "")
	if got, err := simple.Lexize(ctx, "ikke"); err != nil || !got.Recognized || len(got.Lexemes) != 0 {
		t.Fatal("simple shared resource", got, err)
	}
	for _, resource := range []string{"norwegian_missing", "Norwegian", "../norwegian"} {
		if bad, err := CompileSnowballDictionary(ctx, "norwegian", resource); bad != nil || err == nil {
			t.Fatal(resource, bad, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if bad, err := CompileSnowballDictionary(canceled, "norwegian", "norwegian"); bad != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(bad, err)
	}
	if _, err := d.Lexize(canceled, "BILER"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["norwegian_missing"]
	_, empty := dictionaryStopwords.words[""]
	backing := dictionaryStopwords.words["norwegian"]
	dictionaryStopwords.Unlock()
	if missing || empty || &backing[0] != &d.words[0] || !slices.Equal(backing, stops) {
		t.Fatal("failed compilation changed immutable cache")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(d, "BILER", "bil")
				check(d, "ikke", "")
				check(free, "ikke", "ikk")
			}
		})
	}
	wg.Wait()
}
