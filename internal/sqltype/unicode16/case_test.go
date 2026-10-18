package unicode16

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Literal expectations are Unicode16 UCD mappings and PostgreSQL18 builtin
// rules, independent of the generated tables under test.
func TestUnicode16CaseMappings(t *testing.T) {
	for _, tc := range []struct {
		input               string
		op                  CaseOperation
		ascii, simple, full string
	}{
		{"Straße", Lower, "straße", "straße", "straße"},
		{"Straße", Upper, "STRAßE", "STRAßE", "STRASSE"},
		{"Straße", Fold, "straße", "straße", "strasse"},
		{"Straße", Initcap, "StraßE", "Straße", "Straße"},
		{"İΣ", Lower, "İΣ", "iσ", "i\u0307ς"},
		{"İΣ", Fold, "İΣ", "İσ", "i\u0307σ"},
		{"İΣ", Initcap, "İΣ", "İσ", "İς"},
		{"ẞ", Fold, "ẞ", "ß", "ss"},
		{"ﬃ", Upper, "ﬃ", "ﬃ", "FFI"},
		{"ﬃ", Fold, "ﬃ", "ﬃ", "ffi"},
		{"ﬃOO", Initcap, "ﬃOo", "ﬃoo", "Ffioo"},
		{"ȺȺȺ", Lower, "ȺȺȺ", "ⱥⱥⱥ", "ⱥⱥⱥ"},
		{"ⱥⱥⱥ", Upper, "ⱥⱥⱥ", "ȺȺȺ", "ȺȺȺ"},
		{"ȺȺȺ", Initcap, "ȺȺȺ", "Ⱥⱥⱥ", "Ⱥⱥⱥ"},
		{"Σσς", Fold, "Σσς", "σσσ", "σσσ"},
		{"\uAB70", Fold, "\uAB70", "\u13A0", "\u13A0"},
		{"\u01F0", Fold, "\u01F0", "\u01F0", "j\u030C"},
		{"ǆxxǅ", Initcap, "ǆXxǅ", "Ǆxxǆ", "ǅxxǆ"},
		{"ßSS", Initcap, "ßSs", "ßss", "Ssss"},
		{"42FOO_bar-bAZ O'NEILL", Initcap, "42foo_Bar-Baz O'Neill", "42foo_Bar-Baz O'Neill", "42foo_Bar-Baz O'Neill"},
		{"élan ÉCOLE", Initcap, "éLan ÉCole", "Élan École", "Élan École"},
		{"Λλ 1a １a", Initcap, "Λλ 1a １A", "Λλ 1a １A", "Λλ 1a １a"},
		{"a\u0301BC", Initcap, "A\u0301Bc", "A\u0301Bc", "A\u0301Bc"},
		{"a\u0345BC", Initcap, "A\u0345Bc", "A\u0345bc", "A\u0345bc"},
		{"ΑΣ", Lower, "ΑΣ", "ασ", "ας"},
		{"ΑΣΑ", Lower, "ΑΣΑ", "ασα", "ασα"},
		{"Σ", Lower, "Σ", "σ", "σ"},
		{"ΑΣ", Fold, "ΑΣ", "ασ", "ασ"},
		{"Α\u0345Σ\u0345", Lower, "Α\u0345Σ\u0345", "α\u0345σ\u0345", "α\u0345ς\u0345"},
		{"\u0345Σ", Lower, "\u0345Σ", "\u0345σ", "\u0345σ"},
		{"\u0345Σ", Initcap, "\u0345Σ", "Ισ", "Ισ"},
		{"\u0345Σ", Fold, "\u0345Σ", "ισ", "ισ"},
		{"ΑΣ\u0345Α", Lower, "ΑΣ\u0345Α", "ασ\u0345α", "ασ\u0345α"},
		{"\u1C89\uA7CB\U00010D50", Lower, "\u1C89\uA7CB\U00010D50", "\u1C8A\u0264\U00010D70", "\u1C8A\u0264\U00010D70"},
		{"\u1C8A\u0264\U00010D70", Upper, "\u1C8A\u0264\U00010D70", "\u1C89\uA7CB\U00010D50", "\u1C89\uA7CB\U00010D50"},
		{"", Lower, "", "", ""},
	} {
		for mode, want := range []string{tc.ascii, tc.simple, tc.full} {
			got, err := ConvertCase(context.Background(), tc.input, tc.op, CaseMode(mode))
			if err != nil || got != want {
				t.Fatalf("mode=%d operation=%d input=%q: got %q want %q error=%v", mode, tc.op, tc.input, got, want, err)
			}
		}
	}
}

func TestUnicode16CaseProperties(t *testing.T) {
	for _, tc := range []struct {
		r                                     rune
		alphabetic, cased, ignorable, decimal bool
	}{
		{'A', true, true, false, false}, {'1', false, false, false, true}, {'１', false, false, false, true},
		{0x0301, false, false, true, false}, {0x0345, true, true, true, false}, {'Σ', true, true, false, false},
		{0x10D50, true, true, false, false}, {0x10D40, false, false, false, true}, {'_', false, false, false, false},
	} {
		if caseProperty(tc.r, alphabeticRanges[:]) != tc.alphabetic || caseProperty(tc.r, casedRanges[:]) != tc.cased || caseProperty(tc.r, caseIgnorableRanges[:]) != tc.ignorable || caseProperty(tc.r, decimalRanges[:]) != tc.decimal {
			t.Fatalf("case properties U+%04X", tc.r)
		}
	}
}

func TestUnicode16CaseSigmaLookahead(t *testing.T) {
	marks := strings.Repeat("\u0345", 8192)
	for _, tc := range []struct{ input, want string }{
		{"ΑΣ" + marks, "ας" + marks}, {"ΑΣ" + marks + "Α", "ασ" + marks + "α"},
		{marks + "Σ", marks + "σ"}, {marks + "Σ" + marks, marks + "σ" + marks},
		{marks + "Σ" + marks + "Α", marks + "σ" + marks + "α"},
		{"1" + marks + "Σ", "1" + marks + "σ"},
	} {
		got, err := ConvertCase(context.Background(), tc.input, Lower, Full)
		if err != nil || got != tc.want {
			t.Fatal("original-input Final_Sigma", err)
		}
	}
	// PostgreSQL 18.6 needs a cased nearest non-ignorable predecessor; an
	// all-ignorable prefix is not one. The emitted title expansion must not
	// replace the original-input context.
	for _, tc := range []struct{ input, lower, initcap string }{
		{"\u0345Σ", "\u0345σ", "Ισ"},
		{"\u0301Σ", "\u0301σ", "\u0301Σ"},
		{"\u0301\u0345Σ", "\u0301\u0345σ", "\u0301Ισ"},
		{"\u0345Σ\u0345", "\u0345σ\u0345", "Ισ\u0345"},
		{"Σ", "σ", "Σ"}, {"1Σ", "1σ", "1σ"},
		{"1\u0345Σ", "1\u0345σ", "1\u0345σ"},
		{"\u0345ΣΑ", "\u0345σα", "Ισα"},
		{"\u0301ΣΑ", "\u0301σα", "\u0301Σα"},
		{"İΣ", "i\u0307ς", "İς"}, {"", "", ""},
	} {
		for _, op := range []CaseOperation{Lower, Initcap} {
			want := tc.lower
			if op == Initcap {
				want = tc.initcap
			}
			got, err := ConvertCase(context.Background(), tc.input, op, Full)
			if err != nil || got != want {
				t.Fatalf("original context operation=%d input=%q: %q want %q error=%v", op, tc.input, got, want, err)
			}
		}
	}
}

func TestUnicode16CaseCancellationAndBorrowing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for mode := ASCII; mode <= Full; mode++ {
		for op := Lower; op <= Initcap; op++ {
			if got, err := ConvertCase(ctx, "", op, mode); got != "" || !errors.Is(err, context.Canceled) {
				t.Fatal("entry cancellation", got, err)
			}
			for _, source := range []string{strings.Repeat(" ", 8192), strings.Repeat("ﬃ", 8192), "ΑΣ" + strings.Repeat("\u0345", 8192) + "Α", strings.Repeat("\u0345", 8192) + "Σ", "\u0345Σ" + strings.Repeat("\u0345", 8192)} {
				probe := &cancellationProbe{Context: context.Background(), remaining: 3}
				if got, err := ConvertCase(probe, source, op, mode); got != "" || !errors.Is(err, context.Canceled) {
					t.Fatal("bounded cancellation", mode, op, err)
				}
			}
			for _, source := range []string{"", "🙂 123"} {
				allocations := testing.AllocsPerRun(100, func() {
					got, err := ConvertCase(context.Background(), source, op, mode)
					if err != nil || got != source {
						panic("unchanged case input")
					}
				})
				if allocations != 0 {
					t.Fatal("unchanged case allocates", mode, op, allocations)
				}
			}
		}
	}
}
