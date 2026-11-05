package xml

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
)

func TestXSLTCollationLanguages(t *testing.T) {
	// xsltNewLocale's released tag grammar and pinned locale inventory, not
	// Go language matching or availability on the machine running this test.
	for _, lang := range []string{"en", "EN", "en-US", "En-uS", "en-GB", "en-Latn", "en-ABCDEFGH"} {
		t.Run(lang, func(t *testing.T) {
			xsltAssertCollation(t, lang, "", "a", "A", -1)
		})
	}
	for _, lang := range []string{"", "de", "de-DE", "eo", "eng", "C", "C.utf8", "POSIX", "en_US", "en-", "en-US-extra", "en-123", "en-ABCDEFGHI", "englishxx", "é", " en", "en "} {
		t.Run("bytes_"+lang, func(t *testing.T) {
			xsltAssertCollation(t, lang, "", "a", "A", 1)
		})
	}
}

func TestXSLTCollationSortOrders(t *testing.T) {
	// The English order follows the retained ISO14651 definitions: hyphen
	// is ignored until the positional fourth level, accents precede case,
	// and ae is the primary expansion of the ligature. Unavailable locales
	// instead compare UTF-8 bytes. Both complete orders were independently
	// qualified by the pinned PostgreSQL sort probes (report16).
	input := []xsltCollationSortKey{
		{"a", "a"}, {"A", "A"}, {"á", "acute"}, {"ä", "dia"},
		{"aa", "aa"}, {"a-", "a-dash"}, {"-a", "dash-a"},
		{"æ", "ae"}, {"b", "b"}, {"B", "B"}, {"z", "z"},
	}
	const englishOrder = "dash-a|a|a-dash|A|acute|dia|aa|ae|b|B|z|"
	const byteOrder = "dash-a|A|B|a|a-dash|aa|b|z|acute|dia|ae|"
	for _, tc := range []struct{ lang, want string }{
		{"en", englishOrder}, {"en-US", englishOrder}, {"en-GB", englishOrder},
		{"", byteOrder}, {"de", byteOrder}, {"eo", byteOrder}, {"en_US", byteOrder},
	} {
		for _, caseOrder := range []string{"", "upper-first", "lower-first"} {
			t.Run(tc.lang+"/"+caseOrder, func(t *testing.T) {
				xsltAssertCollationSort(t, tc.lang, caseOrder, input, tc.want)
			})
		}
	}
}

func TestXSLTCollationSortBoundaries(t *testing.T) {
	// These are complete, independently specified orders, not a native
	// output recorder. In particular, canonical equivalents need not tie
	// at level four, undefined characters have no scalar tiebreak, and
	// contracted spellings really tie rather than merely staying adjacent.
	for _, tc := range []struct {
		name  string
		input []xsltCollationSortKey
		want  string
	}{
		{
			"canonical",
			[]xsltCollationSortKey{{"é", "composed"}, {"e\u0301", "decomposed"}, {"e", "plain"}, {"É", "upper"}},
			"plain|decomposed|composed|upper|",
		},
		{
			"undefined",
			[]xsltCollationSortKey{{"\u0379", "b"}, {"\u0378", "a"}, {"\U0010ffff", "max"}, {"😀", "smile"}},
			"b|a|max|smile|",
		},
		{
			"contractions",
			[]xsltCollationSortKey{{"l·", "two-l"}, {"ŀ", "one-l"}, {"И\u0306", "two-cyr"}, {"Й", "one-cyr"}},
			"two-l|one-l|two-cyr|one-cyr|",
		},
		{
			"contraction_ties_reordered",
			[]xsltCollationSortKey{{"Й", "one-cyr"}, {"ŀ", "one-l"}, {"И\u0306", "two-cyr"}, {"l·", "two-l"}},
			"one-l|two-l|one-cyr|two-cyr|",
		},
	} {
		for _, lang := range []string{"en", "en-US", "en-GB"} {
			for _, caseOrder := range []string{"", "upper-first", "lower-first"} {
				t.Run(tc.name+"/"+lang+"/"+caseOrder, func(t *testing.T) {
					xsltAssertCollationSort(t, lang, caseOrder, tc.input, tc.want)
				})
			}
		}
	}
}

func TestXSLTCollationLevels(t *testing.T) {
	// Inspect the genuine iterator's level streams through relations
	// specified by the retained source weights, never captured byte goldens.
	// These distinguish a contraction/expansion interpreter from a rune map
	// even where a complete stable sort alone could leave input unchanged.
	t.Run("contractions", func(t *testing.T) {
		for _, pair := range [][2]string{{"l·", "ŀ"}, {"l\u0387", "ŀ"}, {"И\u0306", "Й"}} {
			for level := range 4 {
				xsltAssertCollationLevel(t, level, pair[0], pair[1], 0)
			}
		}
		// Cyrillic short I contracts to a different primary from plain I;
		// treating its combining breve as primary-ignorable is not enough.
		xsltAssertCollationLevel(t, 0, "И\u0306", "И", 1)
	})
	t.Run("expansion", func(t *testing.T) {
		// The ligature emits both a and e primary weights, not just a.
		xsltAssertCollationLevel(t, 0, "æ", "ae", 0)
		xsltAssertCollationLevel(t, 0, "æ", "ad", 1)
		xsltAssertCollationLevel(t, 0, "æ", "af", -1)
	})
	t.Run("canonical_fourth_level", func(t *testing.T) {
		for level := range 3 {
			xsltAssertCollationLevel(t, level, "e\u0301", "é", 0)
		}
		xsltAssertCollationLevel(t, 3, "e\u0301", "é", -1)
	})
	t.Run("backward_secondary_segments", func(t *testing.T) {
		// AIGUT < GRAVE, but SPECIAL combining marks run backwards.
		// LATIN runs forward, and a following Latin letter terminates
		// the backward segment rather than joining a whole-string reversal.
		xsltAssertCollationLevel(t, 1, "\u0301\u0300", "\u0300\u0301", 1)
		xsltAssertCollationLevel(t, 1, "áà", "àá", -1)
		xsltAssertCollationLevel(t, 1, "a\u0301\u0300b", "a\u0300\u0301b", 1)
		xsltAssertCollationLevel(t, 1, "a\u0301b\u0300", "a\u0300b\u0301", -1)
		// Cross the iterator's inline storage boundary without changing
		// the independently ordered final reversed weight.
		xsltAssertCollationLevel(t, 1, strings.Repeat("\u0301", 64)+"\u0300", strings.Repeat("\u0301", 64)+"\u0308", -1)
	})
	t.Run("positional_fourth_level", func(t *testing.T) {
		for level := range 3 {
			xsltAssertCollationLevel(t, level, "-a", "a-", 0)
		}
		xsltAssertCollationLevel(t, 3, "-a", "a-", -1)
	})
}

func TestXSLTCollationUnicode(t *testing.T) {
	// Expected order comes from the retained ISO14651 source definitions:
	// primary expansion precedes secondary, secondary precedes case, and
	// SPECIAL secondary segments run backwards independently of LATIN.
	cases := []struct {
		name, a, b string
		want       int
	}{
		{"empty", "", "", 0},
		{"empty_before_ignorable_primary", "", "😀", -1},
		{"case", "a", "A", -1},
		{"secondary_before_case", "A", "á", -1},
		{"whole_primary_before_accent", "á", "aa", -1},
		{"ligature_expansion_lower", "ad", "æ", -1},
		{"ligature_expansion_upper", "æ", "af", -1},
		{"sharp_s_expansion", "ß", "st", -1},
		{"combining_backward", "\u0301\u0300", "\u0300\u0301", 1},
		{"mixed_backward_segment", "a\u0301\u0300b", "a\u0300\u0301b", 1},
		{"long_backward_segment", strings.Repeat("\u0301", 64) + "\u0300", strings.Repeat("\u0301", 64) + "\u0308", -1},
		{"cyrillic_contraction", "И\u0306", "Й", 0},
		{"latin_contraction", "l·", "ŀ", 0},
		{"alternative_latin_contraction", "l\u0387", "ŀ", 0},
		{"contraction_tail", "l·a", "ŀb", -1},
		{"noncanonical_level_four", "e\u0301", "é", -1},
		{"supplementary_fourth_level", "😀", "😁", -1},
		{"han_range", "一", "丁", -1},
		{"unassigned_same_undefined_weight", "\u0378", "\u0379", 0},
		{"max_scalar_is_not_replacement", "\U0010ffff", "\U0010ffff", 0},
		{"one_byte_boundary", "\u007f", "\u007f", 0},
		{"two_byte_boundary", "\u0080\u07ff", "\u0080\u07ff", 0},
		{"three_byte_boundary", "\u0800\ud7ff\ue000\ufffd", "\u0800\ud7ff\ue000\ufffd", 0},
		{"four_byte_boundary", "\U00010000\U0010ffff", "\U00010000\U0010ffff", 0},
		{"prefix", "a", "a-", -1},
		{"embedded_nul_C_boundary", "a\x00z", "a\x00b", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xsltAssertCollation(t, "en-US", "", tc.a, tc.b, tc.want)
			xsltAssertCollation(t, "en-US", "", tc.b, tc.a, -tc.want)
		})
	}
}

func TestXSLTCollationCaseOrder(t *testing.T) {
	// POSIX libxslt 1.1.35 compiles lower_first but does not consume it in
	// its sorter. This is released implementation behavior, not a claim
	// that XSLT's upper-first generally means lower-first.
	for _, order := range []string{"", "upper-first", "lower-first"} {
		xsltAssertCollation(t, "en-US", order, "a", "A", -1)
		xsltAssertCollation(t, "", order, "a", "A", 1)
	}
}

func TestXSLTCollationPositions(t *testing.T) {
	for _, tc := range []struct {
		value uint32
		want  string
	}{
		{1, "\x01"},
		{0x7f, "\x7f"},
		{0x80, "\xc2\x80"},
		{0x7ff, "\xdf\xbf"},
		{0x800, "\xe0\xa0\x80"},
		{0xffff, "\xef\xbf\xbf"},
		{0x10000, "\xf0\x90\x80\x80"},
		{0x1fffff, "\xf7\xbf\xbf\xbf"},
		{0x200000, "\xf8\x88\x80\x80\x80"},
		{0x3ffffff, "\xfb\xbf\xbf\xbf\xbf"},
		{0x4000000, "\xfc\x84\x80\x80\x80\x80"},
		{0x7fffffff, "\xfd\xbf\xbf\xbf\xbf\xbf"},
	} {
		var buf [6]byte
		n := xsltCollationPosition(&buf, tc.value)
		if got := string(buf[:n]); got != tc.want {
			t.Fatalf("position %x: got %x, want %x", tc.value, got, tc.want)
		}
	}
}

func TestXSLTCollationCancellation(t *testing.T) {
	pre, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := compareXSLTText(pre, "en", "", "a", "b"); got != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("entry cancellation", got, err)
	}
	for _, tc := range []struct{ lang, text string }{
		{"", strings.Repeat("a", 8192)},
		{"en", strings.Repeat("a", 8192)},
		{"en", strings.Repeat("\u0301", 8192)},
		{"en", strings.Repeat("一", 8192)},
	} {
		base, stop := context.WithCancel(context.Background())
		ctx := &xsltCollationCancelContext{Context: base, cancel: stop, remaining: 12}
		got, err := compareXSLTText(ctx, tc.lang, "", tc.text, tc.text)
		stop()
		if got != 0 || !errors.Is(err, context.Canceled) || ctx.remaining != 0 {
			t.Fatal("in-flight cancellation was replaced or work was not entered", tc.lang, got, err, ctx.remaining)
		}
	}
	xsltAssertCollation(t, "en", "", "æ", "b", -1)
}

type xsltCollationCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (ctx *xsltCollationCancelContext) Err() error {
	if ctx.remaining > 0 {
		ctx.remaining--
		if ctx.remaining == 0 {
			ctx.cancel()
		}
	}
	return ctx.Context.Err()
}

func xsltAssertCollation(t *testing.T, lang, caseOrder, a, b string, want int) {
	t.Helper()
	got, err := compareXSLTText(context.Background(), lang, caseOrder, a, b)
	if err != nil || got != want {
		t.Fatalf("lang=%q case-order=%q compare(%q,%q) = %d,%v; want %d", lang, caseOrder, a, b, got, err, want)
	}
}

type xsltCollationSortKey struct {
	value, label string
}

func xsltAssertCollationSort(t *testing.T, lang, caseOrder string, input []xsltCollationSortKey, want string) {
	t.Helper()
	keys := append([]xsltCollationSortKey(nil), input...)
	sort.SliceStable(keys, func(i, j int) bool {
		result, err := compareXSLTText(context.Background(), lang, caseOrder, keys[i].value, keys[j].value)
		if err != nil {
			t.Fatal(err)
		}
		return result < 0
	})
	var got strings.Builder
	for _, key := range keys {
		got.WriteString(key.label)
		got.WriteByte('|')
	}
	if got.String() != want {
		t.Fatalf("lang=%q case-order=%q order=%q; want %q", lang, caseOrder, got.String(), want)
	}
}

func xsltAssertCollationLevel(t *testing.T, level int, a, b string, want int) {
	t.Helper()
	stream := func(text string) string {
		it := xsltCollationIterator{text: text}
		it.reset(level)
		var result strings.Builder
		for {
			v, ok, err := it.next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				return result.String()
			}
			result.WriteByte(v)
		}
	}
	if got := strings.Compare(stream(a), stream(b)); got != want {
		t.Fatalf("level=%d compare(%q,%q) = %d; want %d", level+1, a, b, got, want)
	}
}
