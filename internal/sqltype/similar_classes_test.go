package sqltype

import (
	"context"
	"testing"
)

func TestSimilarClassCollationPolicy(t *testing.T) {
	for _, tc := range []struct {
		source, class   string
		c, compat, full bool
	}{
		{"é", "alpha", false, true, true}, {"٥", "digit", false, false, true}, {"+", "punct", true, true, false},
		{"\u00a0", "blank", false, false, false}, {"\u00a0", "space", false, true, true}, {"\u00a0", "graph", false, false, false}, {"\u00a0", "print", false, true, true},
		{"\u2028", "space", false, true, true}, {"\u2028", "graph", false, false, false}, {"\u2028", "print", false, false, false},
		{"\u200b", "space", false, false, false}, {"\u200b", "graph", false, true, true}, {"\u200b", "print", false, true, true},
		{"\u0301", "word", false, false, false}, {"_", "word", true, true, true},
		{"\u0080", "cntrl", true, true, true}, {"\u0085", "cntrl", true, true, true}, {"\u009f", "cntrl", true, true, true},
		{"é", "lower", false, true, true}, {"É", "upper", false, true, true}, {"\t", "print", false, false, false},
	} {
		for _, mode := range []struct {
			id   uint32
			want bool
		}{{950, tc.c}, {951, tc.c}, {100, tc.c}, {811, tc.compat}, {6411, tc.full}} {
			p := similarTestProgram(t, "[[:"+tc.class+":]]", "\\", mode.id)
			yes, _, _, err := similarTestMatch(p, context.Background(), tc.source)
			if err != nil || yes != mode.want {
				t.Fatal(tc, mode, yes, err)
			}
		}
	}
}
func TestSimilarClassNamedElements(t *testing.T) {
	for _, e := range pgRegexElementNames {
		p := similarTestProgram(t, "[[."+e.name+".]]", "\\", 950)
		yes, _, _, err := similarTestMatch(p, context.Background(), string(e.value))
		if err != nil || !yes {
			t.Fatal(e, yes, err)
		}
	}
	for _, tc := range []struct {
		pattern, source string
		want            bool
	}{
		{`[\dA-F]`, "B", true}, {`[^\D]`, "5", true}, {`[\D]`, "5", false},
		{`[\u0061-\u0063]`, "b", true}, {`[[.zero.]-[.nine.]]`, "5", true},
	} {
		p := similarTestProgram(t, tc.pattern, "\\", 950)
		yes, _, _, err := similarTestMatch(p, context.Background(), tc.source)
		if err != nil || yes != tc.want {
			t.Fatal(tc, yes, err)
		}
	}
}
