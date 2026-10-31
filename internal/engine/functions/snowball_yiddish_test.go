package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSnowballYiddishRegions(t *testing.T) {
	// Literal released-C stage states: offsets are bytes in the mutated buffer.
	for _, tc := range []struct { input, buffer string; cursor, p1 int; ok bool }{
		{"זאבאבנ", "זאבאבנ", 4, 6, true}, {"בר", "בר", 0, 4, false},
		{"ברכנ", "ברכנ", 6, 6, false}, {"שפרנ", "שפרנ", 8, 8, false},
		{"שטרנ", "שטרנ", 8, 8, false}, {"שטשנ", "שטשנ", 8, 8, false}, {"דזששנ", "דזששנ", 10, 10, false},
		// Released a_2[0] is Dalet/Zayen/Shin (six UTF8 bytes), not
		// Dalet/Zayen/Shin/Shin. Spell these witnesses explicitly.
		{"\u05d3\u05d6\u05e9נ", "\u05d3\u05d6\u05e9נ", 8, 8, false},
		{"\u05d3\u05d6\u05e9ער", "\u05d3\u05d6\u05e9ער", 8, 8, true},
		{"גע", "גע", 0, 4, false}, {"געא", "GEא", 2, 6, false},
		{"געמיטנ", "GEמיטנ", 6, 8, true},
		{"אױסצוזאבאבנ", "אױסTSUזאבאבנ", 13, 15, true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			w := yiddishWord{text: tc.input}
			if ok := w.markRegions(); ok != tc.ok || w.result() != tc.buffer || w.c != tc.cursor || w.p1 != tc.p1 { t.Fatalf("mark: %q c=%d p1=%d ok=%t", w.result(), w.c, w.p1, ok) }
		})
	}
	for _, p1 := range []int{6, 7} {
		w := yiddishWord{text: "גיסט", c: 0, p1: p1}
		if w.r1plus3() != (p1 == 6) { t.Fatal("released byte boundary", p1) }
		w.c = 8
		w.primary()
		want := "גיסט"
		if p1 == 6 { want = "גיס" }
		if w.result() != want || w.c != len(want) || w.p1 != p1 { t.Fatal("IST guarded rewrite", w.result(), w.c, w.p1) }
	}
	// Three codepoints here occupy only five bytes: a rune-relative hop would
	// incorrectly reject p1=6, while the released cursor+6 condition accepts.
	w := yiddishWord{text: "aאב", p1: 6}
	if !w.r1plus3() { t.Fatal("R1plus3 must not count runes") }
}

func TestSnowballYiddishRules(t *testing.T) {
	t.Run("nested-and-later-stages", yiddishRuleStages)
	// All primary candidates independently transcribed from the pinned .sbl.
	// The fixed root puts each suffix in R1; these are primary-only outputs,
	// deliberately before secondary/tertiary and marker cleanup.
	for _, tc := range [][2]string{
		{"ערס", ""}, {"ענ", ""}, {"נס", ""}, {"ענערס", ""}, {"עס", ""}, {"ע", ""}, {"נ", ""}, {"ס", ""}, {"עמ", ""}, {"ער", ""},
		{"יע", "יע"}, {"יעס", "יע"}, {"ענע", ""}, {"ענעמ", ""}, {"ענער", ""}, {"ענס", ""},
		{"ט", ""}, {"טנ", ""}, {"טע", ""}, {"טער", ""}, {"טס", ""}, {"טנס", ""}, {"טענע", ""}, {"טענעמ", ""}, {"טענער", ""},
		{"עט", ""}, {"עטנ", ""}, {"עטס", ""}, {"עטע", ""}, {"עטער", ""},
		{"גײנ", "גײ"}, {"גאנגענ", "גײ"}, {"נומענ", "נעמ"}, {"שריבנ", "שרײב"},
		{"GEמיטנ", "מײד"}, {"GEביטנ", "בײט"}, {"GEביסנ", "בײס"}, {"װיזנ", "װײז"}, {"טריבנ", "טרײב"}, {"GEליטנ", "לײט"},
		{"קליבנ", "קלײב"}, {"ריבנ", "רײב"}, {"GEריסנ", "רײס"}, {"שװיגנ", "שװײג"}, {"שמיסנ", "שמײס"}, {"שניטנ", "שנײד"},
		{"בונדנ", "בינד"}, {"װוטשנ", "װיטש"}, {"זונגנ", "זינג"}, {"טרונקנ", "טרינק"}, {"צװונגנ", "צװינג"}, {"שלונגנ", "שלינג"},
		{"בױגנ", "בײג"}, {"הױבנ", "הײב"}, {"פארלױרנ", "פארליר"}, {"שטאנענ", "שטײ"}, {"שװױרנ", "שװער"}, {"בראכט", "ברענג"},
		{"ונג", ""}, {"הײט", ""}, {"קײט", ""}, {"יקײט", ""}, {"שאפט", ""}, {"יזמ", ""}, {"יזמענ", ""}, {"ימ", ""}, {"ות", "ה"},
		{"עלעכ", ""}, {"עלע", ""}, {"לעכ", ""}, {"עלעס", ""}, {"עלענ", ""}, {"יסט", ""}, {"יסטנ", ""}, {"סטו", ""}, {"סטער", ""}, {"סטע", ""}, {"סטנ", ""}, {"סט", ""},
	} {
		t.Run("primary-"+tc[0], func(t *testing.T) {
			w := yiddishWord{text: "זאבאב"+tc[0], p1: 6}
			w.c = w.length()
			w.primary()
			want := "זאבאב"+tc[1]
			if w.result() != want || w.p1 != 6 || w.c != len(want) { t.Fatal(w.result(), w.c, w.p1, want) }
		})
	}
	for _, tc := range []struct { input string; p1 int; primary, secondary, tertiary string }{
		{"זאבאבישונגס", 6, "זאבאבישונג", "זאבאביש", "זאבאב"},
		{"זאבאבונגיק", 6, "זאבאבונגיק", "זאבאבונגיק", "זאבאבונג"},
		{"זאבאבבליק", 6, "זאבאבבליק", "זאבאבבליק", "זאבאבבליק"},
		{"אטנ", 6, "אט", "אט", "אט"}, {"גיסטנ", 6, "גיסטנ", "גיסטנ", "גיסטנ"},
		{"GEבראכטענע", 8, "GEברענג", "GEברענג", "GEברענג"},
		{"GEמיטענער", 8, "GEמיט", "GEמיט", "GEמיט"},
	} {
		w := yiddishWord{text: tc.input, c: len(tc.input), p1: tc.p1}
		w.primary()
		if w.result() != tc.primary || w.c != len(tc.primary) || w.p1 != tc.p1 { t.Fatal("primary", tc, w) }
		w.secondary()
		if w.result() != tc.secondary || w.c != len(tc.secondary) || w.p1 != tc.p1 { t.Fatal("secondary", tc, w) }
		w.tertiary()
		if w.result() != tc.tertiary || w.c != len(tc.tertiary) || w.p1 != tc.p1 { t.Fatal("tertiary", tc, w) }
	}
	w := yiddishWord{text: "אGEבTSUג", c: 11, p1: 11}
	w.cleanup()
	if w.result() != "אבג" || w.c != 0 || w.p1 != 11 { t.Fatal("backward marker cleanup", w) }
}

func TestSnowballYiddishResources(t *testing.T) {
	ctx := t.Context()
	a, err := CompileSnowballDictionary(ctx, "yiddish", "")
	if err != nil { t.Fatal(err) }
	b, err := CompileSnowballDictionary(ctx, "YiDdIsH", "")
	if err != nil { t.Fatal(err) }
	if a.provider != b.provider || !a.Configured("yiddish", "") || !b.Configured("YiDdIsH", "") || b.Configured("yiddish", "") || len(a.words) != 0 || a.Filtering() { t.Fatal("provider/spelling/no-list authority") }
	stock, err := DefaultYiddishSnowballDictionary()
	if err != nil { t.Fatal(err) }
	again, err := DefaultYiddishSnowballDictionary()
	if err != nil || stock != again || stock.provider != a.provider || !stock.Configured("yiddish", "") || len(stock.words) != 0 { t.Fatal(stock, again, err) }
	stopped, err := CompileSnowballDictionary(ctx, "yiddish", "english")
	if err != nil { t.Fatal(err) }
	english, err := CompileSnowballDictionary(ctx, "english", "english")
	if err != nil { t.Fatal(err) }
	simple, err := CompileSimpleDictionary(ctx, true, "english")
	if err != nil { t.Fatal(err) }
	if len(stopped.words) != 127 || &stopped.words[0] != &english.words[0] || &stopped.words[0] != &simple.words[0] || stopped.provider == english.provider { t.Fatal("shared immutable list, independent kernel") }
	check := func(d *SnowballDictionary, input, want string, stop bool) {
		t.Helper()
		got, err := d.Lexize(ctx, input)
		if err != nil || !got.Recognized { t.Fatal(input, got, err) }
		if stop { if len(got.Lexemes) != 0 { t.Fatal(input, got) }; return }
		if len(got.Lexemes) != 1 || got.Lexemes[0].Text != want || got.Lexemes[0].Prefix || got.Lexemes[0].Variant != 0 { t.Fatal(input, got, want) }
	}
	check(a, "ONLY", "only", false); check(stopped, "ONLY", "", true)
	check(a, "", "", true); check(a, "\u05b0", "\u05b0", false)
	check(a, strings.Repeat("A", 998)+"ך", strings.Repeat("a", 998)+"כ", false)
	check(a, strings.Repeat("A", 999)+"ך", strings.Repeat("a", 999)+"ך", false)
	for _, language := range []string{" yiddish ", "YIDDISH ", ""} { if d, err := CompileSnowballDictionary(ctx, language, ""); d != nil || err == nil { t.Fatal(language, d, err) } }
	if d, err := CompileSnowballDictionary(ctx, "yiddish", "yiddish"); d != nil || err == nil { t.Fatal("invented resource", d, err) }
	canceled, cancel := context.WithCancel(ctx); cancel()
	if d, err := CompileSnowballDictionary(canceled, "yiddish", ""); d != nil || !errors.Is(err, context.Canceled) { t.Fatal(d, err) }
	if _, err := a.Lexize(canceled, "קינדער"); !errors.Is(err, context.Canceled) { t.Fatal(err) }
	var wg sync.WaitGroup
	for range 8 { wg.Go(func() { for range 32 {
		check(a, "קינדער", "קינד", false); check(b, "געבראַכט", "ברענג", false)
		check(a, "אַדורכגעביסן", "אדורכבײס", false); check(a, "בליק", "בליק", false)
		check(stopped, "ONLY", "", true); check(stopped, "גע", "גע", false)
	} }) }
	wg.Wait()
}
