package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSnowballArmenianDictionary(t *testing.T) {
	ctx := t.Context()
	a, err := CompileSnowballDictionary(ctx, "armenian", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileSnowballDictionary(ctx, "ArMeNiAn", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.provider != b.provider || !a.Configured("armenian", "") || !b.Configured("ArMeNiAn", "") || b.Configured("armenian", "") || len(a.words) != 0 || a.Filtering() {
		t.Fatal("Armenian provider/configured spelling/resource authority")
	}
	stock, err := DefaultArmenianSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DefaultArmenianSnowballDictionary()
	if err != nil || stock != again || stock.provider != a.provider || !stock.Configured("armenian", "") || len(stock.words) != 0 {
		t.Fatal("shared language-only bootstrap runtime", stock, again, err)
	}
	stopped, err := CompileSnowballDictionary(ctx, "armenian", "english")
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
	check(stopped, "աղոթքը", "աղոթ", false)
	check(stopped, "RATS", "rats", false)
	check(english, "RATS", "rat", false)
	check(a, "A"+strings.Repeat("բ", 496)+"աիչ", "a"+strings.Repeat("բ", 496)+"ա", false)
	for _, tc := range [][2]string{{"RATS", "rats"}, {"աղոթքը", "աղոթ"}, {"ԱՂՈԹՔԸ", "ԱՂՈԹՔԸ"}, {"աղոթք\u0301", "աղոթք\u0301"}, {"CAFÉS", "cafÉs"}, {strings.Repeat("բ", 497) + "աիչ", strings.Repeat("բ", 497) + "ա"}, {strings.Repeat("բ", 498) + "աիչ", strings.Repeat("բ", 498) + "աիչ"}, {"A" + strings.Repeat("բ", 497) + "աիչ", "a" + strings.Repeat("բ", 497) + "աիչ"}} {
		check(a, tc[0], tc[1], false)
	}
	for _, language := range []string{" armenian ", "ARMENIAN ", ""} {
		if d, err := CompileSnowballDictionary(ctx, language, ""); d != nil || err == nil {
			t.Fatal(language, d, err)
		}
	}
	if d, err := CompileSnowballDictionary(ctx, "armenian", "armenian"); d != nil || err == nil {
		t.Fatal("invented Armenian stoplist", d, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "armenian", ""); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(d, err)
	}
	if _, err := a.Lexize(canceled, "աղոթքը"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Alternate phase lengths through the same immutable provider; one invocation
	// must not leak region offsets or mutable scratch into another.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(a, "աղոթքը", "աղոթ", false)
				check(b, "բաբիչպեսել", "բաբ", false)
				check(a, "բաբաբացնել", "բաբաբա", false)
				check(stopped, "ONLY", "", true)
			}
		})
	}
	wg.Wait()
}
