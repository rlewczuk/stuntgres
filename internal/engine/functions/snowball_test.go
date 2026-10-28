package functions

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
)

func TestSnowballEnglishRules(t *testing.T) {
	snowballEnglishStages(t)
	// Independent released-rule examples: exception tables, every suffix branch,
	// region/preceding-letter failures, short syllables, possessives and Y marking.
	cases := [][2]string{
		{"rats", "rat"}, {"running", "run"}, {"skis", "ski"}, {"skies", "sky"}, {"dying", "die"}, {"lying", "lie"}, {"tying", "tie"}, {"idly", "idl"}, {"gently", "gentl"}, {"ugly", "ugli"}, {"early", "earli"}, {"only", "onli"}, {"singly", "singl"},
		{"sky", "sky"}, {"news", "news"}, {"howe", "howe"}, {"atlas", "atlas"}, {"cosmos", "cosmos"}, {"bias", "bias"}, {"andes", "andes"},
		{"innings", "inning"}, {"outings", "outing"}, {"cannings", "canning"}, {"herrings", "herring"}, {"earrings", "earring"}, {"proceeds", "proceed"}, {"exceeds", "exceed"}, {"succeeds", "succeed"},
		{"caresses", "caress"}, {"ties", "tie"}, {"cries", "cri"}, {"tied", "tie"}, {"agreed", "agre"}, {"feed", "feed"}, {"gas", "gas"}, {"gaps", "gap"}, {"this", "this"}, {"us", "us"}, {"caress", "caress"},
		{"added", "add"}, {"ebbed", "ebb"}, {"offing", "off"}, {"hopping", "hop"}, {"fizzed", "fizz"}, {"filing", "file"}, {"hoped", "hope"}, {"plastered", "plaster"}, {"bled", "bled"}, {"sing", "sing"}, {"conflated", "conflat"}, {"troubled", "troubl"}, {"sized", "size"}, {"falling", "fall"}, {"happily", "happili"}, {"say", "say"}, {"by", "by"},
		{"relational", "relat"}, {"conditional", "condit"}, {"rational", "ration"}, {"valenci", "valenc"}, {"hesitanci", "hesit"}, {"radicabli", "radic"}, {"differentli", "differ"}, {"vietnamization", "vietnam"}, {"digitizer", "digit"}, {"conformabli", "conform"}, {"predication", "predic"}, {"operator", "oper"}, {"feudalism", "feudal"}, {"decisiveness", "decis"}, {"hopefulness", "hope"}, {"callousness", "callous"}, {"formaliti", "formal"}, {"sensitiviti", "sensit"}, {"sensibiliti", "sensibl"}, {"analogousli", "analog"}, {"tripli", "tripli"}, {"geologi", "geolog"}, {"biologi", "biolog"}, {"vileli", "vile"}, {"fully", "fulli"}, {"carefulli", "care"}, {"hopelessli", "hopeless"}, {"ugli", "ug"},
		{"triplicate", "triplic"}, {"formative", "format"}, {"formalize", "formal"}, {"electriciti", "electr"}, {"electrical", "electr"}, {"hopeful", "hope"}, {"goodness", "good"},
		{"revival", "reviv"}, {"allowance", "allow"}, {"inference", "infer"}, {"airliner", "airlin"}, {"gyroscopic", "gyroscop"}, {"adjustable", "adjust"}, {"defensible", "defens"}, {"irritant", "irrit"}, {"replacement", "replac"}, {"adjustment", "adjust"}, {"dependent", "depend"}, {"adoption", "adopt"}, {"communion", "communion"}, {"homologou", "homologou"}, {"communism", "communism"}, {"activate", "activ"}, {"angulariti", "angular"}, {"homologous", "homolog"}, {"effective", "effect"}, {"bowdlerize", "bowdler"},
		{"probate", "probat"}, {"rate", "rate"}, {"cease", "ceas"}, {"controll", "control"}, {"roll", "roll"}, {"generously", "generous"}, {"arsenal", "arsenal"}, {"communally", "communal"}, {"evening", "even"}, {"geologist", "geologist"}, {"youthfully", "youth"}, {"boyish", "boyish"}, {"enjoy", "enjoy"},
		{"children's", "children"}, {"children’s", "children’"}, {"'skies", "ski"}, {"ès", "ès"}, {"cafés", "café"}, {"cafÉs", "cafÉ"}, {"hôtels", "hôtel"}, {"cats!", "cats!"},
	}
	for _, tc := range cases {
		if got := englishStem(tc[0]); got != tc[1] {
			t.Errorf("%q -> %q, want %q", tc[0], got, tc[1])
		}
	}
}

func TestSnowballDictionaryResources(t *testing.T) {
	ctx := context.Background()
	d, err := CompileSnowballDictionary(ctx, "english", "english")
	if err != nil {
		t.Fatal(err)
	}
	simple, err := CompileSimpleDictionary(ctx, true, "english")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.words) != 127 || &d.words[0] != &simple.words[0] {
		t.Fatal("released stoplist not shared")
	}
	array := dictionaryArrayType(t)
	check := func(input, want string, empty bool) {
		t.Helper()
		lexical, err := d.Lexize(ctx, input)
		if err != nil || !lexical.Recognized {
			t.Fatal(lexical, err)
		}
		for _, word := range lexical.Lexemes {
			if word.Prefix || word.Variant != 0 {
				t.Fatal(word)
			}
		}
		v, err := sqltype.LexicalArray(array, lexical)
		if err != nil || v.IsNull() || v.Type() != array {
			t.Fatal(v, err)
		}
		if empty {
			if v.ArrayLen() != 0 || len(v.ArrayDimensions()) != 0 {
				t.Fatal(v)
			}
			return
		}
		if v.ArrayLen() != 1 {
			t.Fatal(v)
		}
		got, err := v.ArrayElement(0).Text()
		if err != nil || got != want {
			t.Fatal(input, got, want, err)
		}
	}
	for _, word := range d.words {
		check(strings.ToUpper(word), "", true)
	}
	check("", "", true)
	check("RATS", "rat", false)
	check("CAFÉS", "cafÉ", false)
	check(strings.Repeat("a", 997)+"ies", strings.Repeat("a", 997)+"i", false)
	check(strings.Repeat("A", 998)+"ies", strings.Repeat("a", 998)+"ies", false)
	check(strings.Repeat("é", 498)+"aies", strings.Repeat("é", 498)+"ai", false)
	check(strings.Repeat("é", 499)+"ies", strings.Repeat("é", 499)+"ies", false)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := CompileSnowballDictionary(canceled, "english", "english"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := d.Lexize(canceled, "RATS"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// The shared provider has no mutable Snowball environment or token cache.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check("RATS", "rat", false)
				check("ONLY", "", true)
			}
		})
	}
	wg.Wait()
}

func TestSnowballLanguageProvider(t *testing.T) {
	a, err := CompileSnowballDictionary(t.Context(), "english", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileSnowballDictionary(t.Context(), "EnGlIsH", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.provider != b.provider || !a.Configured("english", "") || !b.Configured("EnGlIsH", "") || b.Configured("english", "") {
		t.Fatal("provider/spelling authority")
	}
	for _, language := range []string{"", " english ", "snowball_nonexistent", "0", "true"} {
		if d, err := CompileSnowballDictionary(t.Context(), language, ""); d != nil || err == nil {
			t.Fatal(language, d, err)
		}
	}
}
