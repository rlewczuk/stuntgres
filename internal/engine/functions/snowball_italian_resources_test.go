package functions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestSnowballItalianDictionaryResources(t *testing.T) {
	ctx := t.Context()
	t.Run("canceled-uncached-load", func(t *testing.T) {
		dictionaryStopwords.Lock()
		saved, existed := dictionaryStopwords.words["italian"]
		delete(dictionaryStopwords.words, "italian")
		dictionaryStopwords.Unlock()
		defer func() {
			dictionaryStopwords.Lock()
			defer dictionaryStopwords.Unlock()
			if existed {
				dictionaryStopwords.words["italian"] = saved
			} else {
				delete(dictionaryStopwords.words, "italian")
			}
		}()
		loading, cancel := context.WithCancel(ctx)
		defer cancel()
		scan := &germanLoadCancelContext{Context: loading, cancel: cancel}
		d, err := CompileSnowballDictionary(scan, "italian", "italian")
		if d != nil || !errors.Is(err, context.Canceled) || !errors.Is(loading.Err(), context.Canceled) {
			t.Fatal("canceled scan published runtime", d, err)
		}
		dictionaryStopwords.Lock()
		_, published := dictionaryStopwords.words["italian"]
		dictionaryStopwords.Unlock()
		if published {
			t.Fatal("canceled scan published backing")
		}
	})
	stock, err := CompileSnowballDictionary(ctx, "italian", "italian")
	if err != nil {
		t.Fatal(err)
	}
	again, err := CompileSnowballDictionary(ctx, "italian", "italian")
	if err != nil || stock.provider != again.provider || !stock.Configured("italian", "italian") || stock.Filtering() {
		t.Fatal("shared stock runtime authority", again, err)
	}
	defaultFirst, err := DefaultItalianSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	defaultAgain, err := DefaultItalianSnowballDictionary()
	if err != nil || defaultFirst != defaultAgain || defaultFirst.provider != stock.provider || &defaultFirst.words[0] != &stock.words[0] {
		t.Fatal("default singleton/provider/resource backing", defaultFirst, defaultAgain, err)
	}
	// Independently transcribed REL_18_6 italian.stop, all 279 entries.
	stops := strings.Fields("ad al allo ai agli all agl alla alle con col coi da dal dallo dai dagli dall dagl dalla dalle di del dello dei degli dell degl della delle in nel nello nei negli nell negl nella nelle su sul sullo sui sugli sull sugl sulla sulle per tra contro io tu lui lei noi voi loro mio mia miei mie tuo tua tuoi tue suo sua suoi sue nostro nostra nostri nostre vostro vostra vostri vostre mi ti ci vi lo la li le gli ne il un uno una ma ed se perché anche come dov dove che chi cui non più quale quanto quanti quanta quante quello quelli quella quelle questo questi questa queste si tutto tutti a c e i l o ho hai ha abbiamo avete hanno abbia abbiate abbiano avrò avrai avrà avremo avrete avranno avrei avresti avrebbe avremmo avreste avrebbero avevo avevi aveva avevamo avevate avevano ebbi avesti ebbe avemmo aveste ebbero avessi avesse avessimo avessero avendo avuto avuta avuti avute sono sei è siamo siete sia siate siano sarò sarai sarà saremo sarete saranno sarei saresti sarebbe saremmo sareste sarebbero ero eri era eravamo eravate erano fui fosti fu fummo foste furono fossi fosse fossimo fossero essendo faccio fai facciamo fanno faccia facciate facciano farò farai farà faremo farete faranno farei faresti farebbe faremmo fareste farebbero facevo facevi faceva facevamo facevate facevano feci facesti fece facemmo faceste fecero facessi facesse facessimo facessero facendo sto stai sta stiamo stanno stia stiate stiano starò starai starà staremo starete staranno starei staresti starebbe staremmo stareste starebbero stavo stavi stava stavamo stavate stavano stetti stesti stette stemmo steste stettero stessi stesse stessimo stessero stando")
	sorted := slices.Clone(stops)
	slices.Sort(sorted)
	if len(stops) != 279 || !slices.Equal(stock.words, sorted) {
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
	for _, tc := range [][2]string{{"perché", ""}, {"PERCHé", ""}, {"PERCHÉ", "perchÉ"}, {"cani", "can"}, {"CANI", "can"}, {"più", ""}, {"piú", "più"}, {"CAFfÈ", "caffÈ"}, {"caffè", "caff"}, {"", ""},
		{strings.Repeat("A", 996) + "cani", strings.Repeat("a", 996) + "can"},
		{strings.Repeat("A", 997) + "cani", strings.Repeat("a", 997) + "cani"},
		{strings.Repeat("à", 498) + "cani", strings.Repeat("à", 498) + "can"},
		{strings.Repeat("à", 499) + "cani", strings.Repeat("à", 499) + "cani"},
		{strings.Repeat("A", 997) + "abacá", strings.Repeat("a", 997) + "abacá"},
	} {
		check(stock, tc[0], tc[1])
	}
	for range 3 {
		d, err := CompileSnowballDictionary(ctx, "italian", "italian")
		simple, se := CompileSimpleDictionary(ctx, true, "italian")
		cross, ce := CompileSnowballDictionary(ctx, "english", "italian")
		if err != nil || se != nil || ce != nil {
			t.Fatal(err, se, ce)
		}
		if &d.words[0] != &stock.words[0] || &simple.words[0] != &stock.words[0] || &cross.words[0] != &stock.words[0] {
			t.Fatal("same resource does not share immutable backing")
		}
		check(cross, "perché", "")
	}
	for _, name := range []string{"english", "danish", "dutch", "german"} {
		other, err := CompileSnowballDictionary(ctx, "italian", name)
		if err != nil || &other.words[0] == &stock.words[0] {
			t.Fatal("distinct resource backing", name, err)
		}
	}
	free, err := CompileSnowballDictionary(ctx, "italian", "")
	if err != nil || free.words != nil {
		t.Fatal(free, err)
	}
	check(free, "perché", "perc")
	mixed, err := CompileSnowballDictionary(ctx, "ItAlIaN", "italian")
	if err != nil || mixed.provider != stock.provider || !mixed.Configured("ItAlIaN", "italian") || mixed.Configured("italian", "italian") {
		t.Fatal("configured spelling/provider authority", err)
	}
	forged := *stock
	counterfeit := *stock.provider
	forged.provider = &counterfeit
	if forged.Configured("italian", "italian") {
		t.Fatal("copied public language spelling forged registered provider authority")
	}
	for _, language := range []string{"", " italian ", "Italian ", "italian_missing"} {
		if d, err := CompileSnowballDictionary(ctx, language, "italian"); d != nil || err == nil {
			t.Fatal("unregistered language accepted", language, d, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "italian", "italian"); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := stock.Lexize(canceled, "cani"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, name := range []string{"italian_missing", "Italian", "../italian"} {
		if d, err := CompileSnowballDictionary(ctx, "italian", name); d != nil || err == nil {
			t.Fatal(name, d, err)
		}
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["italian_missing"]
	_, empty := dictionaryStopwords.words[""]
	backing := dictionaryStopwords.words["italian"]
	dictionaryStopwords.Unlock()
	if missing || empty || len(backing) != 279 || &backing[0] != &stock.words[0] {
		t.Fatal("failed/empty load poisoned backing")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(stock, "cani", "can")
				check(stock, "perché", "")
				check(free, "perché", "perc")
			}
		})
	}
	wg.Wait()
}
