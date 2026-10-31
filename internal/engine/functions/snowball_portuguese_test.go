package functions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestSnowballPortugueseRules(t *testing.T) {
	// Historical snowball-data goldens and independently release-traced phase
	// witnesses. Expectations never iterate the implementation's rule tables.
	cases := [][2]string{
		{"ações", "açõ"}, {"ativamente", "ativ"}, {"cadeiras", "cadeir"},
		{"casa", "cas"}, {"casas", "cas"}, {"ciência", "ciênc"},
		{"corações", "coraçõ"}, {"correndo", "corr"}, {"correr", "corr"},
		{"felicidade", "felic"}, {"lápis", "láp"}, {"mães", "mã"},
		{"pague", "pag"}, {"rapidamente", "rapid"},
		{"abacico", "abac"}, {"abico", "abic"}, {"baca", "bac"}, {"aba", "aba"},
		{"bacar", "bac"}, {"abcar", "abcar"}, {"aaae", "aaae"}, {"abamente", "ab"},
		{"abaceira", "abaceir"}, {"abacativamente", "abac"},
		{"abacabilidades", "abac"}, {"abacicidade", "abac"}, {"abacatividades", "abacat"},
		{"abaciando", "abac"}, {"abacgue", "abacg"}, {"abaccie", "abacc"},
		{"ç", "c"}, {"casas!", "casas!"}, {"casas ", "casas "}, {"cafe\u0301", "cafe\u0301"},
		{"a~", "ã"}, {"o~", "õ"}, {"não", "nã"}, {"a~~", "ã~"},
		{"abacadamente", "abac"}, {"abacosamente", "abac"}, {"abacicamente", "abac"},
		{"abacantemente", "abac"}, {"abacavelmente", "abac"}, {"abacivelmente", "abacivel"},
		{"abacívelmente", "abac"}, // secondary mente removes accented ível, not ivel.
		{"abacatividade", "abacat"}, {"abacativo", "abac"},
		{"abacicativo", "abacic"},     // ivo removes at, not the newly exposed ic.
		{"abacávelmente", "abacável"}, // secondary mente has avel, not ável.
		{"amente", "ament"},           // failed standard region does not retry mente.
		{"abacç", "abacc"}, {"abacci", "abacc"}, {"abacguê", "abacg"},
		{"bariam", "bar"}, // RV excludes ariam but admits its shorter iam ending.
	}
	// All 45 standard endings in natural spelling, grouped by independent
	// expected action. abac fixes R1=2, R2=4, RV=3 before any suffix is edited.
	for _, group := range []struct{ endings, tail string }{
		{"ica icas ância adora adoras ador adores ante antes aça~o aço~es oso osos osa osas ismo ismos ista istas ico icos eza ezas ável ível amento amentos imento imentos amente mente idade idades ivo ivos iva ivas ira iras", ""},
		{"logia logias", "log"}, {"uça~o uço~es", "u"}, {"ência ências", "ent"},
	} {
		for suffix := range strings.FieldsSeq(group.endings) {
			cases = append(cases, [2]string{"abac" + suffix, "abac" + group.tail})
		}
	}
	// All 120 released verb endings, independently transcribed in conjugation
	// families, with an RV-admitted stable prefix and a fixed expected stem.
	for suffix := range strings.FieldsSeq("ada adas ado ados ida idas ido idos ando endo indo ar er ir am em as es is eu iu ou ei ia ias iam íamos íeis imos emos amos ámos ais eis ava avas avam ávamos áveis ara aras aram áramos áreis era eras eram éramos éreis ira iras iram íramos íreis aria arias ariam aríamos aríeis eria erias eriam eríamos eríeis iria irias iriam iríamos iríeis asse asses assem ássemos ásseis esse esses essem êssemos ésseis isse isses issem íssemos ísseis aste astes este estes iste istes arei areis aremos ará arás arão erei ereis eremos erá erás erão irei ireis iremos irá irás irão arem ares ardes armos erem eres erdes ermos irem ires irdes irmos") {
		cases = append(cases, [2]string{"abac" + suffix, "abac"})
	}
	for _, tc := range cases {
		if got := portugueseStem(tc[0]); got != tc[1] {
			t.Errorf("%q: got %q, want %q", tc[0], got, tc[1])
		}
	}
	// Distinguish primary predicates from later verb/residual effects.
	for _, tc := range []struct {
		word       string
		rv, r1, r2 int
		success    bool
		want       string
	}{
		{"abaceira", 3, 2, 4, true, "abaceir"},
		{"abacira", 3, 2, 4, false, "abacira"},
		{"abamente", 3, 2, 4, true, "ab"},
		{"amente", 3, 2, 4, false, "amente"},
		{"abidade", 3, 2, 4, false, "abidade"},
		{"abacamente", 3, 2, 4, true, "abac"},
	} {
		w := portugueseWord{prefix: tc.word}
		if success := w.standard(tc.rv, tc.r1, tc.r2); success != tc.success || w.prefix+w.tail != tc.want {
			t.Error("standard region/predicate", tc, success, w)
		}
	}
	for _, tc := range []struct {
		word       string
		rv, r1, r2 int
	}{
		{"", 0, 0, 0}, {"a", 1, 1, 1}, {"abacando", 3, 2, 4},
		{"baca", 3, 3, 4}, {"abcar", 4, 2, 5}, {"aaae", 4, 4, 4},
		{"áábc", 5, 5, 6}, {"brácar", 4, 5, 7},
	} {
		s := portuguesePrelude(tc.word)
		r1 := portugueseRegion(s, 0)
		if rv, r2 := portugueseRV(s), portugueseRegion(s, r1); rv != tc.rv || r1 != tc.r1 || r2 != tc.r2 {
			t.Error("fixed byte regions", tc, rv, r1, r2)
		}
	}
	for _, r := range "aeiouáéíóúâêô" {
		if !portugueseVowel(r) {
			t.Errorf("released vowel %q", r)
		}
	}
	for _, r := range "ãõüàèìòùyAEIOU~" {
		if portugueseVowel(r) {
			t.Errorf("not a released vowel %q", r)
		}
	}
}

func TestSnowballPortugueseResources(t *testing.T) {
	ctx := t.Context()
	d, err := DefaultPortugueseSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	// Independent complete REL_18_6 inventory, not read from the embedded file.
	stops := strings.Fields("de a o que e do da em um para com não uma os no se na por mais as dos como mas ao ele das à seu sua ou quando muito nos já eu também só pelo pela até isso ela entre depois sem mesmo aos seus quem nas me esse eles você essa num nem suas meu às minha numa pelos elas qual nós lhe deles essas esses pelas este dele tu te vocês vos lhes meus minhas teu tua teus tuas nosso nossa nossos nossas dela delas esta estes estas aquele aquela aqueles aquelas isto aquilo estou está estamos estão estive esteve estivemos estiveram estava estávamos estavam estivera estivéramos esteja estejamos estejam estivesse estivéssemos estivessem estiver estivermos estiverem hei há havemos hão houve houvemos houveram houvera houvéramos haja hajamos hajam houvesse houvéssemos houvessem houver houvermos houverem houverei houverá houveremos houverão houveria houveríamos houveriam sou somos são era éramos eram fui foi fomos foram fora fôramos seja sejamos sejam fosse fôssemos fossem for formos forem serei será seremos serão seria seríamos seriam tenho tem temos tém tinha tínhamos tinham tive teve tivemos tiveram tivera tivéramos tenha tenhamos tenham tivesse tivéssemos tivessem tiver tivermos tiverem terei terá teremos terão teria teríamos teriam")
	slices.Sort(stops)
	if !slices.Equal(d.words, stops) {
		t.Fatal("full released stoplist", d.words)
	}
	check := func(runtime *SnowballDictionary, input, want string) {
		t.Helper()
		got, err := runtime.Lexize(ctx, input)
		if err != nil || !got.Recognized || want == "" && len(got.Lexemes) != 0 || want != "" && (len(got.Lexemes) != 1 || got.Lexemes[0].Text != want) {
			t.Error(input, got, want, err)
		}
		for _, lexeme := range got.Lexemes {
			if lexeme.Prefix || lexeme.Variant != 0 {
				t.Error("lexeme flags", lexeme)
			}
		}
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
	for _, tc := range [][2]string{
		{"", ""}, {"Não", ""}, {"NÃO", "nÃo"}, {"nao", "nao"}, {"têm", "têm"},
		{"sim", "sim"}, {"CASAS", "cas"}, {"casas!", "casas!"}, {"cafe\u0301", "cafe\u0301"},
		{strings.Repeat("ab", 498) + "ando", strings.Repeat("ab", 498)},
		{strings.Repeat("AB", 498) + "aando", strings.Repeat("ab", 498) + "aando"},
		{strings.Repeat("á", 498) + "ando", strings.Repeat("á", 498) + "and"},
		{strings.Repeat("á", 498) + "aando", strings.Repeat("á", 498) + "aando"},
	} {
		check(d, tc[0], tc[1])
	}
	free, err := CompileSnowballDictionary(ctx, "portuguese", "")
	if err != nil || free.words != nil {
		t.Fatal("resource-free provider", free, err)
	}
	check(free, "não", "nã")
	mixed, err := CompileSnowballDictionary(ctx, "PoRtUgUeSe", "portuguese")
	if err != nil || mixed.provider != d.provider || !mixed.Configured("PoRtUgUeSe", "portuguese") || mixed.Configured("portuguese", "portuguese") {
		t.Fatal("configured spelling and provider authority", mixed, err)
	}
	simple, err := CompileSimpleDictionary(ctx, true, "portuguese")
	if err != nil {
		t.Fatal(err)
	}
	if &mixed.words[0] != &d.words[0] || &simple.words[0] != &d.words[0] {
		t.Fatal("shared immutable resource backing")
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
	for _, resource := range []string{"portuguese_missing", "PORTUGUESE", "../portuguese"} {
		if bad, err := CompileSnowballDictionary(ctx, "portuguese", resource); bad != nil || err == nil {
			t.Fatal(resource, bad, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if bad, err := CompileSnowballDictionary(canceled, "portuguese", "portuguese"); bad != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(bad, err)
	}
	if _, err := d.Lexize(canceled, "CASAS"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["portuguese_missing"]
	backing := dictionaryStopwords.words["portuguese"]
	dictionaryStopwords.Unlock()
	if missing || &backing[0] != &d.words[0] || !slices.Equal(backing, stops) {
		t.Fatal("failed compilation changed immutable cache")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(d, "CASAS", "cas")
				check(d, "não", "")
				check(free, "não", "nã")
			}
		})
	}
	wg.Wait()
}
