package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSnowballBasqueDictionary(t *testing.T) {
	ctx := t.Context()
	a, err := CompileSnowballDictionary(ctx, "basque", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileSnowballDictionary(ctx, "BaSqUe", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.provider != b.provider || !a.Configured("basque", "") || !b.Configured("BaSqUe", "") || b.Configured("basque", "") || len(a.words) != 0 || a.Filtering() {
		t.Fatal("Basque provider/configured spelling/resource authority")
	}
	stock, err := DefaultBasqueSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DefaultBasqueSnowballDictionary()
	if err != nil || stock != again || stock.provider != a.provider || !stock.Configured("basque", "") || len(stock.words) != 0 {
		t.Fatal("shared language-only bootstrap runtime", stock, again, err)
	}
	stopped, err := CompileSnowballDictionary(ctx, "basque", "english")
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
	check(a, "eta", "eta", false)
	check(a, "da", "da", false)
	check(a, "THE", "the", false)
	check(stopped, "THE", "", true)
	check(stopped, "ONLY", "", true)
	check(stopped, "ETXEAK", "etxe", false)
	check(stopped, "RATS", "rats", false)
	check(english, "RATS", "rat", false)
	for _, tc := range [][2]string{{"ETXEAK", "etxe"}, {"etxeko", "etxeko"}, {"aurka", "aur"}, {"CAFÉS", "cafÉs"}, {"etxeak!", "etxeak!"}, {"etxeak\u0301", "etxeak\u0301"}, {"bab" + strings.Repeat("a", 995) + "ak", "bab" + strings.Repeat("a", 995)}, {"BAB" + strings.Repeat("A", 996) + "ak", "bab" + strings.Repeat("a", 996) + "ak"}, {"bab" + strings.Repeat("ñ", 497) + "aak", "bab" + strings.Repeat("ñ", 497) + "a"}, {"BAB" + strings.Repeat("ñ", 498) + "ak", "bab" + strings.Repeat("ñ", 498) + "ak"}} {
		check(a, tc[0], tc[1], false)
	}
	for _, tc := range [][2]string{{"a", "a"}, {"bb", "bb"}, {"bbb", "bbb"}, {"aaaa", "aaaa"}, {"café", "café"}, {"cafés", "cafés"}, {"cafe\u0301", "cafe\u0301"}, {"bababño", "babab"}, {"bababñi", "babab"}, {"abatu", "aba"}, {"aabatu", "aab"}, {"bbatu", "bba"}, {"babatu", "bab"}} {
		check(a, tc[0], tc[1], false)
	}
	for _, language := range []string{" basque ", "BASQUE ", ""} {
		if d, err := CompileSnowballDictionary(ctx, language, ""); d != nil || err == nil {
			t.Fatal(language, d, err)
		}
	}
	if d, err := CompileSnowballDictionary(ctx, "basque", "basque"); d != nil || err == nil {
		t.Fatal("invented Basque stoplist", d, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "basque", ""); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := a.Lexize(canceled, "etxeak"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Alternate phase lengths through the same immutable provider; one invocation
	// must not leak region offsets or mutable scratch into another.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(a, "etxeak", "etxe", false)
				check(b, "bababatuatseden", "bababatseden", false)
				check(a, "bababminutuko", "bababminutu", false)
				check(stopped, "ONLY", "", true)
			}
		})
	}
	wg.Wait()
}
