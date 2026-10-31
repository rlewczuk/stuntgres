package functions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestSnowballSpanishRules(t *testing.T) {
	// Independent historical snowball-data 4f982ec072a091771b321ab7861841471942e14a
	// pairs and REL_18_6 source-traced actions; never enumerate production tables.
	cases := [][2]string{
		{"activamente", "activ"}, {"árboles", "arbol"}, {"biología", "biolog"}, {"caballos", "caball"}, {"camión", "camion"}, {"canción", "cancion"}, {"canciones", "cancion"}, {"casas", "cas"}, {"construyendo", "constru"}, {"corriendo", "corr"}, {"diferencias", "diferent"}, {"educación", "educ"}, {"educativos", "educ"}, {"hablando", "habl"}, {"haciéndola", "hac"}, {"niños", "niñ"}, {"pague", "pag"}, {"perros", "perr"}, {"rápidamente", "rapid"}, {"revolución", "revolu"}, {"vergüenza", "vergüenz"},
		{"educacion", "educacion"}, {"revolucion", "revolucion"}, {"abacacion", "abacacion"}, {"abacucion", "abacucion"},
		{"abico", "abic"}, {"bacar", "bac"}, {"abcar", "abcar"}, {"aaae", "aaae"}, {"abamente", "ab"},
		{"abacativamente", "abac"}, {"abacosamente", "abac"}, {"abacicamente", "abac"}, {"abacadamente", "abac"}, {"abacantemente", "abac"}, {"abacablemente", "abac"}, {"abaciblemente", "abac"}, {"abacabilidades", "abac"}, {"abacicidad", "abac"}, {"abacividad", "abac"}, {"abacativo", "abac"}, {"abacatividades", "abacat"},
		{"abacándome", "abac"}, {"abaciéndolo", "abac"}, {"abacárselas", "abac"}, {"abacérselo", "abac"}, {"abacírselo", "abac"}, {"abacuyendolo", "abacu"}, {"abacyendolo", "abacyendol"},
		{"abuyan", "abu"}, {"abacyendo", "abacyend"}, {"agues", "ag"}, {"ague", "agu"}, {"ü", "ü"}, {"ñ", "ñ"}, {"á", "a"}, {"ÁÉÍÓÚ", "ÁÉÍÓÚ"}, {"cafe\u0301", "cafe\u0301"}, {"casas!", "casas!"},
	}
	for _, group := range []struct{ endings, tail string }{
		{"anza anzas ico ica icos icas ismo ismos able ables ible ibles ista istas oso osa osos osas amiento amientos imiento imientos adora ador ación adoras adores aciones ante antes ancia ancias amente mente idad idades iva ivo ivas ivos", ""},
		{"logía logías", "log"}, {"ución uciones", "u"}, {"encia encias", "ent"},
	} {
		for ending := range strings.FieldsSeq(group.endings) {
			cases = append(cases, [2]string{"abac" + ending, "abac" + group.tail})
		}
	}
	for ending := range strings.FieldsSeq("en es éis emos arían arías arán arás aríais aría aréis aríamos aremos ará aré erían erías erán erás eríais ería eréis eríamos eremos erá eré irían irías irán irás iríais iría iréis iríamos iremos irá iré aba ada ida ía ara iera ad ed id ase iese aste iste an aban ían aran ieran asen iesen aron ieron ado ido ando iendo ió ar er ir as abas adas idas ías aras ieras ases ieses ís áis abais íais arais ierais aseis ieseis asteis isteis ados idos amos ábamos íamos imos áramos iéramos iésemos ásemos") {
		cases = append(cases, [2]string{"abac" + ending, "abac"})
	}
	for ending := range strings.FieldsSeq("ya ye yan yen yeron yendo yo yó yas yes yais yamos") {
		cases = append(cases, [2]string{"abacu" + ending, "abacu"})
	}
	for ending := range strings.FieldsSeq("me se sela selo selas selos la le lo las les los nos") {
		cases = append(cases, [2]string{"abacar" + ending, "abac"})
	}
	for _, tc := range cases {
		if got := spanishStem(tc[0]); got != tc[1] {
			t.Errorf("%q: got %q, want %q", tc[0], got, tc[1])
		}
	}
	// Stage predicates distinguish global-longest rejection from RV-limited
	// fallback, even where a later phase could obscure the primary decision.
	w := spanishWord{prefix: "amente"}
	if w.standard(2, 4) || w.prefix != "amente" {
		t.Fatal("must not retry mente", w)
	}
	w = spanishWord{prefix: "barían"}
	if !w.verb(3) || w.prefix != "bar" {
		t.Fatal("RV admits ían, not arían", w)
	}
	w = spanishWord{prefix: "abacárselas"}
	rv, r1 := spanishRV(w.prefix), spanishRegion(w.prefix, 0)
	r2 := spanishRegion(w.prefix, r1)
	if rv != 3 || r1 != 2 || r2 != 4 || !w.pronoun(rv) || w.prefix+w.tail != "abacar" || !w.verb(rv) || w.prefix+w.tail != "abac" {
		t.Fatal("fixed original regions across accent replacement", rv, r1, r2, w)
	}
	for _, tc := range []struct {
		word       string
		rv, r1, r2 int
	}{{"", 0, 0, 0}, {"a", 1, 1, 1}, {"abacando", 3, 2, 4}, {"baca", 3, 3, 4}, {"abcar", 4, 2, 5}, {"aaae", 4, 4, 4}, {"áábc", 5, 5, 6}, {"brácar", 4, 5, 7}} {
		r1 := spanishRegion(tc.word, 0)
		if rv, r2 := spanishRV(tc.word), spanishRegion(tc.word, r1); rv != tc.rv || r1 != tc.r1 || r2 != tc.r2 {
			t.Error(tc, rv, r1, r2)
		}
	}
	for _, r := range "aeiouáéíóúü" {
		if !spanishVowel(r) {
			t.Error("missing vowel", r)
		}
	}
	for _, r := range "yñâêôàAEIOUÁÉÍÓÚ" {
		if spanishVowel(r) {
			t.Error("extra vowel", r)
		}
	}
}

func TestSnowballSpanishResources(t *testing.T) {
	ctx := t.Context()
	d, err := DefaultSpanishSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	check := func(runtime *SnowballDictionary, input, want string) {
		t.Helper()
		got, err := runtime.Lexize(ctx, input)
		if err != nil || !got.Recognized || want == "" && len(got.Lexemes) != 0 || want != "" && (len(got.Lexemes) != 1 || got.Lexemes[0].Text != want) {
			t.Error(input, got, want, err)
		}
		for _, lexeme := range got.Lexemes {
			if lexeme.Prefix || lexeme.Variant != 0 {
				t.Error(lexeme)
			}
		}
	}
	// Independent complete resource literal is supplied below, not loaded from
	// dictionaryResources or copied from the runtime under test.
	stops := strings.Fields(spanishExpectedStops)
	slices.Sort(stops)
	if len(stops) != 313 || !slices.Equal(d.words, stops) {
		t.Fatal("exact released 313 entries", d.words)
	}
	for _, word := range stops {
		check(d, word, "")
		upper := []byte(word)
		for i, c := range upper {
			if c >= 'a' && c <= 'z' {
				upper[i] -= 'a' - 'A'
			}
		}
		check(d, string(upper), "")
	}
	for _, tc := range [][2]string{{"", ""}, {"Más", ""}, {"MÁS", "mÁs"}, {"Él", "Él"}, {"TÚ", "tÚ"}, {"si", "si"}, {"vosotros", "vosotr"}, {"vosotras", "vosotr"}, {"CASAS", "cas"}, {"NIÑOS", "niÑ"},
		{strings.Repeat("AB", 498) + "ando", strings.Repeat("ab", 498)},
		{strings.Repeat("AB", 498) + "aando", strings.Repeat("ab", 498) + "aando"},
		{strings.Repeat("á", 498) + "ando", strings.Repeat("a", 498) + "and"},
		{strings.Repeat("á", 498) + "aando", strings.Repeat("á", 498) + "aando"},
	} {
		check(d, tc[0], tc[1])
	}
	free, err := CompileSnowballDictionary(ctx, "spanish", "")
	if err != nil || free.words != nil {
		t.Fatal(free, err)
	}
	check(free, "más", "mas")
	check(free, "teniendo", "ten")
	check(free, "sintiendo", "sint")
	check(free, "sentido", "sent")
	mixed, err := CompileSnowballDictionary(ctx, "SpAnIsH", "spanish")
	if err != nil || mixed.provider != d.provider || !mixed.Configured("SpAnIsH", "spanish") || mixed.Configured("spanish", "spanish") {
		t.Fatal("provider authority/spelling", mixed, err)
	}
	simple, err := CompileSimpleDictionary(ctx, true, "spanish")
	if err != nil {
		t.Fatal(err)
	}
	english, err := CompileSnowballDictionary(ctx, "english", "spanish")
	if err != nil {
		t.Fatal(err)
	}
	if &mixed.words[0] != &d.words[0] || &simple.words[0] != &d.words[0] || &english.words[0] != &d.words[0] {
		t.Fatal("shared immutable exact-resource backing")
	}
	for _, word := range stops {
		if got, err := simple.Lexize(ctx, word); err != nil || !got.Recognized || len(got.Lexemes) != 0 {
			t.Fatal(word, got, err)
		}
	}
	first, err := d.Lexize(ctx, "CASAS")
	if err != nil {
		t.Fatal(err)
	}
	first.Lexemes[0].Text = "changed"
	check(d, "CASAS", "cas")
	for _, resource := range []string{"spanish_missing", "Spanish", "../spanish"} {
		if bad, err := CompileSnowballDictionary(ctx, "spanish", resource); bad != nil || err == nil {
			t.Fatal(resource, bad, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if bad, err := CompileSnowballDictionary(canceled, "spanish", "spanish"); bad != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(bad, err)
	}
	if _, err := d.Lexize(canceled, "CASAS"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["spanish_missing"]
	backing := dictionaryStopwords.words["spanish"]
	dictionaryStopwords.Unlock()
	if missing || &backing[0] != &d.words[0] || !slices.Equal(backing, stops) {
		t.Fatal("failed compilation changed cache")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(d, "CASAS", "cas")
				check(d, "más", "")
				check(free, "más", "mas")
			}
		})
	}
	wg.Wait()
}
