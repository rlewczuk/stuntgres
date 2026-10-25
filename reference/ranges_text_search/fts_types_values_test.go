package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSTypesValues(t *testing.T) {
	ftsTypesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_values(id integer, v tsvector, q tsquery)", 0))
		run(fixture.Exec("INSERT INTO fts_values VALUES(1,'a:3B,1D,3A a:2c','fat & (rat | cat)'),(2,NULL,NULL)", 2))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO fts_values VALUES($1,$2,$3)", 1, int64(3), []byte("b:16384D"), "a <1> b:*a")
			if prepared {
				s.Args[0] = int64(4)
				s.Prepared = true
			}
			run(s)
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT id,v,q FROM fts_values ORDER BY id", []string{"id", "v", "q"}, []string{"INT4", "TSVECTOR", "TSQUERY"}, [][]any{
				{int64(1), "'a':1,2C,3A", "'fat' & ( 'rat' | 'cat' )"},
				{int64(2), nil, nil},
				{int64(3), "'b':16383", "'a' <-> 'b':*A"},
				{int64(4), "'b':16383", "'a' <-> 'b':*A"},
			})
			s.Prepared = prepared
			run(s)
		}
		for _, tc := range []struct{ input, want string }{
			{"a fat cat sat on a mat and ate a fat rat", "'a' 'and' 'ate' 'cat' 'fat' 'mat' 'on' 'rat' 'sat'"},
			{"The Fat Rats", "'Fat' 'Rats' 'The'"},
			{"b aa a é A 'space word'", "'A' 'a' 'aa' 'b' 'space word' 'é'"},
			{"'a'b a'c 'x\\\\y' 'a''q'", "'a' 'a''c' 'a''q' 'b' 'x\\\\y'"},
			{"a a:3c a:1d", "'a':1,3C"}, {"", ""}, {" \t\n", ""},
		} {
			for _, expression := range []string{
				ftsTypesQuote(tc.input) + "::tsvector",
				"TSVECTOR " + ftsTypesQuote(tc.input),
				"pg_catalog.tsvector " + ftsTypesQuote(tc.input),
				"CAST(" + ftsTypesQuote(tc.input) + " AS tsvector)",
			} {
				s := fixture.Select("SELECT "+expression+" AS v", []string{"v"}, []string{"TSVECTOR"}, [][]any{{tc.want}})
				run(s)
				run(fixture.Prepared(s))
			}
		}
	})
}
