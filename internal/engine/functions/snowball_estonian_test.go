package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSnowballEstonianProvider(t *testing.T) {
	ctx := t.Context()
	a, err := CompileSnowballDictionary(ctx, "estonian", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileSnowballDictionary(ctx, "EsToNiAn", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.provider != b.provider || !a.Configured("estonian", "") || !b.Configured("EsToNiAn", "") || b.Configured("estonian", "") || len(a.words) != 0 || a.Filtering() {
		t.Fatal("Estonian provider/configured spelling/resource authority")
	}
	stock, err := DefaultEstonianSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DefaultEstonianSnowballDictionary()
	if err != nil || stock != again || stock.provider != a.provider || !stock.Configured("estonian", "") || len(stock.words) != 0 {
		t.Fatal("shared language-only bootstrap runtime", stock, again, err)
	}
	stopped, err := CompileSnowballDictionary(ctx, "estonian", "english")
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
	check(a, "ja", "ja", false)
	check(a, "on", "on", false)
	check(a, "THE", "the", false)
	check(stopped, "THE", "", true)
	check(stopped, "ONLY", "", true)
	check(stopped, "RAAMATUD", "raama", false)
	check(stopped, "RATS", "rats", false)
	check(english, "RATS", "rat", false)
	for _, tc := range [][2]string{
		{"RAAMATUD", "raama"}, {"LÖÖN", "lÖÖn"}, {"viin", "viima"}, {"CAFÉS", "cafÉs"},
		{"raamatud!", "raamatud!"}, {"raamatud\u0301", "raamatud\u0301"},
		{"ab" + strings.Repeat("a", 996) + "ma", "ab" + strings.Repeat("a", 996)},
		{"AB" + strings.Repeat("A", 997) + "ma", "ab" + strings.Repeat("a", 997) + "ma"},
		{"ab" + strings.Repeat("õ", 498) + "ma", "ab" + strings.Repeat("õ", 498)},
		{"AB" + strings.Repeat("õ", 498) + "ama", "ab" + strings.Repeat("õ", 498) + "ama"},
	} {
		check(a, tc[0], tc[1], false)
	}
	var resources []*SnowballDictionary
	for _, name := range []string{"english", "danish", "dutch"} {
		first, err := CompileSnowballDictionary(ctx, "estonian", name)
		if err != nil {
			t.Fatal(err)
		}
		second, err := CompileSnowballDictionary(ctx, "EsToNiAn", name)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.words) == 0 || &first.words[0] != &second.words[0] || first.provider != a.provider {
			t.Fatal("exact resource backing", name)
		}
		for _, previous := range resources {
			if &first.words[0] == &previous.words[0] {
				t.Fatal("different resource backing aliased", name)
			}
		}
		resources = append(resources, first)
	}
	for _, language := range []string{" estonian ", "ESTONIAN ", ""} {
		if d, err := CompileSnowballDictionary(ctx, language, ""); d != nil || err == nil {
			t.Fatal(language, d, err)
		}
	}
	if d, err := CompileSnowballDictionary(ctx, "estonian", "estonian"); d != nil || err == nil {
		t.Fatal("invented Estonian stoplist", d, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "estonian", ""); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := a.Lexize(canceled, "raamatud"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Alternate phase lengths through the same immutable provider; one invocation
	// must not leak region offsets or mutable scratch into another.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(a, "raamatud", "raama", false)
				check(b, "viin", "viima", false)
				check(a, "abmõttele", "abmõte", false)
				check(stopped, "ONLY", "", true)
			}
		})
	}
	wg.Wait()
}
