package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type ftsMatchCase struct {
	vector, query string
	want          bool
}

// Released tstypes.sql witnesses specify values independently of either backend.
// Both catalog-selected orders share raw typed input, never normalized text.
func ftsMatchTyped(t *testing.T, p *fixture.Pair, b fixture.Backend, cases []ftsMatchCase) {
	t.Helper()
	for _, reverse := range []bool{false, true} {
		expression := "$1::tsvector @@ $2::tsquery"
		if reverse {
			expression = "$2::tsquery @@ $1::tsvector"
		}
		query := "SELECT " + expression + " AS result"
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
			for i, tc := range cases {
				t.Run(fmt.Sprintf("reverse_%t/%02d", reverse, i), func(t *testing.T) {
					ftsOpsValue(t, p, b, expression, "BOOL", tc.want, tc.vector, tc.query)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"result"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.vector, tc.query))
				})
			}
			for _, tc := range []struct {
				vector, query, want any
			}{{"cat", "cat", true}, {"cat", "dog", false}, {nil, "cat", nil}, {"cat", "cat", true}} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"result"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.vector, tc.query))
			}
		}()
	}
}

func TestReferenceFTSMatchBoolean(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		ftsMatchTyped(t, p, b, []ftsMatchCase{
			{"fat cat rat", "cat & rat", true}, {"fat cat rat", "cat & cow", false},
			{"fat cat rat", "cat & !cow", true}, {"fat cat rat", "cat & !rat", false},
			{"cat", "cat | cow", true}, {"cat", "rat | cow", false},
			{"cat", "!!cat", true}, {"cat", "!(cat | rat)", false},
			{"cat", "cat | rat & cow", true}, {"cat", "(cat | rat) & cow", false},
			{"", "!foo", true}, {"cat", "", false}, {"", "", false},
			{"Rats", "rat", false}, {"cat", "Cat", false},
		})
	})
}

func TestReferenceFTSMatchPhrase(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		ftsMatchTyped(t, p, b, []ftsMatchCase{
			{"a:1 b:2", "a<0>b", false}, {"a:1 b:2", "a<1>b", true}, {"a:1 b:2", "a<2>b", false},
			{"a:1 b:3", "a<2>b", true}, {"a:1 b:3", "a<0>a:*", true},
			{"a:1 b:2 c:3", "a<->(b<->c)", true},
			{"q:1,3 x:2 y:4", "q<->(x&y)", false},
			{"q:1 x:2", "q<->(x|y<->z)", true}, {"q:1 y:2", "q<->(x|y<->z)", false},
			{"q:1 y:2 z:3", "q<->(x|y<->z)", true},
			{"x:1 y:2,4 q:3", "!x<->y", true}, {"x:1 y:2,4 q:3", "!(x<->y)", false},
			{"a b c", "a<->b", false}, {"a b c", "!(a<->b)", true},
			{"wa:1 wb c:2", "w:*<->c", false}, {"wa:1 wb c:2", "!(w:*<->c)", true},
			{"a:1 b c:2", "(a|b)<->c", false}, {"a:1 b c:2", "!((a|b)<->c)", true},
			{"a:16382 b:16383", "a<->b", true}, {"a:1 b:16383", "a<16384>b", false},
			// Intermediate ends use released position bits; widths stay wide.
			{"a:1 b:1", "(a<16382>!missing)<0>b", false},
			{"a:1 b:1", "(a<16383>!missing)<0>b", false},
			{"a:1 b:1", "(a<16384>!missing)<0>b", true},
			{"a:1 b:2", "(a<16384>!missing)<0>b", false},
			{"a:2 b:2", "(a<16384>!missing)<0>b", true},
			{"a:1 b:16383", "(a<16382>!missing)<0>b", true},
			// Computed failure width survives NOT, but failed OR children
			// and short-circuiting AND parents have separate width rules.
			{"a:1 b:2 c:1 d:2", "(!(a<2>b)&c)<->d", false},
			{"a:1 b:2 c:1 d:4", "(!(a<2>b)&c)<->d", true},
			{"c:1 d:2", "(!(a<2>b)&c)<->d", true},
			{"a:1 b:2 c:1 d:2", "(!(missing<2>b)&c)<->d", true},
			{"a:1 b:2 c:1 d:2", "(!(a<2>missing)&c)<->d", true},
			{"a:1 b:2 c:1 d:2", "((a<2>b)|c)<->d", true},
			{"a:1 b:2 c:1 d:2", "(c|(a<2>b))<->d", true},
			{"a:1 b:2 c:1 d:2", "(!((a<2>b)&missing)&c)<->d", true},
			{"a:1 b:2 c:1 d:2", "(!(!(a<2>b))|c)<->d", true},
			{"a:1 b:2 c:1 d:2", "(!(!(!(a<2>b)))&c)<->d", false},
			{"a:1 b:2 c:1 d:4", "(!(!(!(a<2>b)))&c)<->d", true},
			{"a:1 b:2 c:1 d:2", "((!(a<2>b)|c)&c)<->d", false},
			{"a:1 b:2 c:1 d:4", "((!(a<2>b)|c)&c)<->d", true},
		})
		for _, tc := range []struct {
			doc, query string
			want       bool
		}{
			{"q x q y", "q<->(x&y)", false}, {"q x", "q<->(x|y<->z)", true},
			{"q y", "q<->(x|y<->z)", false}, {"q y z", "q<->(x|y<->z)", true},
			{"x y q y", "!x<->y", true}, {"x y q y", "!(x<->y)", false},
		} {
			ftsOpsValue(t, p, b, "to_tsvector('simple',$1::text) @@ $2::tsquery", "BOOL", tc.want, tc.doc, tc.query)
			ftsOpsValue(t, p, b, "$2::tsquery @@ to_tsvector('simple',$1::text)", "BOOL", tc.want, tc.doc, tc.query)
		}
	})
}

func TestReferenceFTSMatchPrefixWeights(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		ftsMatchTyped(t, p, b, []ftsMatchCase{
			{"supernova", "super", false}, {"supernova", "super:*", true},
			{"supeanova supernova", "super:*", true}, {"asupernova", "super:*", false},
			{"wa:1D wb:2A", "w:*D & w:*A", true}, {"wa:1D wb:2A", "w:*D<->w:*A", true},
			{"wa:1A wb:2D", "w:*D<->w:*A", false}, {"wa:1A", "w:*D", false},
			{"wa", "w:*D", true}, {"w:1A wa:2D", "w:*D", true},
			{"wa:1 wb:1 c:2", "w:*<->c", true},
			{"a:1A b:2B c:3C d:4", "a:A & b:B & c:C & d:D", true},
			{"a:1A b:2B c:3C d:4", "a:B | b:C | c:D | d:A", false},
			{"a:1B", "a:AB", true}, {"a:1C", "a:AB", false},
			{"a:1C", "a:ACD", true}, {"a:1D", "a:ACD", true},
			{"a:1B", "a:ACD", false}, {"a", "a:A", true},
		})
	})
}

func TestReferenceFTSMatchNullEmpty(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, signature := range ftsMatchSignatures {
			for _, routine := range []bool{false, true} {
				expression := signature.expression(routine)
				for _, args := range [][]any{{nil, "cat"}, {"cat", nil}, {nil, nil}} {
					ftsOpsValue(t, p, b, expression, "BOOL", nil, args...)
				}
			}
		}
		ftsMatchTyped(t, p, b, []ftsMatchCase{{"", "!foo", true}, {"", "foo", false}, {"foo", "", false}, {"", "", false}})
	})
}
