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

func TestSnowballTurkishRules(t *testing.T) {
	// Independently release-traced corpus, corrected only by the pinned PG18.6
	// observations in tests-1. Marker expectations do not read production tables.
	cases := [][2]string{
		{"ev", "ev"}, {"evler", "ev"}, {"evlar", "evlar"}, {"kitaplar", "kitap"}, {"kitapları", "kitap"}, {"kitabı", "kitap"}, {"ağacı", "ağaç"}, {"evdeki", "ev"}, {"evdekiler", "evdeki"}, {"doktoruymuşsunuz", "doktor"}, {"kedim", "kedi"},
		{"ad", "ad"}, {"soyad", "soyad"}, {"abab", "abap"}, {"abac", "abaç"}, {"abağ", "abak"}, {"abad", "abadı"}, {"abeg", "abegi"}, {"abog", "abogu"}, {"abög", "abögü"}, {"abablar", "abab"}, {"ab", "ab"}, {"âbâlar", "âbâlar"},
		{"kitap'lar", "kitap"}, {"o'connor", "o"}, {"a'lar", "a"}, {"'kitaplar", ""}, {"kitaplar’", "kitaplar’"}, {"kitap'lar'ı", "kitap"}, {"", ""},
		{"abaxmışcasına", "abax"}, {"abaxmışsınızcasına", "abax"}, {"abaxmışlar", "abax"}, {"abaxdılar", "abax"}, {"abaxsalar", "abax"}, {"abaxdırlar", "abax"}, {"abaxdınız", "abax"}, {"abaxsanız", "abax"}, {"abaxmışsınız", "abax"}, {"abaxmışız", "abax"}, {"abaxmışımdır", "abax"}, {"abaxsındır", "abaxs"},
		{"abaxlarınca", "abax"}, {"abaxımca", "abax"}, {"abaxlarca", "abax"}, {"abaxlarına", "abax"}, {"abaxsına", "abaxs"}, {"abaxlarından", "abax"}, {"abaxlarını", "abax"}, {"abaxımdan", "abax"}, {"abaxlardan", "abax"}, {"abaxların", "abax"}, {"abaxımla", "abax"}, {"abaxlarla", "abax"}, {"abaxları", "abax"}, {"abaxımda", "abax"}, {"abaxlarda", "abax"}, {"abaxlarım", "abax"}, {"abaxdakiler", "abaxdaki"}, {"abaxdakilerdeki", "abax"}, {"abaxlarınki", "abaxlar"}, {"abaxımdaki", "abax"}, {"abaxlarındaki", "abax"},
		{"abaxler", "abaxler"}, {"ebexlar", "ebexlar"}, {"abaxdir", "abaxdir"}, {"abaxmiş", "abaxmiş"}, {"abaxyı", "abaxyı"}, {"abaxsı", "abaxsı"}, {"abaxnca", "abaxnca"}, {"abaxnın", "abaxn"}, {"abaxki", "abaxki"}, {"abaxndaki", "abaxn"}, {"abaxxyzı", "abaxxyzı"}, {"abaxxyzım", "abaxxyz"}, {"abaxyken", "abaxyke"}, {"abaxcasına", "abaxca"}, {"abaxsinız", "abaxs"}, {"abaxsiniz", "abax"}, {"abaexlar", "abaex"}, {"abaxse", "abax"}, {"abayı", "aba"}, {"abası", "abas"}, {"abanın", "aba"}, {"abayla", "aba"}, {"abayken", "ap"}, {"abayım", "ap"}, {"abayız", "ap"}, {"abaydı", "ap"}, {"abaymış", "ap"}, {"abaysa", "ap"}, {"abam", "aba"}, {"abamız", "aba"}, {"abaxm", "abaxm"}, {"abasına", "aba"}, {"abasında", "aba"}, {"abasından", "aba"}, {"abasını", "aba"}, {"abaxınınki", "abax"}, {"abaxımın", "abax"},
	}
	for _, f := range []struct{ base, suffixes string }{
		{"abax", "lar ları ı ın a da ta dan tan la ca ım ımız ınız dır mış dı dım dın dık sa sam san sak ken sın sınız ız dınız mışcasına ında ına ından ını daki ınki ındaki tı tım tın tık tır"},
		{"ebex", "ler leri i in e de den le ce im imiz iniz dir miş di sem sin siniz iz deki ti tim tin tik dim din dik tir sen sek"},
		{"obox", "u un um umuz unuz dur muş du sun sunuz uz tu tum tun tuk dum dun duk tur"},
		{"öböx", "ü ün üm ümüz ünüz dür müş dü sün sünüz üz tü tüm tün tük düm dün dük tür"},
	} {
		for suffix := range strings.FieldsSeq(f.suffixes) {
			cases = append(cases, [2]string{f.base + suffix, f.base})
		}
	}
	// cAsInA/DUr retain their own ordered optional person alternatives.
	cases = append(cases, [][2]string{
		{"ebexmişcesine", "ebex"}, {"abaxmışlarcasına", "abax"},
		{"abaxmışımcasına", "abax"}, {"abaxmışsıncasına", "abax"}, {"abaxmışızcasına", "abax"},
		{"abaxmışsınızdır", "abax"}, {"abaxmışlardır", "abax"}, {"abaxmışsındır", "abax"}, {"abaxmışızdır", "abax"},
	}...)
	for _, tc := range cases {
		if got := turkishStem(tc[0]); got != tc[1] {
			t.Errorf("%q: got %q want %q", tc[0], got, tc[1])
		}
	}
	// Each marker family has a fixed successful suffix and exact cursor prefix.
	for _, tc := range []struct {
		marker       turkishMarker
		word, prefix string
	}{
		{trPoss, "abaxımız", "abax"}, {trSU, "abaxı", "abax"}, {trLArI, "abaxları", "abax"}, {trYU, "abaxı", "abax"}, {trNU, "abaxnı", "abax"}, {trNUn, "abaxın", "abax"}, {trYA, "abaxa", "abax"}, {trNA, "abaxna", "abax"}, {trDA, "abaxda", "abax"}, {trNdA, "abaxnda", "abax"}, {trDAn, "abaxdan", "abax"}, {trNdAn, "abaxndan", "abax"}, {trYlA, "abaxla", "abax"}, {trKi, "abaxki", "abax"}, {trNcA, "abaxca", "abax"}, {trYUm, "abaxım", "abax"}, {trSUn, "abaxsın", "abax"}, {trYUz, "abaxız", "abax"}, {trSUnUz, "abaxsınız", "abax"}, {trLAr, "abaxlar", "abax"}, {trNUz, "abaxnız", "abax"}, {trDUr, "abaxdır", "abax"}, {trCAsInA, "abaxcasına", "abax"}, {trYDU, "abaxdım", "abax"}, {trYsA, "abaxsam", "abax"}, {trYmUs, "abaxmış", "abax"}, {trYken, "abaxken", "abax"},
	} {
		s := turkishState{word: tc.word, c: len(tc.word)}
		if !s.mark(tc.marker) || s.word[:s.c] != tc.prefix || s.word != tc.word {
			t.Error("marker cursor", tc, s)
		}
	}
	for _, tc := range []struct {
		word   string
		buffer rune
		ok     bool
		prefix string
	}{
		{"abay", 'y', true, "aba"}, {"abax", 'y', true, "abax"}, {"abaxy", 'y', false, "abaxy"}, {"abaxxyz", 'y', false, "abaxxyz"},
		{"abas", 's', true, "aba"}, {"abaxs", 's', false, "abaxs"}, {"aban", 'n', true, "aba"}, {"abaxn", 'n', false, "abaxn"},
	} {
		s := turkishState{word: tc.word, c: len(tc.word)}
		if got := s.optional(tc.buffer); got != tc.ok || got && s.word[:s.c] != tc.prefix {
			t.Error("optional context", tc, s)
		}
	}
	for _, tc := range []struct {
		word   string
		ok     bool
		prefix string
	}{{"abaxı", true, "abax"}, {"aba", true, "aba"}, {"abax", false, "abax"}, {"abaı", false, "abaı"}} {
		s := turkishState{word: tc.word, c: len(tc.word)}
		if got := s.optionalU(); got != tc.ok || got && s.word[:s.c] != tc.prefix {
			t.Error("optional U context", tc, s)
		}
	}
	for _, tc := range []struct {
		word string
		ok   bool
	}{{"abaexlar", true}, {"abaxler", false}, {"oboxu", true}, {"öböxü", true}, {"abaxi", false}} {
		s := turkishState{word: tc.word, c: len(tc.word)}
		if got := s.harmony(); got != tc.ok || s.c != len(tc.word) || s.word != tc.word {
			t.Error("test-only harmony search", tc, s)
		}
	}
	// Outer deletions survive failed optional ki continuations; noun ordering
	// does not restart after success. Assert the raw phase, not only postlude.
	for _, tc := range [][2]string{{"abaxlarla", "abax"}, {"abaxlarınki", "abaxlar"}, {"abaxımda", "abax"}, {"abaxcasına", "abaxca"}} {
		s := turkishState{word: tc[0], c: len(tc[0])}
		s.nounsStage()
		if s.word != tc[1] {
			t.Error("committed noun edits", tc, s)
		}
	}
	s := turkishState{word: "abablar", c: 7}
	s.nominal()
	if s.nouns || s.word != "abab" {
		t.Fatal("nominal early exit", s)
	}
	// Recursive ki consumes suffixes, while failed chains preserve spelling.
	for _, tc := range [][2]string{{"abaxdakilerdeki", "abax"}, {"abaxki", "abaxki"}} {
		s := turkishState{word: tc[0], c: len(tc[0])}
		s.chain()
		if s.word != tc[1] {
			t.Fatal("ki chain", tc, s)
		}
	}
}

func TestSnowballTurkishResources(t *testing.T) {
	ctx := t.Context()
	d, err := DefaultTurkishSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	if d.Filtering() {
		t.Fatal("Snowball must not filter")
	}
	stops := strings.Fields("acaba ama aslında az bazı belki biri birkaç birşey biz bu çok çünkü da daha de defa diye eğer en gibi hem hep hepsi her hiç için ile ise kez ki kim mı mu mü nasıl ne neden nerde nerede nereye niçin niye o sanki şey siz şu tüm ve veya ya yani")
	slices.Sort(stops)
	if len(stops) != 53 || !slices.Equal(stops, d.words) {
		t.Fatal("exact released 53", d.words)
	}
	check := func(runtime *SnowballDictionary, input, want string) {
		t.Helper()
		v, err := runtime.Lexize(ctx, input)
		if err != nil || !v.Recognized || want == "" && len(v.Lexemes) != 0 || want != "" && (len(v.Lexemes) != 1 || v.Lexemes[0].Text != want) {
			t.Error(input, v, want, err)
		}
		for _, l := range v.Lexemes {
			if l.Prefix || l.Variant != 0 {
				t.Error(l)
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
	for _, tc := range [][2]string{{"KITAPLAR", "kitap"}, {"bize", "biz"}, {"mi", "mi"}, {"bir", "bir"}, {"cok", "cok"}, {"ÇOK", "Çok"}, {"'kitaplar", "'kitaplar"}, {"KİTAPLAR", "kİtap"}, {"BİZ", "bİz"}, {"", ""}, {strings.Repeat("ab", 498) + "alar", strings.Repeat("ab", 498) + "a"}, {strings.Repeat("AB", 498) + "aalar", strings.Repeat("ab", 498) + "aalar"}, {strings.Repeat("ö", 498) + "eler", strings.Repeat("ö", 498) + "e"}, {strings.Repeat("ö", 498) + "eeler", strings.Repeat("ö", 498) + "eeler"}, {"'" + strings.Repeat("AB", 500), "'" + strings.Repeat("ab", 500)}} {
		check(d, tc[0], tc[1])
	}
	free, err := CompileSnowballDictionary(ctx, "turkish", "")
	if err != nil || free.words != nil {
		t.Fatal(free, err)
	}
	check(free, "biz", "biz")
	mixed, err := CompileSnowballDictionary(ctx, "TuRkIsH", "turkish")
	if err != nil || mixed.provider != d.provider || !mixed.Configured("TuRkIsH", "turkish") || mixed.Configured("turkish", "turkish") {
		t.Fatal(mixed, err)
	}
	simple, err := CompileSimpleDictionary(ctx, true, "turkish")
	if err != nil {
		t.Fatal(err)
	}
	other, err := CompileSnowballDictionary(ctx, "english", "turkish")
	if err != nil {
		t.Fatal(err)
	}
	if &d.words[0] != &mixed.words[0] || &d.words[0] != &simple.words[0] || &d.words[0] != &other.words[0] {
		t.Fatal("shared immutable resource backing")
	}
	check(other, "biz", "")
	for _, word := range stops {
		v, err := simple.Lexize(ctx, word)
		if err != nil || !v.Recognized || len(v.Lexemes) != 0 {
			t.Fatal(word, v, err)
		}
	}
	v, err := d.Lexize(ctx, "KITAPLAR")
	if err != nil {
		t.Fatal(err)
	}
	v.Lexemes[0].Text = "changed"
	check(d, "KITAPLAR", "kitap")
	for _, resource := range []string{"turkish_missing", "TURKISH", "../turkish"} {
		if bad, err := CompileSnowballDictionary(ctx, "turkish", resource); bad != nil || err == nil {
			t.Fatal(resource, bad, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	expired, closeExpired := context.WithDeadline(ctx, time.Unix(1, 0))
	defer closeExpired()
	for _, c := range []context.Context{canceled, expired} {
		if bad, err := CompileSnowballDictionary(c, "turkish", "turkish"); bad != nil || !errors.Is(err, c.Err()) {
			t.Fatal(bad, err)
		}
		if _, err := d.Lexize(c, "KITAPLAR"); !errors.Is(err, c.Err()) {
			t.Fatal(err)
		}
	}
	dictionaryStopwords.Lock()
	backing := dictionaryStopwords.words["turkish"]
	_, missing := dictionaryStopwords.words["turkish_missing"]
	dictionaryStopwords.Unlock()
	if missing || &backing[0] != &d.words[0] || !slices.Equal(backing, stops) {
		t.Fatal("failed compilation changed cache")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(d, "KITAPLAR", "kitap")
				check(d, "biz", "")
				check(free, "biz", "biz")
			}
		})
	}
	wg.Wait()
}
