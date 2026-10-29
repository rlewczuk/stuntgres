package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabFeatureSmoke(t *testing.T) {
	crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		crosstabSeed(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT * FROM crosstab($1::text) AS t(k text,c1 text,c2 text,c3 text) ORDER BY k"
		h := fixture.CrosstabHeld(t, p, c, q)
		run(fixture.CrosstabWant(q, fixture.CrosstabSource))
		run(fixture.Prepared(fixture.CrosstabWant(q, fixture.CrosstabSource)))
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.CrosstabWant(q, fixture.CrosstabSource))
		wrapperSQL := "SELECT * FROM crosstab3($1::text) ORDER BY row_name"
		wrapper := fixture.Select(wrapperSQL, []string{"row_name", "category_1", "category_2", "category_3"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"g1", "A", "C", nil}, {"g2", "B1", "B2", "B3"}, {"g3", nil, nil, nil}}, fixture.CrosstabSource)
		wh := fixture.CrosstabHeld(t, p, c, wrapperSQL)
		run(wrapper)
		run(fixture.Prepared(wrapper))
		fixture.RunStep(t, p.Ctx, wh, b.Recorder, wrapper)
		// Sequence zero places the new cell first, in the existing g1 group.
		run(fixture.Exec("INSERT INTO ct_source VALUES(0,'g1','later','fresh')", 1))
		fresh := fixture.CrosstabWant(q, fixture.CrosstabSource)
		fresh.Rows = [][]any{{"g1", "fresh", "A", "C"}, {"g2", "B1", "B2", "B3"}, {"g3", nil, nil, nil}}
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fresh)
		wrapper.Rows = fresh.Rows
		fixture.RunStep(t, p.Ctx, wh, b.Recorder, wrapper)
		crosstabFailure(t, p, b, c, "SELECT * FROM crosstab($1::text) AS t(k text,a text,b integer)", crosstabReturnError("Source value datatype text does not match return value datatype integer in column 3."), "execute", fixture.CrosstabSource)
		d := fixture.ControlsReferenceDiagnostic("0A000", "DELETE is not allowed in a non-volatile function")
		d.Context = "SQL statement \"DELETE FROM ct_source\""
		crosstabFailure(t, p, b, c, q, d, "execute", "DELETE FROM ct_source")
		run(fixture.Select("SELECT count(*) AS n FROM ct_source", []string{"n"}, []string{"INT8"}, [][]any{{int64(9)}}))
		run(fixture.Exec("CREATE VIEW ct_smoke_view AS SELECT * FROM crosstab('SELECT k,category,v FROM public.ct_source ORDER BY seq') AS t(k text,c1 text,c2 text,c3 text)", 0))
		view := fresh
		view.Query, view.Args = "SELECT * FROM ct_smoke_view ORDER BY k", nil
		run(view)
		if !b.PG {
			dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			run(view)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fresh)
		}
	})
}
