package functions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSnowballPorterRules(t *testing.T) {
	// Independent historical snowball-data porter/{voc,output}.txt, revision
	// 4f982ec072a091771b321ab7861841471942e14a; s is deliberately kernel-empty.
	for _, tc := range [][2]string{
		{"added", "ad"}, {"agreed", "agre"}, {"as", "a"}, {"bled", "bled"},
		{"caresses", "caress"}, {"controll", "control"}, {"dying", "dy"}, {"enjoy", "enjoi"},
		{"failing", "fail"}, {"feed", "feed"}, {"generously", "gener"}, {"happy", "happi"},
		{"hopping", "hop"}, {"is", "i"}, {"lying", "ly"}, {"news", "new"}, {"only", "onli"},
		{"playing", "plai"}, {"ponies", "poni"}, {"rate", "rate"}, {"roll", "roll"},
		{"running", "run"}, {"s", ""}, {"sing", "sing"}, {"skies", "ski"}, {"sky", "sky"},
		{"ties", "ti"}, {"tying", "ty"}, {"us", "u"},
		// Prelude/postlude, original regions, no English exceptions or punctuation stripping.
		{"", ""}, {"y", "y"}, {"yoyo", "yoyo"}, {"boy", "boi"}, {"yy", "yy"},
		{"cats!", "cats!"}, {"'skies", "'ski"}, {"café", "café"}, {"cafés", "café"},
		{"cafe\u0301s", "cafe\u0301"}, {"generational", "gener"},
	} {
		if got := porterStem(tc[0]); got != tc[1] {
			t.Errorf("kernel %q = %q, want %q", tc[0], got, tc[1])
		}
	}
	phase := func(name, input, want string, p1, p2 int, apply func(*englishWord)) {
		t.Helper()
		w := englishWord{letters: []rune(input), p1: p1, p2: p2}
		apply(&w)
		if got := string(w.letters); got != want || w.p1 != p1 || w.p2 != p2 {
			t.Errorf("%s %q = %q regions %d/%d; want %q regions %d/%d", name, input, got, w.p1, w.p2, want, p1, p2)
		}
	}
	for _, tc := range [][2]string{{"caresses", "caress"}, {"ties", "ti"}, {"ss", "ss"}, {"s", ""}, {"us", "u"}, {"cat", "cat"}} {
		phase("1a", tc[0], tc[1], 0, 0, porterStep1a)
	}
	for _, tc := range []struct {
		input, want string
		p1          int
	}{
		{"agreed", "agree", 2}, {"feed", "feed", 4}, // rejected eed must not retry ed
		{"bled", "bled", 4}, {"sing", "sing", 3}, {"failing", "fail", 4},
		{"conflated", "conflate", 3}, {"troubled", "trouble", 5}, {"sized", "size", 3},
		{"hopping", "hop", 3}, {"added", "ad", 2}, {"aiding", "aid", 3},
		{"filing", "file", 3}, {"mating", "mate", 3}, {"abing", "ab", 2},
		{"snowing", "snow", 4}, {"boxing", "box", 3}, {"toYing", "toY", 3},
		{"filing", "fil", 4}, {"eedly", "eedly", 2}, {"hoppingly", "hoppingly", 3},
	} {
		phase("1b", tc.input, tc.want, tc.p1, 99, porterStep1b)
	}
	for _, double := range []string{"bb", "dd", "ff", "gg", "mm", "nn", "pp", "rr", "tt"} {
		phase("1b double", "a"+double+"ed", "a"+double[:1], 2, 99, porterStep1b)
	}
	for _, tc := range [][2]string{{"happy", "happi"}, {"enjoy", "enjoi"}, {"sky", "sky"}, {"by", "by"}, {"ay", "ai"}, {"boY", "boi"}, {"Y", "Y"}} {
		phase("1c", tc[0], tc[1], 0, 0, porterStep1c)
	}
	// Independently transcribed released phase actions; test at R1 and one before.
	for _, tc := range [][2]string{
		{"tional", "tion"}, {"enci", "ence"}, {"anci", "ance"}, {"abli", "able"},
		{"entli", "ent"}, {"eli", "e"}, {"izer", "ize"}, {"ization", "ize"},
		{"ational", "ate"}, {"ation", "ate"}, {"ator", "ate"}, {"alli", "al"},
		{"alism", "al"}, {"aliti", "al"}, {"fulness", "ful"}, {"ousli", "ous"},
		{"ousness", "ous"}, {"iveness", "ive"}, {"iviti", "ive"}, {"biliti", "ble"},
	} {
		phase("2 at R1", "bac"+tc[0], "bac"+tc[1], 3, 99, porterStep2)
		phase("2 before R1", "bac"+tc[0], "bac"+tc[0], 4, 99, porterStep2)
	}
	for _, tc := range [][2]string{{"alize", "al"}, {"icate", "ic"}, {"iciti", "ic"}, {"ical", "ic"}, {"ative", ""}, {"ful", ""}, {"ness", ""}} {
		phase("3 at R1", "bac"+tc[0], "bac"+tc[1], 3, 99, porterStep3)
		phase("3 before R1", "bac"+tc[0], "bac"+tc[0], 4, 99, porterStep3)
	}
	for _, suffix := range strings.Fields("al ance ence er ic able ible ant ement ment ent ou ism ate iti ous ive ize") {
		phase("4 at R2", "bacab"+suffix, "bacab", 3, 5, porterStep4)
		phase("4 before R2", "bacab"+suffix, "bacab"+suffix, 3, 6, porterStep4)
	}
	for _, tc := range [][2]string{{"bacabsion", "bacabs"}, {"bacabtion", "bacabt"}, {"bacabcion", "bacabcion"}, {"ion", "ion"}} {
		phase("4 ion", tc[0], tc[1], 0, 0, porterStep4)
	}
	for _, tc := range []struct {
		input, want string
		p1, p2      int
	}{
		{"rate", "rate", 3, 4}, {"rate", "rat", 3, 3}, {"cease", "ceas", 3, 5},
		{"abe", "ab", 2, 3}, {"be", "be", 2, 2}, {"snowe", "snow", 4, 5},
	} {
		phase("5a", tc.input, tc.want, tc.p1, tc.p2, porterStep5a)
	}
	phase("5b at R2", "controll", "control", 3, 7, porterStep5b)
	phase("5b before R2", "roll", "roll", 3, 4, porterStep5b)
	phase("5b single", "control", "control", 0, 0, porterStep5b)
	phase("4 ion before R2", "bacabtion", "bacabtion", 3, 7, porterStep4)
	phase("4 longest rejection", "bacabement", "bacabement", 3, 6, porterStep4)
	phase("2 longest rejection", "bacization", "bacization", 4, 99, porterStep2)
}

func TestSnowballPorterDictionary(t *testing.T) {
	ctx := t.Context()
	d, err := CompileSnowballDictionary(ctx, "PoRtEr", "english")
	if err != nil {
		t.Fatal(err)
	}
	free, err := CompileSnowballDictionary(ctx, "porter", "")
	if err != nil || free.words != nil {
		t.Fatal(free, err)
	}
	english, err := CompileSnowballDictionary(ctx, "english", "english")
	if err != nil {
		t.Fatal(err)
	}
	simple, err := CompileSimpleDictionary(ctx, true, "english")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Configured("PoRtEr", "english") || d.Configured("porter", "english") || d.provider != free.provider || d.provider == english.provider || &d.words[0] != &english.words[0] || &d.words[0] != &simple.words[0] {
		t.Fatal("configured identity or exact shared resource backing")
	}
	backing := slices.Clone(d.words)
	check := func(runtime *SnowballDictionary, input, want string) {
		t.Helper()
		got, err := runtime.Lexize(ctx, input)
		if err != nil || !got.Recognized || want == "" && len(got.Lexemes) != 0 || want != "" && (len(got.Lexemes) != 1 || got.Lexemes[0].Text != want) {
			t.Errorf("%q: got %+v, want %q, error %v", input, got, want, err)
		}
		for _, lexeme := range got.Lexemes {
			if lexeme.Prefix || lexeme.Variant != 0 {
				t.Error("flags", lexeme)
			}
		}
	}
	for _, tc := range [][2]string{{"THE", ""}, {"ONLY", ""}, {"s", ""}, {"SKIES", "ski"}, {"DYING", "dy"}, {"", ""}} {
		check(d, tc[0], tc[1])
	}
	for _, tc := range [][2]string{
		{"THE", "the"}, {"ONLY", "onli"}, {"s", "s"}, {"SKIES", "ski"}, {"RATS", "rat"},
		{"CAFÉS", "cafÉ"}, {"cafés", "café"}, {"cafe\u0301s", "cafe\u0301"}, {"cats!", "cats!"}, {"", ""},
		{strings.Repeat("A", 997) + "ies", strings.Repeat("a", 997) + "i"},
		{strings.Repeat("A", 998) + "ies", strings.Repeat("a", 998) + "ies"},
		{strings.Repeat("é", 498) + "aies", strings.Repeat("é", 498) + "ai"},
		{strings.Repeat("é", 499) + "ies", strings.Repeat("é", 499) + "ies"},
	} {
		check(free, tc[0], tc[1])
	}
	for _, resource := range []string{"porter", "porter_missing", "English", "../english"} {
		if bad, err := CompileSnowballDictionary(ctx, "porter", resource); bad != nil || err == nil {
			t.Fatal(resource, bad, err)
		}
	}
	for _, language := range []string{" porter ", "porter_bogus", ""} {
		if bad, err := CompileSnowballDictionary(ctx, language, ""); bad != nil || err == nil {
			t.Fatal(language, bad, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	expired, expire := context.WithDeadline(ctx, time.Unix(0, 0))
	defer expire()
	for _, tc := range []struct {
		ctx  context.Context
		want error
	}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
		if bad, err := CompileSnowballDictionary(tc.ctx, "porter", "english"); bad != nil || !errors.Is(err, tc.want) {
			t.Fatal(bad, err)
		}
		if got, err := d.Lexize(tc.ctx, "SKIES"); !errors.Is(err, tc.want) || got.Recognized || len(got.Lexemes) != 0 {
			t.Fatal(got, err)
		}
	}
	// Returned lexical slices and every invocation's kernel scratch are owned.
	first, err := free.Lexize(ctx, "SKIES")
	if err != nil {
		t.Fatal(err)
	}
	first.Lexemes[0].Text = "caller-owned"
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(d, "ONLY", "")
				check(free, "ONLY", "onli")
				check(free, "SKIES", "ski")
				if got := porterStem("playing"); got != "plai" {
					t.Error(got)
				}
			}
		})
	}
	wg.Wait()
	if !slices.Equal(d.words, backing) || first.Lexemes[0].Text != "caller-owned" {
		t.Fatal("mutated retained output/resource")
	}
	dictionaryStopwords.Lock()
	_, porter := dictionaryStopwords.words["porter"]
	_, missing := dictionaryStopwords.words["porter_missing"]
	_, empty := dictionaryStopwords.words[""]
	cached := dictionaryStopwords.words["english"]
	dictionaryStopwords.Unlock()
	if porter || missing || empty || &cached[0] != &d.words[0] {
		t.Fatal("failed compilation published resource backing")
	}
}
