package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceHypotheticalAggregateFeatureSmoke(t *testing.T) {
	var emitted string
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		view := hypotheticalResult("SELECT rank,dense_rank,percent_rank,cume_dist FROM hs_smoke_view", hypotheticalRow(1, 1, 0, .5))
		if b.PG {
			if emitted == "" {
				t.Fatal("actual native dump missing before PostgreSQL replay")
			}
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal(err)
			}
		} else {
			hypotheticalSetup(t, p, c, b)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE hs_smoke_pairs(x INT,y TEXT)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hs_smoke_pairs VALUES(1,'a'),(1,'b'),(1,'b'),(2,'a'),(NULL,'z')", 5))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW hs_smoke_view AS SELECT "+hypotheticalProjection("1,'b'", "x,y DESC", "")+" FROM hs_smoke_pairs", 0))
		}
		for i, h := range []string{"5", "20", "30", "50"} {
			rows := [][]any{hypotheticalRow(1, 1, 0, .2), hypotheticalRow(2, 2, .25, .8), hypotheticalRow(4, 3, .75, .8), hypotheticalRow(5, 4, 1, 1)}
			orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection(h, "v", "")+" FROM hs_input", rows[i]))
		}
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("20", "v", " FILTER(WHERE FALSE)")+" FROM hs_input", hypotheticalRow(1, 1, 0, 1)))
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("NULL::int", "v NULLS FIRST", "")+" FROM (VALUES(1),(2),(2),(NULL)) AS a(v)", hypotheticalRow(1, 1, 0, .4)))
		orderedSetBoth(t, p, c, b, view)
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("h", "v", "")+" FROM (VALUES(1,2,1),(1,2,1),(1,2,3),(2,2,2),(2,2,NULL)) AS a(g,h,v) GROUP BY g,h ORDER BY g", hypotheticalRow(3, 2, 2.0/3, .75), hypotheticalRow(1, 1, 0, 2.0/3)))
		q := "SELECT rank(1) WITHIN GROUP(ORDER BY 1,2)"
		orderedSetBoth(t, p, c, b, hypotheticalArity(q, "rank", "integer, integer, integer", 1, 2))
		q = "/*é😀*/ SELECT rank(v) WITHIN GROUP(ORDER BY v) FROM hs_input"
		s := orderedSetDiagnostic(q, "42803", "column \"hs_input.v\" must appear in the GROUP BY clause or be used in an aggregate function", "v)", "")
		s.Diagnostic.Detail = "Direct arguments of an ordered-set aggregate must use only grouped columns."
		orderedSetBoth(t, p, c, b, s)
		q = "SELECT " + hypotheticalProjection("$1", "v", "") + " FROM hs_input"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		s = hypotheticalResult(q, hypotheticalRow(2, 2, .25, .8))
		s.Args = []any{"20"}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		bad := orderedSetDiagnostic(q, "22P02", "invalid input syntax for type integer: \"bad\"", "", "")
		bad.Args, bad.ErrorStage = []any{"bad"}, "execute"
		bad.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = dump.String()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			orderedSetBoth(t, p, c, b, view)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}
