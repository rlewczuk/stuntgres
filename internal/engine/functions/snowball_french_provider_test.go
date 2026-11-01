package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine/fts"
)

func TestSnowballFrenchDictionaryResources(t *testing.T) {
	ctx := t.Context()
	french, err := CompileSnowballDictionary(ctx, "french", "french")
	if err != nil {
		t.Fatal(err)
	}
	simple, err := CompileSimpleDictionary(ctx, true, "french")
	if err != nil {
		t.Fatal(err)
	}
	cross, err := CompileSnowballDictionary(ctx, "english", "french")
	if err != nil {
		t.Fatal(err)
	}
	if len(french.words) != 155 || len(simple.words) != 155 || len(cross.words) != 155 || &french.words[0] != &simple.words[0] || &french.words[0] != &cross.words[0] || french.provider == cross.provider {
		t.Fatal("exact released French resource backing independent of provider")
	}
	var resources []*SnowballDictionary
	for _, resource := range []string{"english", "danish", "dutch", "french"} {
		a, err := CompileSnowballDictionary(ctx, "french", resource)
		if err != nil {
			t.Fatal(err)
		}
		b, err := CompileSnowballDictionary(ctx, "FrEnCh", resource)
		if err != nil {
			t.Fatal(err)
		}
		if len(a.words) == 0 || &a.words[0] != &b.words[0] || a.provider != french.provider {
			t.Fatal("same-resource immutable authority", resource)
		}
		for _, previous := range resources {
			if &previous.words[0] == &a.words[0] {
				t.Fatal("cross-resource cache contamination", resource)
			}
		}
		resources = append(resources, a)
	}
	for _, d := range []interface {
		Lexize(context.Context, string) (fts.LexicalResult, error)
	}{french, simple, cross} {
		got, err := d.Lexize(ctx, "été")
		if err != nil || !got.Recognized || len(got.Lexemes) != 0 {
			t.Fatal("shared French stopword", got, err)
		}
	}
	for _, tc := range []struct {
		runtime interface {
			Lexize(context.Context, string) (fts.LexicalResult, error)
		}
		want string
	}{{french, "cheval"}, {simple, "chevaux"}, {cross, "chevaux"}} {
		got, err := tc.runtime.Lexize(ctx, "CHEVAUX")
		if err != nil || !got.Recognized || len(got.Lexemes) != 1 || got.Lexemes[0].Text != tc.want {
			t.Fatal(got, tc.want, err)
		}
	}
	if d, err := CompileSnowballDictionary(ctx, "french", "french_missing"); d != nil || err == nil {
		t.Fatal("missing resource published", d, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "french", "french"); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := french.Lexize(canceled, "CHEVAUX"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSnowballFrenchLanguageProvider(t *testing.T) {
	ctx := t.Context()
	a, err := CompileSnowballDictionary(ctx, "french", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileSnowballDictionary(ctx, "FrEnCh", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.provider != b.provider || !a.Configured("french", "") || !b.Configured("FrEnCh", "") || b.Configured("french", "") || b.Configured("FrEnCh", "french") || a.Filtering() || len(a.words) != 0 {
		t.Fatal("immutable provider identity/configured spelling/resource")
	}
	stock, err := DefaultFrenchSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DefaultFrenchSnowballDictionary()
	if err != nil || again != stock || stock.provider != a.provider || !stock.Configured("french", "french") || len(stock.words) != 155 {
		t.Fatal("shared French bootstrap runtime", stock, again, err)
	}
	for _, language := range []string{" french ", "FRENCH ", "", "french_bogus"} {
		if d, err := CompileSnowballDictionary(ctx, language, ""); d != nil || err == nil {
			t.Fatal(language, d, err)
		}
	}
	check := func(d *SnowballDictionary, input, want string, empty bool) {
		t.Helper()
		got, err := d.Lexize(ctx, input)
		if err != nil || !got.Recognized {
			t.Errorf("%q: %v, %v", input, got, err)
			return
		}
		if empty {
			if len(got.Lexemes) != 0 {
				t.Errorf("%q: expected stop/empty, got %v", input, got)
			}
			return
		}
		if len(got.Lexemes) != 1 || got.Lexemes[0].Text != want || got.Lexemes[0].Prefix || got.Lexemes[0].Variant != 0 {
			t.Errorf("%q: got %v, want %q", input, got, want)
		}
	}
	check(a, "", "", true)
	check(a, "avions", "avion", false)
	check(stock, "avions", "", true)
	check(stock, "été", "", true)
	check(stock, "éTé", "", true)
	for _, tc := range [][2]string{
		{"CHEVAUX", "cheval"}, {"hôtels", "hôtel"}, {"CAFÉ", "cafÉ"}, {"CAFÉS", "cafÉ"}, {"ÉTÉ", "ÉtÉ"},
		{"l'été", "été"}, {"les", "le"}, {"où", "où"}, {"cafe\u0301", "cafe\u0301"}, {"chats!", "chats!"},
		{"bab" + strings.Repeat("a", 995) + "er", "bab" + strings.Repeat("a", 995)},
		{"BAB" + strings.Repeat("A", 996) + "er", "bab" + strings.Repeat("a", 996) + "er"},
		{"ba" + strings.Repeat("é", 498) + "er", "ba" + strings.Repeat("é", 498)},
		{"BAA" + strings.Repeat("é", 498) + "er", "baa" + strings.Repeat("é", 498) + "er"},
	} {
		check(stock, tc[0], tc[1], false)
	}
	// Different expansion/region/stage lengths alternate on the same immutable
	// provider. Race proof must never observe another invocation's scratch.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(a, "ambiguïtés", "ambigu", false)
				check(b, "bababamment", "babab", false)
				check(stock, "CHEVAUX", "cheval", false)
				check(stock, "été", "", true)
				check(a, "bijoux", "bijoux", false)
			}
		})
	}
	wg.Wait()
}
