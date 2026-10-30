package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/sqlstate"
)

func greekUnitASCII(s string) string {
	return strings.Map(func(c rune) rune {
		if c >= 'A' && c <= 'Z' {
			return c + 'a' - 'A'
		}
		return c
	}, s)
}

func TestSnowballGreekRules(t *testing.T) {
	// Literal full-pipeline witnesses, independently traced from the release.
	// Public corpus/oracle proof belongs to the reference suite, not this unit.
	for _, pair := range [][2]string{
		{"THE", "the"}, {"ONLY", "only"}, {"RATS", "rats"}, {"running", "running"},
		{"να", "να"}, {"το", "το"}, {"και", "κα"}, {"καλός", "καλ"}, {"ΚΑΛΌΣ", "καλ"}, {"καλό", "καλ"},
		{"κρέας", "κρε"}, {"κρέατα", "κρε"}, {"πέρας", "περ"}, {"τέρας", "τερ"}, {"φως", "φω"}, {"φώτων", "φω"},
		{"καθεστώς", "καθεστ"}, {"γεγονότος", "γεγον"}, {"παίζω", "παι"}, {"δανείζω", "δανει"}, {"δανεισμός", "δανει"},
		{"παιδάκια", "παιδ"}, {"κονάκια", "κονακ"}, {"δίσκος", "δισκ"}, {"μπαμπάδες", "μπαμπ"}, {"παπάδες", "παπαδ"},
		{"καφέδες", "καφ"}, {"θεών", "θε"}, {"παιδιά", "πα"}, {"νομικά", "νομ"}, {"φυσικό", "φυσικ"},
		{"τρέχοντας", "τρεχ"}, {"γράφουμε", "γραφ"}, {"γράμματα", "γραμμ"}, {"θέματα", "θεμ"}, {"καλύτερος", "καλ"}, {"καλύτερη", "καλ"},
		{"", ""}, {"α", "α"}, {"αα", "αα"}, {"ααα", "αα"}, {"ματα", "μ"}, {"ισα", "ισ"}, {"ισω", ""},
		{"καλός!", "καλοσ!"}, {"καλο\u0301", "καλο\u0301"}, {"Α\u0301", "α\u0301"}, {"café", "café"}, {"CAFÉS", "cafÉs"},
		{" καλός", " καλ"}, {"καλός ", "καλοσ "}, {"α!", "α!"}, {"αα!", "αα!"},
		{"δανειζω", "δανει"}, {"ξδανειζω", "ξδ"},
		{"αλωθηκα", "αλων"}, {"ξαλωθηκα", "ξαλ"},
		{"ανισα", "ανισ"}, {"ξανισα", "ξαν"},
		{"δανεισω", "δανει"}, {"ξδανεισω", "ξδ"},
		{"μιστοσ", "μιστ"}, {"ξμιστοσ", "ξμ"},
		{"σεισμοσ", "σεισμ"}, {"ξσεισμοσ", "ξσε"},
		{"σαρακι", "σαρακ"}, {"ξσαρακι", "ξσ"},
		{"κονακι", "κονακ"}, {"ξκονακι", "ξκον"},
		{"αιφνιδια", "αιφνιδ"}, {"ξαιφνιδια", "ξαιφν"},
		{"δισκοσ", "δισκ"}, {"ξδισκοσ", "ξδ"},
		{"αναπαμε", "αναπαμ"}, {"ξαναπαμε", "ξαναπ"},
		{"γανε", "γαν"}, {"ξγανε", "ξγ"},
		{"αλαικο", "αλικικ"}, {"γαανε", "γαναν"}, {"αβαραετε", "αβαρετετ"},
		// Released {p}{e}{r}{i}{f} uses eta, not iota; this literal
		// regression is independent of the exhaustive exception manifest.
		{"περηφανε", "περηφαν"}, {"ξπερηφανε", "ξπερηφ"},
		{"περηφξανε", "περηφξ"}, {"περιφανε", "περιφ"},
		{"υετε", "υετ"}, {"ξυετε", "ξυ"},
		{"αρχοντασ", "αρχοντ"}, {"ξαρχοντασ", "ξαρχ"},
		{"ονομαστε", "ονομαστ"}, {"ξονομαστε", "ξον"},
		{"αλεστε", "αλιεστ"}, {"ξαλεστε", "ξαλ"},
		{"θηκα", "θηκ"}, {"ξθηκα", "ξθ"},
		{"μουσα", "μουσ"}, {"ξμουσα", "ξμ"},
		{"νησε", "νησ"}, {"ξνησε", "ξν"},
		{"αβασταγα", "αβασταγ"}, {"ξαβασταγα", "ξαβαστ"},
		{"ασβηστε", "ασβηστ"}, {"ξασβηστε", "ξασβ"},
		{"νουνε", "νουν"}, {"ξνουνε", "ξν"},
		{"φουμε", "φουμ"}, {"ξφουμε", "ξφ"},
		{"ψοφαγα", "ψοφ"}, {"ναυλοχαγα", "ναυλοχ"},
	} {
		if got := greekStem(greekUnitASCII(pair[0])); got != pair[1] {
			t.Errorf("%q -> %q, want %q", pair[0], got, pair[1])
		}
	}
	t.Run("finite-normalization", func(t *testing.T) {
		from := []rune("ΑΒΓΔΕΖΗΘΙΚΛΜΝΞΟΠΡΣΤΥΦΧΨΩΪΫάέήίόύώΐΰϊϋΆΈΉΊΌΎΏς")
		to := []rune("αβγδεζηθικλμνξοπρστυφχψωιυαεηιουωηυηυαεηιουωσ")
		if len(from) != len(to) {
			t.Fatal("independent normalization manifest lengths")
		}
		for i, c := range from {
			if got := greekLower(c); got != to[i] {
				t.Errorf("normalization %U -> %U, want %U", c, got, to[i])
			}
			for _, token := range []string{string(c), string([]rune{c, c})} {
				want := strings.Repeat(string(to[i]), len([]rune(token)))
				if got := greekStem(token); got != want {
					t.Errorf("normalize before minimum %q -> %q, want %q", token, got, want)
				}
			}
		}
		for _, c := range []rune("\u036f\u0370\u0385\u0387\u038b\u038d\u03a2\u03cf\u03d0\u0400É\u0301") {
			if got := greekLower(c); got != c {
				t.Errorf("unmapped neighbor %U -> %U", c, got)
			}
		}
	})
}

func TestSnowballGreekResources(t *testing.T) {
	ctx := t.Context()
	stock, err := DefaultGreekSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if stock.words != nil || !stock.Configured("greek", "") || stock.Filtering() {
		t.Fatal("language-only authority", stock)
	}
	mixed, err := CompileSnowballDictionary(ctx, "GrEeK", "")
	if err != nil || mixed.provider != stock.provider || !mixed.Configured("GrEeK", "") || mixed.Configured("greek", "") {
		t.Fatal("configured spelling", mixed, err)
	}
	fake := *stock
	provider := *stock.provider
	fake.provider = &provider
	if fake.Configured("greek", "") {
		t.Fatal("copied provider forged registry authority")
	}
	withStops, err := CompileSnowballDictionary(ctx, "greek", "english")
	if err != nil {
		t.Fatal(err)
	}
	english, err := CompileSnowballDictionary(ctx, "english", "english")
	if err != nil {
		t.Fatal(err)
	}
	simple, err := CompileSimpleDictionary(ctx, true, "english")
	if err != nil || len(withStops.words) != 127 || &withStops.words[0] != &english.words[0] || &withStops.words[0] != &simple.words[0] {
		t.Fatal("shared immutable English stop backing", err)
	}
	check := func(d *SnowballDictionary, input, want string) {
		t.Helper()
		got, err := d.Lexize(ctx, input)
		if err != nil || !got.Recognized || want == "" && len(got.Lexemes) != 0 || want != "" && (len(got.Lexemes) != 1 || got.Lexemes[0].Text != want) {
			t.Error(input, got, want, err)
			return
		}
		for _, lexeme := range got.Lexemes {
			if lexeme.Prefix || lexeme.Variant != 0 {
				t.Error("lexeme flags", lexeme)
			}
		}
	}
	for _, pair := range [][2]string{
		{"THE", "the"}, {"ONLY", "only"}, {"RATS", "rats"}, {"να", "να"}, {"το", "το"}, {"καλός", "καλ"}, {"ΚΑΛΌΣ", "καλ"},
		{"", ""}, {"ισω", "ισω"}, {"ουν", "ουν"},
		{strings.Repeat("Α", 500), strings.Repeat("α", 499)},
		{"A" + strings.Repeat("Α", 500), "a" + strings.Repeat("Α", 500)},
		{strings.Repeat("A", 1000), strings.Repeat("a", 1000)},
		{strings.Repeat("A", 1001), strings.Repeat("a", 1001)},
		{strings.Repeat("A", 996) + "ΆΆ", strings.Repeat("a", 996) + "α"},
		{strings.Repeat("A", 997) + "ΆΆ", strings.Repeat("a", 997) + "ΆΆ"},
	} {
		check(stock, pair[0], pair[1])
	}
	for _, stop := range english.words {
		check(withStops, stop, "")
	}
	check(withStops, "THE", "")
	check(withStops, "ONLY", "")
	check(withStops, "RATS", "rats")
	check(withStops, "καλός", "καλ")
	for range 3 {
		d, err := CompileSnowballDictionary(ctx, "greek", "greek")
		var diagnostic *sqlstate.Error
		if d != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != `could not open stop-word file "greek.stop": No such file or directory` {
			t.Fatal("missing Greek resource", d, err)
		}
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["greek"]
	dictionaryStopwords.Unlock()
	if missing {
		t.Fatal("missing resource published in cache")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "greek", ""); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled compile", d, err)
	}
	if _, err := stock.Lexize(canceled, "ΚΑΛΌΣ"); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled lexize", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(stock, "ΚΑΛΌΣ", "καλ")
				check(mixed, "ONLY", "only")
				check(withStops, "ONLY", "")
				check(stock, "δανείζω", "δανει")
			}
		})
	}
	wg.Wait()
}
