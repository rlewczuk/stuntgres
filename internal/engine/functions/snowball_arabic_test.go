package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSnowballArabicDictionary(t *testing.T) {
	ctx := t.Context()
	a, err := CompileSnowballDictionary(ctx, "arabic", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileSnowballDictionary(ctx, "ArAbIc", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.provider != b.provider || !a.Configured("arabic", "") || !b.Configured("ArAbIc", "") || b.Configured("arabic", "") || len(a.words) != 0 || a.Filtering() {
		t.Fatal("Arabic provider/configured spelling/resource authority")
	}
	stopped, err := CompileSnowballDictionary(ctx, "arabic", "english")
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
	check(stopped, "الكتاب", "كتاب", false)
	for _, tc := range [][2]string{{"RATS", "rats"}, {"الكتاب", "كتاب"}, {"\u064e", "\u064e"}, {"\u0640", "\u0640"}, {"CAFÉS", "cafÉs"}, {strings.Repeat("ب", 498) + "ون", strings.Repeat("ب", 497)}, {strings.Repeat("ب", 499) + "ون", strings.Repeat("ب", 499) + "ون"}, {"A" + strings.Repeat("ب", 498) + "ون", "a" + strings.Repeat("ب", 498) + "ون"}} {
		check(a, tc[0], tc[1], false)
	}
	for _, language := range []string{" arabic ", "ARABIC ", ""} {
		if d, err := CompileSnowballDictionary(ctx, language, ""); d != nil || err == nil {
			t.Fatal(language, d, err)
		}
	}
	if d, err := CompileSnowballDictionary(ctx, "arabic", "arabic"); d != nil || err == nil {
		t.Fatal("invented Arabic stoplist", d, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "arabic", ""); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := a.Lexize(canceled, "الكتاب"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Alternate noun/verb/normalization work through the same immutable provider;
	// one invocation must not leak defined/noun/verb flags or mutable scratch.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(a, "الكتاب", "كتاب", false)
				check(b, "يستعمل", "استعمل", false)
				check(a, "كِتَابٌ", "كتاب", false)
				check(stopped, "ONLY", "", true)
			}
		})
	}
	wg.Wait()
}
