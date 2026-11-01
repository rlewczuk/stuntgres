package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabCategoriesValues(t *testing.T) {
	crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		crosstabSeed(t, p, b, c)
		q := crosstabCategoriesSQL("crosstab", "k text,z text,a text,x text") + " ORDER BY k"
		for _, tc := range []struct {
			categories []string
			rows       [][]any
		}{
			{[]string{"z", "a", "x"}, [][]any{{"g1", "A", "C", nil}, {"g2", nil, nil, "B2"}, {"g3", nil, nil, nil}}},
			{[]string{"x", "a", "z"}, [][]any{{"g1", nil, "C", "A"}, {"g2", "B2", nil, nil}, {"g3", nil, nil, nil}}},
		} {
			s := fixture.Select(q, []string{"k", "z", "a", "x"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, tc.rows, fixture.CrosstabSource, crosstabCategoriesOrder(tc.categories...))
			crosstabCategoriesBoth(t, p, b, c, s)
		}
		h := fixture.CrosstabHeld(t, p, c, q)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"k", "z", "a", "x"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"g1", "A", "C", nil}, {"g2", nil, nil, "B2"}, {"g3", nil, nil, nil}}, fixture.CrosstabSource, crosstabCategoriesOrder("z", "a", "x")))
		crosstabCategoriesExtras(t, p, b, c)
		extra := crosstabCategoriesSQL("crosstab", "k text,metadata text,b integer,a integer,c integer") + " ORDER BY k"
		crosstabCategoriesBoth(t, p, b, c, crosstabCategoriesExtrasWant(extra, 20))
		// The first ignored category still fixes every extra; later invalid extra
		// strings and ignored values must never enter destination input codecs.
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE ct_many_extras(seq integer,k text,flag text,n text,category text,v text)", 0),
			fixture.Exec("INSERT INTO ct_many_extras VALUES(1,'g','true','7','ignored','bad'),(2,'g','bad','bad','a','11'),(3,'g','false','8','b','12')", 3),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		q = crosstabCategoriesSQL("crosstab", "k text,flag boolean,n integer,a integer,b integer")
		crosstabCategoriesBoth(t, p, b, c, fixture.Select(q, []string{"k", "flag", "n", "a", "b"}, []string{"TEXT", "BOOL", "INT4", "INT4", "INT4"}, [][]any{{"g", true, int64(7), int64(11), int64(12)}}, "SELECT k,flag,n,category,v FROM ct_many_extras ORDER BY seq", crosstabCategoriesOrder("a", "b")))
	})
}
