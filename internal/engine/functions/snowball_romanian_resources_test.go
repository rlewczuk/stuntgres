package functions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestSnowballRomanianResources(t *testing.T) {
	ctx := t.Context()
	stock, err := DefaultRomanianSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DefaultRomanianSnowballDictionary()
	if err != nil || stock != again || !stock.Configured("romanian", "") || stock.Filtering() || stock.words != nil {
		t.Fatal("shared resource-free Romanian authority", again, err)
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
	for _, pair := range [][2]string{
		{"OCOLUL", "ocol"}, {"RUNNING", "running"}, {"ONLY", "only"}, {"THE", "the"}, {"și", "și"},
		{"abruptă", "abrupt"}, {"ABRUPTĂ", "abruptĂ"}, {"", ""}, {"abruptă!", "abruptă!"},
		{strings.Repeat("ş", 500), strings.Repeat("ș", 500)},
		{"A" + strings.Repeat("ş", 500), "a" + strings.Repeat("ş", 500)},
	} {
		check(stock, pair[0], pair[1])
	}
	first, err := stock.Lexize(ctx, "OCOLUL")
	if err != nil {
		t.Fatal(err)
	}
	first.Lexemes[0].Text = "changed"
	check(stock, "OCOLUL", "ocol")
	configured, err := CompileSnowballDictionary(ctx, "RoMaNiAn", "english")
	if err != nil || configured.provider != stock.provider || !configured.Configured("RoMaNiAn", "english") || configured.Configured("romanian", "english") {
		t.Fatal("configured spelling retains immutable provider", configured, err)
	}
	// Independently transcribed released English127, not an Romanian stock resource.
	stops := strings.Fields("i me my myself we our ours ourselves you your yours yourself yourselves he him his himself she her hers herself it its itself they them their theirs themselves what which who whom this that these those am is are was were be been being have has had having do does did doing a an the and but if or because as until while of at by for with about against between into through during before after above below to from up down in out on off over under again further then once here there when where why how all any both each few more most other some such no nor not only own same so than too very s t can will just don should now")
	sorted := slices.Clone(stops)
	slices.Sort(sorted)
	if len(stops) != 127 || !slices.Equal(configured.words, sorted) {
		t.Fatal("independent configured English127", configured.words)
	}
	for _, word := range stops {
		check(configured, word, "")
		check(configured, strings.ToUpper(word), "")
	}
	check(configured, "abruptă", "abrupt")
	check(configured, "OCOLUL", "ocol")
	for range 3 {
		d, err := CompileSnowballDictionary(ctx, "romanian", "english")
		simple, se := CompileSimpleDictionary(ctx, true, "english")
		english, ee := DefaultSnowballDictionary()
		if err != nil || se != nil || ee != nil {
			t.Fatal(err, se, ee)
		}
		if &d.words[0] != &configured.words[0] || &simple.words[0] != &configured.words[0] || &english.words[0] != &configured.words[0] {
			t.Fatal("configured resource backing not shared")
		}
	}
	for _, name := range []string{"danish", "dutch", "german", "hungarian"} {
		d, err := CompileSnowballDictionary(ctx, "romanian", name)
		if err != nil || &d.words[0] == &configured.words[0] {
			t.Fatal("distinct resource backing", name, err)
		}
	}
	for _, name := range []string{"romanian", "ROMANIAN", "../romanian", "café"} {
		if d, err := CompileSnowballDictionary(ctx, "romanian", name); d != nil || err == nil {
			t.Fatal("unavailable resource accepted", name, d, err)
		}
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["romanian"]
	_, empty := dictionaryStopwords.words[""]
	dictionaryStopwords.Unlock()
	if missing || empty {
		t.Fatal("missing or empty resource published backing")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "romanian", "english"); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := stock.Lexize(canceled, "abruptă"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(stock, "abruptă", "abrupt")
				check(stock, "THE", "the")
				check(configured, "THE", "")
				check(configured, "OCOLUL", "ocol")
			}
		})
	}
	wg.Wait()
}
