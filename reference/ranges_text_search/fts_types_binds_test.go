package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSTypesBinds(t *testing.T) {
	ftsTypesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_binds(id integer,v tsvector,q tsquery)", 0))
		query := "INSERT INTO fts_binds VALUES($1,$2,$3)"
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
		vector := []byte("x:3A,1D")
		queryBytes := []byte("x <1> y:*")
		inputs := [][]any{{int64(1), "a:2c a:1", "a & b"}, {int64(2), vector, queryBytes}, {int64(3), nil, nil}, {int64(4), []byte(nil), []byte(nil)}, {int64(5), []byte{}, []byte{}}}
		for _, args := range inputs {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, args...))
		}
		vector[0] = 'z'
		queryBytes[0] = 'z'
		run(fixture.Exec(query, 1, int64(6), "The Fat Rats", "!!b"))
		run(fixture.Prepared(fixture.Exec(query, 1, int64(7), []byte("b:16384D"), []byte("a:*ac"))))
		want := fixture.Select("SELECT id,v,q FROM fts_binds ORDER BY id", []string{"id", "v", "q"}, []string{"INT4", "TSVECTOR", "TSQUERY"}, [][]any{
			{int64(1), "'a':1,2C", "'a' & 'b'"}, {int64(2), "'x':1,3A", "'x' <-> 'y':*"}, {int64(3), nil, nil}, {int64(4), nil, nil}, {int64(5), "", ""}, {int64(6), "'Fat' 'Rats' 'The'", "!!'b'"}, {int64(7), "'b':16383", "'a':*AC"},
		})
		run(want)
		run(fixture.Prepared(want))
		for _, tc := range []struct{ typ, input, want string }{{"tsvector", "b a a", "'a' 'b'"}, {"tsquery", "a:*ac", "'a':*AC"}} {
			q := "SELECT $1::" + tc.typ + " AS first,$1::" + tc.typ + " AS again"
			s, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, arg := range []any{tc.input, []byte(tc.input), nil} {
				rows := [][]any{{tc.want, tc.want}}
				if arg == nil {
					rows = [][]any{{nil, nil}}
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: s, SQL: q}, b.Recorder, fixture.Select(q, []string{"first", "again"}, []string{map[string]string{"tsvector": "TSVECTOR", "tsquery": "TSQUERY"}[tc.typ], map[string]string{"tsvector": "TSVECTOR", "tsquery": "TSQUERY"}[tc.typ]}, rows, arg))
			}
			s.Close()
		}
	})
}
