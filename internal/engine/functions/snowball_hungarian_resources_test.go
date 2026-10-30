package functions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestSnowballHungarianResources(t *testing.T) {
	ctx := t.Context()
	t.Run("canceled-uncached-load", func(t *testing.T) {
		dictionaryStopwords.Lock()
		saved, existed := dictionaryStopwords.words["hungarian"]
		delete(dictionaryStopwords.words, "hungarian")
		dictionaryStopwords.Unlock()
		defer func() {
			dictionaryStopwords.Lock()
			defer dictionaryStopwords.Unlock()
			if existed {
				dictionaryStopwords.words["hungarian"] = saved
			} else {
				delete(dictionaryStopwords.words, "hungarian")
			}
		}()
		loading, cancel := context.WithCancel(ctx)
		defer cancel()
		scan := &germanLoadCancelContext{Context: loading, cancel: cancel}
		d, err := CompileSnowballDictionary(scan, "hungarian", "hungarian")
		if d != nil || !errors.Is(err, context.Canceled) || !errors.Is(loading.Err(), context.Canceled) {
			t.Fatal("canceled scan published runtime", d, err)
		}
		dictionaryStopwords.Lock()
		_, published := dictionaryStopwords.words["hungarian"]
		dictionaryStopwords.Unlock()
		if published {
			t.Fatal("canceled scan published backing")
		}
	})
	stock, err := DefaultHungarianSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DefaultHungarianSnowballDictionary()
	if err != nil || stock != again || !stock.Configured("hungarian", "hungarian") || stock.Filtering() {
		t.Fatal("shared stock runtime authority", again, err)
	}
	// Independently transcribed release inventory, including duplicate majd.
	stops := strings.Fields("a ahogy ahol aki akik akkor alatt által általában amely amelyek amelyekben amelyeket amelyet amelynek ami amit amolyan amíg amikor át abban ahhoz annak arra arról az azok azon azt azzal azért aztán azután azonban bár be belül benne cikk cikkek cikkeket csak de e eddig egész egy egyes egyetlen egyéb egyik egyre ekkor el elég ellen elő először előtt első én éppen ebben ehhez emilyen ennek erre ez ezt ezek ezen ezzel ezért és fel felé hanem hiszen hogy hogyan igen így illetve ill. ill ilyen ilyenkor ison ismét itt jó jól jobban kell kellett keresztül keressünk ki kívül között közül legalább lehet lehetett legyen lenne lenni lesz lett maga magát majd majd már más másik meg még mellett mert mely melyek mi mit míg miért milyen mikor minden mindent mindenki mindig mint mintha mivel most nagy nagyobb nagyon ne néha nekem neki nem néhány nélkül nincs olyan ott össze ő ők őket pedig persze rá s saját sem semmi sok sokat sokkal számára szemben szerint szinte talán tehát teljes tovább továbbá több úgy ugyanis új újabb újra után utána utolsó vagy vagyis valaki valami valamint való vagyok van vannak volt voltam voltak voltunk vissza vele viszont volna")
	sorted := slices.Clone(stops)
	slices.Sort(sorted)
	if len(stops) != 199 || !slices.Equal(stock.words, sorted) {
		t.Fatal("released stopword inventory", len(stops), len(stock.words))
	}
	check := func(d *SnowballDictionary, input, want string) {
		t.Helper()
		got, err := d.Lexize(ctx, input)
		if err != nil || !got.Recognized || want == "" && len(got.Lexemes) != 0 || want != "" && (len(got.Lexemes) != 1 || got.Lexemes[0].Text != want) {
			t.Error(input, got, want, err)
			return
		}
		for _, lexeme := range got.Lexemes {
			if lexeme.Prefix || lexeme.Variant != 0 {
				t.Error("flags", lexeme)
			}
		}
	}
	for _, stop := range stops {
		check(stock, stop, "")
		upper := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' {
				return r - 'a' + 'A'
			}
			return r
		}, stop)
		check(stock, upper, "")
	}
	for _, tc := range [][2]string{{"cikk", ""}, {"cikkben", "cik"}, {"ahol", ""}, {"aholban", "ahol"}, {"HÁZAK", "hÁzak"}, {"Házak", "ház"}, {"ÉS", "És"}, {"Ő", "Ő"}, {"RATS", "rats"}, {"ONLY", "only"}, {"házak!", "házak!"}, {"házak ", "házak "}, {"", ""},
		{"b" + strings.Repeat("a", 998) + "t", "b" + strings.Repeat("a", 996)},
		{"B" + strings.Repeat("A", 999) + "T", "b" + strings.Repeat("a", 999) + "t"},
		{"b" + strings.Repeat("a", 996) + "át", "b" + strings.Repeat("a", 996)},
		{"b" + strings.Repeat("a", 997) + "át", "b" + strings.Repeat("a", 997) + "át"},
	} {
		check(stock, tc[0], tc[1])
	}
	for range 3 {
		d, err := CompileSnowballDictionary(ctx, "hungarian", "hungarian")
		simple, se := CompileSimpleDictionary(ctx, true, "hungarian")
		cross, ce := CompileSnowballDictionary(ctx, "english", "hungarian")
		if err != nil || se != nil || ce != nil {
			t.Fatal(err, se, ce)
		}
		if &d.words[0] != &stock.words[0] || &simple.words[0] != &stock.words[0] || &cross.words[0] != &stock.words[0] {
			t.Fatal("same resource does not share immutable backing")
		}
		check(cross, "cikk", "")
	}
	for _, name := range []string{"english", "danish", "dutch", "german"} {
		other, err := CompileSnowballDictionary(ctx, "hungarian", name)
		if err != nil || &other.words[0] == &stock.words[0] {
			t.Fatal("distinct resource backing", name, err)
		}
	}
	free, err := CompileSnowballDictionary(ctx, "hungarian", "")
	if err != nil || free.words != nil {
		t.Fatal(free, err)
	}
	check(free, "cikk", "cik")
	mixed, err := CompileSnowballDictionary(ctx, "HuNgArIaN", "hungarian")
	if err != nil || mixed.provider != stock.provider || !mixed.Configured("HuNgArIaN", "hungarian") || mixed.Configured("hungarian", "hungarian") {
		t.Fatal("configured spelling/provider authority", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "hungarian", "hungarian"); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := stock.Lexize(canceled, "házak"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, name := range []string{"hungarian_missing", "Hungarian", "../hungarian"} {
		if d, err := CompileSnowballDictionary(ctx, "hungarian", name); d != nil || err == nil {
			t.Fatal(name, d, err)
		}
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["hungarian_missing"]
	_, empty := dictionaryStopwords.words[""]
	backing := dictionaryStopwords.words["hungarian"]
	dictionaryStopwords.Unlock()
	if missing || empty || len(backing) != 199 || &backing[0] != &stock.words[0] {
		t.Fatal("failed/empty load poisoned backing")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(stock, "házak", "ház")
				check(stock, "cikk", "")
				check(free, "cikk", "cik")
			}
		})
	}
	wg.Wait()
}
