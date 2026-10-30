package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSnowballHindiDictionary(t *testing.T) {
	ctx := t.Context()
	a, err := CompileSnowballDictionary(ctx, "hindi", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileSnowballDictionary(ctx, "HiNdI", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.provider != b.provider || !a.Configured("hindi", "") || !b.Configured("HiNdI", "") || b.Configured("hindi", "") || len(a.words) != 0 || a.Filtering() {
		t.Fatal("Hindi provider/configured spelling/resource authority")
	}
	stock, err := DefaultHindiSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DefaultHindiSnowballDictionary()
	if err != nil || stock != again || stock.provider != a.provider || !stock.Configured("hindi", "") || len(stock.words) != 0 {
		t.Fatal("shared language-only bootstrap runtime", stock, again, err)
	}
	stopped, err := CompileSnowballDictionary(ctx, "hindi", "english")
	if err != nil {
		t.Fatal(err)
	}
	english, err := CompileSnowballDictionary(ctx, "english", "english")
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped.words) != 127 || &stopped.words[0] != &english.words[0] || stopped.provider == english.provider {
		t.Fatal("explicit shared stoplist must not substitute English provider")
	}
	check := func(d *SnowballDictionary, input, want string, empty bool) {
		t.Helper()
		got, err := d.Lexize(ctx, input)
		if err != nil || !got.Recognized {
			t.Fatal(input, got, err)
		}
		if empty {
			if len(got.Lexemes) != 0 {
				t.Fatal(input, got)
			}
			return
		}
		if len(got.Lexemes) != 1 || got.Lexemes[0].Text != want || got.Lexemes[0].Prefix || got.Lexemes[0].Variant != 0 {
			t.Fatal(input, got, want)
		}
	}
	check(a, "", "", true)
	check(a, "ONLY", "only", false)
	check(stopped, "ONLY", "", true)
	check(stopped, "करती", "कर", false)
	check(stopped, "RATS", "rats", false)
	check(english, "RATS", "rat", false)
	for _, tc := range [][2]string{
		{"RATS", "rats"}, {"करती", "कर"}, {"करूँगा", "करूँग"}, {"में", "म"}, {"CAFÉS", "cafÉs"},
		{strings.Repeat("A", 994) + "की", strings.Repeat("a", 994) + "क"},
		{strings.Repeat("A", 995) + "की", strings.Repeat("a", 995) + "की"},
	} {
		check(a, tc[0], tc[1], false)
	}
	for _, language := range []string{" hindi ", "ARMENIAN ", ""} {
		if d, err := CompileSnowballDictionary(ctx, language, ""); d != nil || err == nil {
			t.Fatal(language, d, err)
		}
	}
	if d, err := CompileSnowballDictionary(ctx, "hindi", "hindi"); d != nil || err == nil {
		t.Fatal("invented Hindi stoplist", d, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "hindi", ""); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := a.Lexize(canceled, "करती"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Alternate phase lengths through the same immutable provider; one invocation
	// must not leak region offsets or mutable scratch into another.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(a, "करती", "कर", false)
				check(b, "कअआ", "कअ", false)
				check(a, "कऋती", "कऋत", false)
				check(stopped, "ONLY", "", true)
			}
		})
	}
	wg.Wait()
}
