package functions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestSnowballLithuanianResources(t *testing.T) {
	ctx := t.Context()
	stock, err := DefaultLithuanianSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DefaultLithuanianSnowballDictionary()
	if err != nil || stock != again || !stock.Configured("lithuanian", "") || stock.Filtering() || stock.words != nil {
		t.Fatal("shared resource-free Lithuanian authority", again, err)
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
		{"VYRAS", "vyr"}, {"RUNNING", "runn"}, {"ONLY", "onl"}, {"THE", "the"}, {"ir", "ir"},
		{"gervė", "gerv"}, {"GERVĖ", "gervĖ"}, {"", ""}, {"parking'as", "parking'"},
		{"babab" + strings.Repeat("a", 993) + "as", "babab" + strings.Repeat("a", 993)},
		{"BABAB" + strings.Repeat("A", 994) + "as", "babab" + strings.Repeat("a", 994) + "as"},
		{"babab" + strings.Repeat("ą", 496) + "ias", "babab" + strings.Repeat("ą", 496)},
		{"BABAB" + strings.Repeat("ą", 496) + "aias", "babab" + strings.Repeat("ą", 496) + "aias"},
	} {
		check(stock, pair[0], pair[1])
	}
	configured, err := CompileSnowballDictionary(ctx, "LiThUaNiAn", "english")
	if err != nil || configured.provider != stock.provider || !configured.Configured("LiThUaNiAn", "english") || configured.Configured("lithuanian", "english") {
		t.Fatal("configured spelling retains immutable provider", configured, err)
	}
	// Independently transcribed released English127, not an Lithuanian stock resource.
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
	check(configured, "gervė", "gerv")
	check(configured, "VYRAS", "vyr")
	for range 3 {
		d, err := CompileSnowballDictionary(ctx, "lithuanian", "english")
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
		d, err := CompileSnowballDictionary(ctx, "lithuanian", name)
		if err != nil || &d.words[0] == &configured.words[0] {
			t.Fatal("distinct resource backing", name, err)
		}
	}
	for _, name := range []string{"lithuanian", "LITHUANIAN", "../lithuanian", "café"} {
		if d, err := CompileSnowballDictionary(ctx, "lithuanian", name); d != nil || err == nil {
			t.Fatal("unavailable resource accepted", name, d, err)
		}
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["lithuanian"]
	_, empty := dictionaryStopwords.words[""]
	dictionaryStopwords.Unlock()
	if missing || empty {
		t.Fatal("missing or empty resource published backing")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "lithuanian", "english"); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := stock.Lexize(canceled, "gervė"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(stock, "gervė", "gerv")
				check(stock, "THE", "the")
				check(configured, "THE", "")
				check(configured, "VYRAS", "vyr")
			}
		})
	}
	wg.Wait()
}