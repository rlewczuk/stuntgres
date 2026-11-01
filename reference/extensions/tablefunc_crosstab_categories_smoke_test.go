package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabCategoriesFeatureSmoke(t *testing.T) {
	crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION tablefunc", 0))
		crosstabCategoriesExtras(t, p, b, c)
		q := crosstabCategoriesSQL("crosstab", "k text,metadata text,b integer,a integer,c integer") + " ORDER BY k"
		h := fixture.CrosstabHeld(t, p, c, q)
		want := crosstabCategoriesExtrasWant(q, 20)
		crosstabCategoriesBoth(t, p, b, c, want)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
		reversed := crosstabCategoriesSQL("crosstab", "k text,metadata text,a integer,b integer,c integer") + " ORDER BY k"
		crosstabCategoriesBoth(t, p, b, c, fixture.Select(reversed, []string{"k", "metadata", "a", "b", "c"}, []string{"TEXT", "TEXT", "INT4", "INT4", "INT4"}, [][]any{{"g1", "first", int64(10), int64(20), nil}}, crosstabCategoriesExtrasSource, crosstabCategoriesOrder("a", "b", "c")))
		run(fixture.Exec("UPDATE ct_extras SET v='21' WHERE seq=2", 1))
		want = crosstabCategoriesExtrasWant(q, 21)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
		crosstabFailure(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42710", "duplicate category name"), "execute", crosstabCategoriesExtrasSource, crosstabCategoriesOrder("b", "b", "c"))
		run(fixture.Select("SELECT seq,k,metadata,category,v FROM ct_extras ORDER BY seq", []string{"seq", "k", "metadata", "category", "v"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "g1", "first", "ignored", "not-an-integer"}, {int64(2), "g1", "later", "b", "21"}, {int64(3), "g1", "later", "a", "10"}}))
		crosstabCategoriesDeclaration(t, p, b, c, "public")
		run(fixture.Exec("CREATE VIEW ct_category_smoke AS SELECT * FROM crosstab("+fixture.CrosstabQuote(crosstabCategoriesExtrasSource)+","+fixture.CrosstabQuote(crosstabCategoriesOrder("b", "a", "c"))+") AS t(k text,metadata text,b integer,a integer,c integer)", 0))
		view := want
		view.Query, view.Args = "SELECT * FROM ct_category_smoke ORDER BY k", nil
		crosstabCategoriesBoth(t, p, b, c, view)
		if !b.PG {
			dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			crosstabCategoriesBoth(t, p, b, c, view)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
			crosstabCategoriesDeclaration(t, p, b, c, "public")
		}
	})
}
