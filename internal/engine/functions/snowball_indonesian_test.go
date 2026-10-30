package functions

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// Literal release-source witnesses; menulis was adjudicated as ulis by the
// pinned PostgreSQL 18.6 oracle. Never derive expectations from kernel tables.
var indonesianUnitMorphology = [][2]string{
	{"menyapu", "sapu"}, {"membaca", "baca"}, {"memakai", "paka"},
	{"menulis", "ulis"}, {"mengambil", "ambil"}, {"penyapu", "sapu"}, {"pemakai", "pakai"},
	{"belajar", "ajar"}, {"pelajar", "ajar"}, {"pelajaran", "ajar"}, {"belajarkan", "ajar"},
	{"bekerja", "kerja"}, {"perbaikan", "baik"}, {"peledakan", "ledak"},
	{"bukulah", "buku"}, {"bukukah", "buku"}, {"bukupun", "buku"}, {"bukuku", "buku"},
	{"bukumu", "buku"}, {"bukunya", "buku"}, {"bukunyalah", "buku"},
	{"buku", "buku"}, {"ai", "ai"}, {"aai", "aa"},
	{"aaakan", "aaa"}, {"aaaan", "aaa"}, {"aaai", "aaa"},
	{"memaaa", "paaa"}, {"membaaa", "baaa"}, {"menyaaa", "saaa"}, {"menybaaa", "ybaaa"},
	{"penyaaa", "saaa"}, {"penybaaa", "ybaaa"},
	{"diaaaan", "aaaan"}, {"diaaakan", "aaa"}, {"keaaakan", "aaak"}, {"peraaakan", "aaak"}, {"beraaai", "aaai"},
	{"diberaaakan", "aaa"}, {"diberaaaan", "beraaaan"}, {"diberaaa", "beraaa"},
	{"aaakahlah", "aaakah"}, {"aaakunyalah", "aaaku"},
	{"televisi", "televisi"}, {"organisasi", "organisasi"}, {"komunikasi", "komunikasi"}, {"aaasi", "aaasi"},
	{"dan", "dan"}, {"yang", "yang"}, {"dengan", "dengan"}, {"adalah", "ada"},
	{"mÉnyapu", "mÉnyapu"}, {"áákan", "áákan"}, {"áákan", "áá"}, {"bukulah!", "bukulah!"},
}

func TestSnowballIndonesianRules(t *testing.T) {
	for _, tc := range indonesianUnitMorphology {
		if got := indonesianStem(tc[0]); got != tc[1] {
			t.Errorf("%q -> %q, want %q", tc[0], got, tc[1])
		}
	}
	// Cover every released prefix entry, including short me/pe, exact-match
	// special prefixes, and UTF8 nonvowel stepping in the KER predicate.
	for _, tc := range []struct {
		input, want string
		class       int
		matched     bool
	}{
		{"diaaa", "aaa", 1, true}, {"keaaa", "aaa", 3, true},
		{"meaaa", "aaa", 1, true}, {"memaaa", "paaa", 1, true}, {"membaaa", "baaa", 1, true},
		{"menaaa", "aaa", 1, true}, {"mengaaa", "aaa", 1, true}, {"menyaaa", "saaa", 1, true},
		{"menybaaa", "ybaaa", 1, true}, {"pemaaa", "paaa", 3, true}, {"pembaaa", "baaa", 3, true},
		{"penaaa", "aaa", 3, true}, {"pengaaa", "aaa", 3, true}, {"penyaaa", "saaa", 3, true},
		{"penybaaa", "ybaaa", 3, true}, {"teraaa", "aaa", 1, true}, {"aaa", "aaa", 0, false},
	} {
		got, class, matched := indonesianFirstPrefix(tc.input)
		if got != tc.want || class != tc.class || matched != tc.matched {
			t.Error("first prefix", tc, got, class, matched)
		}
	}
	for _, tc := range []struct {
		input, want string
		class       int
		matched     bool
	}{
		{"peraaa", "aaa", 2, true}, {"peaaa", "aaa", 2, true}, {"pelajar", "ajar", 0, true},
		{"beraaa", "aaa", 4, true}, {"belajar", "ajar", 4, true}, {"bekerja", "kerja", 4, true},
		{"beéeraaa", "éeraaa", 4, true}, {"beaeraaa", "beaeraaa", 0, false}, {"be", "be", 0, false},
	} {
		got, class, matched := indonesianSecondPrefix(tc.input)
		if got != tc.want || class != tc.class || matched != tc.matched {
			t.Error("second prefix", tc, got, class, matched)
		}
	}
	for _, tc := range []struct {
		input, want string
		class       int
	}{
		{"aaakan", "aaa", 0}, {"aaakan", "aaa", 1}, {"aaakan", "aaak", 2},
		{"aaakan", "aaak", 3}, {"aaakan", "aaa", 4},
		{"aaaan", "aaaan", 1}, {"aaai", "aaa", 2}, {"aaai", "aaai", 3},
		{"aaai", "aaai", 4}, {"aaasi", "aaasi", 0},
	} {
		got, removed := indonesianSuffix(tc.input, tc.class)
		if got != tc.want || removed != (tc.input != tc.want) {
			t.Error("suffix predicate/fallback", tc, got, removed)
		}
	}
}

func TestSnowballIndonesianDictionary(t *testing.T) {
	ctx := t.Context()
	stock, err := DefaultIndonesianSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := CompileSnowballDictionary(ctx, "indonesian", "english")
	if err != nil {
		t.Fatal(err)
	}
	english, err := DefaultSnowballDictionary()
	if err != nil || len(stopped.words) != 127 || &stopped.words[0] != &english.words[0] || stopped.provider == english.provider {
		t.Fatal("explicit stoplist backing/provider separation", err)
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
				t.Error("flags", lexeme)
			}
		}
	}
	for _, tc := range indonesianUnitMorphology {
		check(stock, tc[0], tc[1])
	}
	for _, tc := range [][2]string{
		{"", ""}, {"MENYAPU", "sapu"}, {"MÉNYAPU", "mÉnyapu"}, {"THE", "the"}, {"RATS", "rats"},
		{strings.Repeat("a", 997) + "kan", strings.Repeat("a", 997)},
		{strings.Repeat("A", 998) + "kan", strings.Repeat("a", 998) + "kan"},
		{strings.Repeat("é", 498) + "akan", strings.Repeat("é", 498) + "akan"},
		{strings.Repeat("a", 995) + "ékan", strings.Repeat("a", 995) + "é"},
		{strings.Repeat("A", 996) + "ékan", strings.Repeat("a", 996) + "ékan"},
	} {
		check(stock, tc[0], tc[1])
	}
	check(stopped, "THE", "")
	check(stopped, "MENYAPU", "sapu")
	check(stopped, "RATS", "rats")
	check(english, "RATS", "rat")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := CompileSnowballDictionary(canceled, "indonesian", ""); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled compilation", d, err)
	}
	if _, err := stock.Lexize(canceled, "MENYAPU"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, resource := range []string{"indonesian", "../indonesian", "Indonesian"} {
		if d, err := CompileSnowballDictionary(ctx, "indonesian", resource); d != nil || err == nil {
			t.Fatal("invented stock stoplist or invalid resource", resource, d, err)
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(stock, "MENYAPU", "sapu")
				check(stock, "diberaaakan", "aaa")
				check(stock, "televisi", "televisi")
				check(stopped, "THE", "")
			}
		})
	}
	wg.Wait()
}

func TestSnowballIndonesianLanguageProvider(t *testing.T) {
	stock, err := DefaultIndonesianSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DefaultIndonesianSnowballDictionary()
	if err != nil || again != stock || !stock.Configured("indonesian", "") || stock.words != nil || stock.Filtering() {
		t.Fatal("shared language-only bootstrap provider", err)
	}
	mixed, err := CompileSnowballDictionary(t.Context(), "InDoNeSiAn", "")
	if err != nil || mixed.provider != stock.provider || !mixed.Configured("InDoNeSiAn", "") || mixed.Configured("indonesian", "") {
		t.Fatal("spelling is separate from registered authority", err)
	}
	for i := range snowballLanguages {
		p := &snowballLanguages[i]
		if (p == stock.provider) != (p.name == "indonesian") {
			t.Fatal("provider identity alias", p.name)
		}
	}
	for _, language := range []string{"", " indonesian", "indonesian ", "indonesian_bogus", "0", "true"} {
		if d, err := CompileSnowballDictionary(t.Context(), language, ""); d != nil || err == nil {
			t.Fatal(language, d, err)
		}
	}
	fake := *stock
	fake.provider = &snowballLanguage{name: "indonesian", stem: indonesianStem}
	if fake.Configured("indonesian", "") || (*SnowballDictionary)(nil).Configured("indonesian", "") {
		t.Fatal("labels/output forged registered provider authority")
	}
}
