package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSQueryFunctionsInspect(t *testing.T) {
	ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// Manual node-count example, then independently counted occurrences and
		// operators. Decorations contribute no nodes.
		for i, tc := range []struct {
			input any
			want  any
		}{
			{"(fat & rat) | cat", int64(5)}, {"a&a", int64(3)},
			{"!!a", int64(3)}, {"a:*AB <2> !b", int64(4)},
			{"", int64(0)}, {nil, nil},
		} {
			t.Run(fmt.Sprintf("count_%d", i), func(t *testing.T) {
				ftsQueryFunctionsValue(t, p, b, "numnode($1::tsquery)", "INT4", tc.want, tc.input)
			})
		}
		// clean_NOT treats an entire NOT subtree as TRUE, not negation parity.
		for i, tc := range []struct {
			input any
			want  any
		}{
			{"foo & !bar", "'foo'"}, {"", ""}, {nil, nil},
			{"!a", "T"}, {"!!a", "T"}, {"a|!b", "T"},
			{"a&(b|!c)", "'a'"}, {"(a|!b)&(c|d)", "'c' | 'd'"},
			{"a<2>!b", "'a'"}, {"!a<2>b", "'b'"},
			{"a<2>b", "'a' <2> 'b'"}, {"!(a|b)&c:*AB", "'c':*AB"},
		} {
			t.Run(fmt.Sprintf("tree_%d", i), func(t *testing.T) {
				ftsQueryFunctionsValue(t, p, b, "querytree($1::tsquery)", "TEXT", tc.want, tc.input)
			})
		}
	})
}

func TestReferenceFTSQueryFunctionsDirectRewrite(t *testing.T) {
	ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for i, tc := range []struct {
			input, target, substitute any
			want                      any
		}{
			{"a&b", "a", "foo|bar", "'b' & ( 'foo' | 'bar' )"},
			{"a&b", "b&a", "x", "'x'"},
			{"a<2>b", "a<2>b", "x", "'x'"},
			{"a<2>b", "b<2>a", "x", "'a' <2> 'b'"},
			{"a<2>b", "a<->b", "x", "'a' <2> 'b'"},
			{"a<->(b<->c)", "a<->b", "x", "'a' <-> ( 'b' <-> 'c' )"},
			{"a:*A", "a:D", "x", "'x'"}, {"abc", "a:*", "x", "'abc'"},
			{"a", "A", "x", "'a'"}, {"!a", "a", "!b", "!!'b'"},
			{"a", "a", "a|b", "'a' | 'b'"}, {"a&a", "a", "x", "'x' & 'x'"},
			{"5&(6|5)", "5", "", "'6'"}, {"!5", "5", "", ""},
			{"", "a", "x", ""}, {"d&c&b&a", "", "x", "'d' & 'c' & 'b' & 'a'"},
			{nil, "a", "b", nil}, {"a", nil, "b", nil}, {"a", "a", nil, nil},
		} {
			t.Run(fmt.Sprintf("rule_%02d", i), func(t *testing.T) {
				ftsQueryFunctionsValue(t, p, b, "ts_rewrite($1::tsquery,$2::tsquery,$3::tsquery)", "TSQUERY", tc.want, tc.input, tc.target, tc.substitute)
			})
		}
		t.Run("single_multiset_subset", func(t *testing.T) {
			ftsQueryFunctionsValue(t, p, b, "numnode(ts_rewrite('a&a&a&a'::tsquery,'a&a'::tsquery,'x'::tsquery))", "INT4", int64(5))
		})
	})
}

func TestReferenceFTSQueryFunctionsNormalization(t *testing.T) {
	// Independent node/operator counts supplemented by pinned PG18.6 text
	// goldens: legacy signed CRC/QTNBinary order, not lexical sorting.
	for i, tc := range []struct {
		input, target, substitute, want string
		nodes                           int64
	}{
		{"a&(b&c)", "c&a", "x", "'b' & 'x'", 3},
		{"a&b&c&d", "absent", "x", "'c' & 'b' & 'a' & 'd'", 7},
		{"d|c|b|a", "absent", "x", "'c' | 'b' | 'a' | 'd'", 7},
		{"a&(b|c)&!d", "absent", "x", "'a' & !'d' & ( 'b' | 'c' )", 8},
		{"é & 'space word' & Z & aa & a", "absent", "x", "'a' & 'space word' & 'Z' & 'aa' & 'é'", 9},
		{"a:A&a:*D&a:B", "absent", "x", "'a':A & 'a':*D & 'a':B", 5},
		{"(a|b)&(a|c)", "a&(b|c)", "x", "( 'b' | 'a' ) & ( 'c' | 'a' )", 7},
		{"a<->(b<->c)", "absent", "x", "'a' <-> ( 'b' <-> 'c' )", 5},
	} {
		t.Run(fmt.Sprintf("normalization_%d", i), func(t *testing.T) {
			ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				for _, prepared := range []bool{false, true} {
					query := "SELECT ts_rewrite($1::tsquery,$2::tsquery,$3::tsquery) AS rewritten,numnode(ts_rewrite($1::tsquery,$2::tsquery,$3::tsquery)) AS nodes"
					s := fixture.Select(query, []string{"rewritten", "nodes"}, []string{"TSQUERY", "INT4"}, [][]any{{tc.want, tc.nodes}}, tc.input, tc.target, tc.substitute)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			})
		})
	}
}
