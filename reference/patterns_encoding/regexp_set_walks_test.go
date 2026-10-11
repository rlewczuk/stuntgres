package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func regexpSetWalks(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, c := range []struct {
		q            string
		names, types []string
		rows         [][]any
		args         []any
	}{
		{`SELECT r.s FROM regexp_split_to_table((SELECT $1::text),' ') r(s) ORDER BY r.s`, []string{"s"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}, []any{"b a"}},
		{`SELECT r.s FROM regexp_split_to_table((SELECT s FROM (VALUES('a b')) v(s)),' ') r(s) ORDER BY r.s`, []string{"s"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}, nil},
		{`SELECT v.id,r.s FROM (VALUES(1,'a b'),(2,'c')) v(id,s),regexp_split_to_table((SELECT v.s),' ') r(s) ORDER BY v.id,r.s`, []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}, {int64(2), "c"}}, nil},
		{`SELECT v.id,(SELECT (SELECT count(*) FROM regexp_split_to_table(v.s,' ') r(s))) AS n FROM (VALUES(1,'a b'),(2,'c')) v(id,s) ORDER BY v.id`, []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}, nil},
		{`SELECT v.id FROM (VALUES(1,'a'),(2,'x')) v(id,s) WHERE EXISTS(SELECT regexp_matches(v.s,'a')) ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}, nil},
		{`SELECT s FROM ROWS FROM(regexp_split_to_table($1,' '),regexp_matches($2,'a','g')) r(s,m) ORDER BY s NULLS LAST`, []string{"s"}, []string{"TEXT"}, [][]any{{"x"}, {"y"}, {nil}}, []any{"x y", "aaa"}},
		{`SELECT regexp_match(regexp_split_to_table('ab cd',' '),'(.)') AS m`, []string{"m"}, []string{"_TEXT"}, [][]any{{"{a}"}, {"{c}"}}, nil},
		{`SELECT regexp_replace(regexp_split_to_table('ab ac',' '),'a','X') AS s ORDER BY s`, []string{"s"}, []string{"TEXT"}, [][]any{{"Xb"}, {"Xc"}}, nil},
		{`SELECT regexp_matches(s,p,'g') AS m FROM (VALUES('aa','a'),('x','[')) v(s,p) WHERE p='a'`, []string{"m"}, []string{"_TEXT"}, [][]any{{"{a}"}, {"{a}"}}, nil},
	} {
		regexpRoutes(t, p, b, regexpRows(b, c.q, c.names, c.types, c.rows, c.args...))
	}
	// Array ordering is an existing non-regexp capability boundary. The
	// unordered witness above retains both complete capture-array results.
	q := `SELECT regexp_match(regexp_split_to_table('ab cd',' '),'(.)') AS m ORDER BY m`
	s := regexpRows(b, q, []string{"m"}, []string{"_TEXT"}, [][]any{{"{a}"}, {"{c}"}})
	if !b.PG {
		s = similarReferenceBindingError(q, fixture.Diagnostic{Code: "42883", Message: "could not identify an ordering operator for type text[]", Hint: "Use an explicit ordering operator or modify the query.", Position: fixture.Position(len(q))})
	}
	regexpRoutes(t, p, b, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE walker_source(id INT,s TEXT)`, 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO walker_source VALUES(1,'a b')`, 1))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE VIEW walker_view AS SELECT r.s FROM regexp_split_to_table((SELECT s FROM walker_source WHERE id=1),' ') r(s)`, 0))
	want := fixture.Select(`SELECT s FROM walker_view ORDER BY s`, []string{"s"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}})
	regexpRoutes(t, p, b, want)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`ALTER TABLE walker_source RENAME COLUMN s TO source`, 0))
	regexpRoutes(t, p, b, want)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`ALTER TABLE walker_source RENAME TO renamed_walker_source`, 0))
	regexpRoutes(t, p, b, want)
}
