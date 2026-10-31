package functions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestSnowballSwedishResources(t *testing.T) {
	ctx := t.Context()
	t.Run("canceled-uncached-load", func(t *testing.T) {
		dictionaryStopwords.Lock()
		saved, existed := dictionaryStopwords.words["swedish"]
		delete(dictionaryStopwords.words, "swedish")
		dictionaryStopwords.Unlock()
		defer func() {
			dictionaryStopwords.Lock()
			defer dictionaryStopwords.Unlock()
			if existed {
				dictionaryStopwords.words["swedish"] = saved
			} else {
				delete(dictionaryStopwords.words, "swedish")
			}
		}()
		loading, cancel := context.WithCancel(ctx)
		defer cancel()
		scan := &germanLoadCancelContext{Context: loading, cancel: cancel}
		runtime, err := CompileSnowballDictionary(scan, "swedish", "swedish")
		if runtime != nil || !errors.Is(err, context.Canceled) || !errors.Is(loading.Err(), context.Canceled) {
			t.Fatal("canceled scan published runtime", runtime, err)
		}
		dictionaryStopwords.Lock()
		_, published := dictionaryStopwords.words["swedish"]
		dictionaryStopwords.Unlock()
		if published {
			t.Fatal("canceled scan published resource backing")
		}
	})
	d, err := DefaultSwedishSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DefaultSwedishSnowballDictionary()
	if err != nil || again != d || !d.Configured("swedish", "swedish") || d.Filtering() {
		t.Fatal("immutable default singleton/provider authority", again, err)
	}
	// Independent literal REL_18_6 inventory, not read back from the resource.
	stops := strings.Fields("och det att i en jag hon som han på den med var sig för så till är men ett om hade de av icke mig du henne då sin nu har inte hans honom skulle hennes där min man ej vid kunde något från ut när efter upp vi dem vara vad över än dig kan sina här ha mot alla under någon eller allt mycket sedan ju denna själv detta åt utan varit hur ingen mitt ni bli blev oss din dessa några deras blir mina samma vilken er sådan vår blivit dess inom mellan sådant varför varje vilka ditt vem vilket sitta sådana vart dina vars vårt våra ert era vilkas")
	slices.Sort(stops)
	if len(stops) != 114 || !slices.Equal(d.words, stops) {
		t.Fatal("full independently specified released inventory", d.words)
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
		{"", ""}, {"sitta", ""}, {"sitt", "sitt"}, {"PÅ", "pÅ"}, {"HUNDAR", "hund"},
		{"HäSTAR", "häst"}, {"HÄSTAR", "hÄstar"}, {"CAFÉS", "cafÉs"}, {"RUNNING", "running"},
		{"bab" + strings.Repeat("a", 995) + "ar", "bab" + strings.Repeat("a", 995)},
		{"BAB" + strings.Repeat("A", 996) + "ar", "bab" + strings.Repeat("a", 996) + "ar"},
		{"bab" + strings.Repeat("å", 497) + "aar", "bab" + strings.Repeat("å", 497) + "a"},
		{"BAB" + strings.Repeat("å", 498) + "ar", "bab" + strings.Repeat("å", 498) + "ar"},
	} {
		check(d, tc[0], tc[1])
	}
	free, err := CompileSnowballDictionary(ctx, "swedish", "")
	if err != nil || free.words != nil {
		t.Fatal("resource-free runtime", free, err)
	}
	check(free, "sitta", "sitt")
	mixed, err := CompileSnowballDictionary(ctx, "SwEdIsH", "swedish")
	if err != nil || mixed.provider != d.provider || !mixed.Configured("SwEdIsH", "swedish") || mixed.Configured("swedish", "swedish") {
		t.Fatal("configured spelling versus provider authority", mixed, err)
	}
	simple, err := CompileSimpleDictionary(ctx, true, "swedish")
	if err != nil {
		t.Fatal(err)
	}
	english, err := CompileSnowballDictionary(ctx, "english", "swedish")
	if err != nil {
		t.Fatal(err)
	}
	if &mixed.words[0] != &d.words[0] || &simple.words[0] != &d.words[0] || &english.words[0] != &d.words[0] {
		t.Fatal("exact resource backing not shared")
	}
	check(english, "sitta", "")
	if got, err := simple.Lexize(ctx, "sitta"); err != nil || !got.Recognized || len(got.Lexemes) != 0 {
		t.Fatal("simple shared resource", got, err)
	}
	cross, err := CompileSnowballDictionary(ctx, "swedish", "english")
	if err != nil || &cross.words[0] == &d.words[0] {
		t.Fatal("distinct resources alias", cross, err)
	}
	check(cross, "only", "")
	check(cross, "sitta", "sitt")
	for _, resource := range []string{"swedish_missing", "SWEDISH", "../swedish", "svenskå"} {
		if bad, err := CompileSnowballDictionary(ctx, "swedish", resource); bad != nil || err == nil {
			t.Fatal(resource, bad, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if bad, err := CompileSnowballDictionary(canceled, "swedish", "swedish"); bad != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(bad, err)
	}
	if _, err := d.Lexize(canceled, "HUNDAR"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["swedish_missing"]
	_, empty := dictionaryStopwords.words[""]
	backing := dictionaryStopwords.words["swedish"]
	dictionaryStopwords.Unlock()
	if missing || empty || &backing[0] != &d.words[0] || !slices.Equal(backing, stops) {
		t.Fatal("failed compilation changed immutable cache")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(d, "HUNDAR", "hund")
				check(d, "sitta", "")
				check(free, "sitta", "sitt")
			}
		})
	}
	wg.Wait()
	if !slices.Equal(d.words, stops) || !d.Configured("swedish", "swedish") {
		t.Fatal("evaluation mutated provider/resource")
	}
}
