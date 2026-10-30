package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSnowballDutchRules(t *testing.T) {
	// Literal release-source expectations, also exercised by the public corpus.
	for _, tc := range [][2]string{
		{"honden", "hond"}, {"bomen", "bom"}, {"huizen", "huiz"}, {"katten", "kat"}, {"mannen", "mann"}, {"mussen", "muss"},
		{"fietsje", "fietsj"}, {"fietsjes", "fietsjes"}, {"gewerkt", "gewerkt"}, {"maan", "man"}, {"brood", "brod"}, {"buur", "bur"}, {"been", "ben"}, {"lichtje", "lichtj"}, {"lichtjes", "lichtjes"}, {"lichamelijkheden", "licham"},
		{"bababheden", "babab"}, {"babheden", "babheid"}, {"bababen", "babab"}, {"bababene", "babab"}, {"babakken", "babak"}, {"babadden", "babad"}, {"babatten", "babat"}, {"babben", "babb"}, {"babagemen", "babagemen"}, {"bababaen", "bababaen"},
		{"bababs", "babab"}, {"bababse", "babab"}, {"bababjs", "bababjs"}, {"bababys", "bababys"}, {"bababjse", "bababjs"}, {"bas", "bas"}, {"babs", "bab"}, {"heden", "heden"}, {"abheden", "abheden"},
		{"bababe", "babab"}, {"babakke", "babak"}, {"babaae", "babaae"}, {"bababheid", "babab"}, {"bababcheid", "bababcheid"},
		{"bababing", "babab"}, {"bababend", "babab"}, {"bababig", "babab"}, {"bababeig", "bababeig"}, {"bababiging", "babab"}, {"bababeiging", "bababeig"}, {"bababkking", "bababk"},
		{"bababelijk", "babab"}, {"bababbaar", "babab"}, {"bababbar", "bababbar"}, {"bababbare", "babab"}, {"ababelijk", "abab"}, {"baaien", "baai"}, {"äben", "aben"}, {"äbben", "abb"},
		{"yaya", "yaya"}, {"aia", "aia"}, {"bababbenheid", "bababb"},
		{"café", "caf"}, {"cafés", "cafes"}, {"cafÉ", "cafÉ"}, {"cafÉs", "cafÉ"}, {"cafe\u0301", "cafe\u0301"}, {"café!", "cafe!"}, {"", ""},
	} {
		if got := dutchStem(tc[0]); got != tc[1] {
			t.Errorf("%q -> %q, want %q", tc[0], got, tc[1])
		}
	}
	t.Run("prelude", func(t *testing.T) {
		for _, tc := range [][2]string{{"áäéëíïóöúüè", "aaeeIioouuè"}, {"yaya", "YaYa"}, {"aia", "aIa"}, {"ia", "ia"}, {"ayaia", "aYaIa"}, {"É", "É"}} {
			w := []rune(tc[0])
			dutchPrelude(w)
			if string(w) != tc[1] {
				t.Fatal(tc, string(w))
			}
		}
	})
	t.Run("regions", func(t *testing.T) {
		for _, tc := range []struct {
			word   string
			r1, r2 int
		}{{"", 0, 0}, {"a", 1, 1}, {"ab", 2, 2}, {"ababelijk", 3, 4}, {"äben", 3, 4}, {"bababheden", 3, 5}, {"aeiou", 3, 5}} {
			w := []rune(tc.word)
			dutchPrelude(w)
			r1, r2 := dutchRegions(w)
			if r1 != tc.r1 || r2 != tc.r2 {
				t.Fatal(tc, r1, r2)
			}
		}
	})
	t.Run("undouble", func(t *testing.T) {
		for _, tc := range [][2]string{{"badd", "bad"}, {"bakk", "bak"}, {"batt", "bat"}, {"bann", "bann"}, {"bass", "bass"}, {"", ""}} {
			if got := string(dutchUndouble([]rune(tc[0]))); got != tc[1] {
				t.Fatal(tc, got)
			}
		}
	})
}

func TestSnowballDutchResources(t *testing.T) {
	ctx := t.Context()
	dutch, err := DefaultDutchSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	english, err := DefaultSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	danish, err := DefaultDanishSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if len(dutch.words) != 101 || len(english.words) != 127 || len(danish.words) != 94 || &dutch.words[0] == &english.words[0] || &dutch.words[0] == &danish.words[0] {
		t.Fatal("distinct released resource backing")
	}
	stops := strings.Fields("de en van ik te dat die in een hij het niet zijn is was op aan met als voor had er maar om hem dan zou of wat mijn men dit zo door over ze zich bij ook tot je mij uit der daar haar naar heb hoe heeft hebben deze u want nog zal me zij nu ge geen omdat iets worden toch al waren veel meer doen toen moet ben zonder kan hun dus alles onder ja eens hier wie werd altijd doch wordt wezen kunnen ons zelf tegen na reeds wil kon niets uw iemand geweest andere")
	if len(stops) != 101 {
		t.Fatal("independent released stop inventory", len(stops))
	}
	check := func(d *SnowballDictionary, input, want string) {
		t.Helper()
		got, err := d.Lexize(ctx, input)
		if err != nil || !got.Recognized || want == "" && len(got.Lexemes) != 0 || want != "" && (len(got.Lexemes) != 1 || got.Lexemes[0].Text != want) {
			t.Fatal(input, got, want, err)
		}
		for _, lexeme := range got.Lexemes {
			if lexeme.Prefix || lexeme.Variant != 0 {
				t.Fatal("unexpected Dutch lexeme flags", lexeme)
			}
		}
	}
	for _, stop := range stops {
		check(dutch, stop, "")
		check(dutch, strings.ToUpper(stop), "")
	}
	for _, tc := range [][2]string{
		{"HONDEN", "hond"}, {"dé", "de"}, {"CAFÉ", "cafÉ"}, {"CAFÉS", "cafÉ"}, {"café!", "cafe!"}, {"A B", "a b"}, {"", ""},
		{"bab" + strings.Repeat("a", 995) + "bs", "bab" + strings.Repeat("a", 995) + "b"},
		{"BAB" + strings.Repeat("A", 996) + "bs", "bab" + strings.Repeat("a", 996) + "bs"},
		{"bab" + strings.Repeat("ä", 497) + "abs", "bab" + strings.Repeat("a", 498) + "b"},
		{"BAB" + strings.Repeat("ä", 498) + "bs", "bab" + strings.Repeat("ä", 498) + "bs"},
	} {
		check(dutch, tc[0], tc[1])
	}
	for range 3 {
		for _, name := range []string{"english", "dutch", "danish", "dutch", "english"} {
			d, err := CompileSnowballDictionary(ctx, name, name)
			if err != nil {
				t.Fatal(err)
			}
			s, err := CompileSimpleDictionary(ctx, true, name)
			if err != nil {
				t.Fatal(err)
			}
			original := english
			switch name {
			case "dutch":
				original = dutch
			case "danish":
				original = danish
			}
			if &d.words[0] != &original.words[0] || &s.words[0] != &original.words[0] {
				t.Fatal("same-resource immutable backing", name)
			}
			switch name {
			case "english":
				check(d, "THE", "")
				check(d, "DE", "de")
			case "dutch":
				check(d, "DE", "")
				check(d, "THE", "the")
				check(d, "OG", "og")
				got, err := s.Lexize(ctx, "HONDEN")
				if err != nil || !got.Recognized || len(got.Lexemes) != 1 || got.Lexemes[0].Text != "honden" {
					t.Fatal(got, err)
				}
			case "danish":
				check(d, "OG", "")
				check(d, "THE", "the")
			}
		}
	}
	cross, err := CompileSnowballDictionary(ctx, "english", "dutch")
	if err != nil {
		t.Fatal(err)
	}
	check(cross, "DE", "")
	check(cross, "THE", "the")
	free, err := CompileSnowballDictionary(ctx, "dutch", "")
	if err != nil || free.words != nil {
		t.Fatal("list-free Dutch", free, err)
	}
	check(free, "DE", "de")
	check(free, "HONDEN", "hond")
	mixed, err := CompileSnowballDictionary(ctx, "DuTcH", "dutch")
	if err != nil || mixed.provider != dutch.provider || !mixed.Configured("DuTcH", "dutch") || mixed.Configured("dutch", "dutch") {
		t.Fatal("provider configured spelling", mixed, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "dutch", "dutch"); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := dutch.Lexize(canceled, "HONDEN"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, resource := range []string{"dutch_missing", "Dutch", "../dutch"} {
		if d, err := CompileSnowballDictionary(ctx, "dutch", resource); d != nil || err == nil {
			t.Fatal(resource, d, err)
		}
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["dutch_missing"]
	_, empty := dictionaryStopwords.words[""]
	backing := dictionaryStopwords.words["dutch"]
	dictionaryStopwords.Unlock()
	if missing || empty || len(backing) != 101 || &backing[0] != &dutch.words[0] {
		t.Fatal("failed/empty compile poisoned cache")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(dutch, "HONDEN", "hond")
				check(dutch, "DE", "")
				check(english, "THE", "")
				check(danish, "OG", "")
			}
		})
	}
	wg.Wait()
}
