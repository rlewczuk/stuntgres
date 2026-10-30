package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSnowballCatalanDictionary(t *testing.T) {
	ctx := t.Context()
	a, err := CompileSnowballDictionary(ctx, "catalan", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileSnowballDictionary(ctx, "CaTaLaN", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.provider != b.provider || !a.Configured("catalan", "") || !b.Configured("CaTaLaN", "") || b.Configured("catalan", "") || len(a.words) != 0 || a.Filtering() {
		t.Fatal("provider/configured spelling/resource authority")
	}
	stock, err := DefaultCatalanSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DefaultCatalanSnowballDictionary()
	if err != nil || stock != again || stock.provider != a.provider || !stock.Configured("catalan", "") || len(stock.words) != 0 {
		t.Fatal("shared language-only bootstrap runtime", stock, again, err)
	}
	stopped, err := CompileSnowballDictionary(ctx, "catalan", "english")
	if err != nil {
		t.Fatal(err)
	}
	english, err := CompileSnowballDictionary(ctx, "english", "english")
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped.words) != 127 || &stopped.words[0] != &english.words[0] || stopped.provider == english.provider {
		t.Fatal("shared stoplist substituted provider")
	}
	check := func(d *SnowballDictionary, input, want string) {
		t.Helper()
		got, err := d.Lexize(ctx, input)
		if err != nil || !got.Recognized {
			t.Fatal(input, got, err)
		}
		if want == "" {
			if len(got.Lexemes) != 0 {
				t.Fatal(input, got)
			}
		} else if len(got.Lexemes) != 1 || got.Lexemes[0].Text != want || got.Lexemes[0].Prefix || got.Lexemes[0].Variant != 0 {
			t.Fatal(input, got, want)
		}
	}
	for _, tc := range catalanUnitGoldens {
		check(a, tc[0], tc[1])
	}
	for _, tc := range [][2]string{
		{"", ""}, {"GATS", "gat"}, {"THE", "the"}, {"ONLY", "only"}, {"IS", "is"},
		{"CAFÉ", "cafÉ"}, {"CAFÉS", "cafÉ"},
		{"bab" + strings.Repeat("é", 498) + "x", "bab" + strings.Repeat("e", 498) + "x"},
		{"BAB" + strings.Repeat("é", 498) + "xx", "bab" + strings.Repeat("é", 498) + "xx"},
	} {
		check(a, tc[0], tc[1])
	}
	for _, word := range []string{"THE", "ONLY", "IS"} {
		check(stopped, word, "")
	}
	check(stopped, "GATS", "gat")
	for _, language := range []string{" catalan ", "CATALAN ", ""} {
		if d, err := CompileSnowballDictionary(ctx, language, ""); d != nil || err == nil {
			t.Fatal(language, d, err)
		}
	}
	if d, err := CompileSnowballDictionary(ctx, "catalan", "catalan"); d != nil || err == nil {
		t.Fatal("invented stoplist", d, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "catalan", ""); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := a.Lexize(canceled, "gats"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(a, "GATS", "gat")
				check(b, "bababquíssima", "bababc")
				check(a, "col·laboracions", "col.labor")
				check(stopped, "ONLY", "")
			}
		})
	}
	wg.Wait()
}
