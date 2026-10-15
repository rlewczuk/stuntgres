package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHypotheticalAggregateBinding(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		hypotheticalSetup(t, p, c, b)
		for _, tc := range []struct {
			direct, order, source string
			row                   []any
		}{
			{"'20'", "v", " FROM hs_input", hypotheticalRow(2, 2, .25, .8)},
			{"20::bigint", "v::smallint", " FROM hs_input", hypotheticalRow(2, 2, .25, .8)},
			{"20.0::numeric", "v", " FROM hs_input", hypotheticalRow(2, 2, .25, .8)},
			{"20::float8", "v::numeric", " FROM hs_input", hypotheticalRow(2, 2, .25, .8)},
			{"'b'", "'a'", "", hypotheticalRow(2, 2, 1, 1)},
			{"NULL", "NULL", "", hypotheticalRow(1, 1, 0, 1)},
			{"1,'b'::varchar(4)", "x,y", " FROM (VALUES(1,'a'::text),(1,'b'),(1,'b'),(2,'a'),(NULL,'z')) AS a(x,y)", hypotheticalRow(2, 2, .2, 4.0/6)},
			{"'a' COLLATE \"C\"", "v", " FROM (VALUES('A'::text),('Z'),('a'),('é')) AS a(v)", hypotheticalRow(3, 3, .5, .8)},
		} {
			orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection(tc.direct, tc.order, "")+tc.source, tc.row))
		}
		for _, name := range []string{"rank", "dense_rank", "percent_rank", "cume_dist"} {
			typ := "INT8"
			var want any = int64(2)
			if name == "percent_rank" {
				typ, want = "FLOAT8", float64(.25)
			}
			if name == "cume_dist" {
				typ, want = "FLOAT8", float64(.8)
			}
			for _, spelling := range []string{name, "pg_catalog." + name, "\"" + name + "\""} {
				orderedSetBoth(t, p, c, b, orderedSetValue("SELECT "+spelling+"(20) WITHIN GROUP(ORDER BY v) AS v FROM hs_input", typ, want))
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE hs_grouped(g INT,h INT,v INT)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hs_grouped VALUES(1,2,1),(1,2,1),(1,2,3),(2,2,2),(2,2,NULL)", 5))
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("h", "v", "")+" FROM hs_grouped GROUP BY g,h ORDER BY g", hypotheticalRow(3, 2, 2.0/3, .75), hypotheticalRow(1, 1, 0, 2.0/3)))
		// Inference, execution failure, and recovery all use the SAME prepared object.
		q := "SELECT " + hypotheticalProjection("$1", "v", "") + " FROM hs_input"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			arg any
			row []any
		}{
			{"20", hypotheticalRow(2, 2, .25, .8)},
			{nil, hypotheticalRow(5, 4, 1, 1)},
			{"30", hypotheticalRow(4, 3, .75, .8)},
		} {
			s := hypotheticalResult(q, tc.row)
			s.Args = []any{tc.arg}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		bad := orderedSetDiagnostic(q, "22P02", "invalid input syntax for type integer: \"bad\"", "", "")
		bad.Args, bad.ErrorStage = []any{"bad"}, "execute"
		bad.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		s := hypotheticalResult(q, hypotheticalRow(2, 2, .25, .8))
		s.Args = []any{"20"}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		q = "SELECT " + hypotheticalProjection("1,$1", "x,y", "") + " FROM (VALUES(1,'a'::text),(1,'b'),(1,'b'),(2,'a'),(NULL,'z')) AS a(x,y)"
		s = hypotheticalResult(q, hypotheticalRow(2, 2, .2, 4.0/6))
		s.Args = []any{"b"}
		orderedSetBoth(t, p, c, b, s)
		q = "SELECT " + hypotheticalProjection("$1", "$2", "")
		s = hypotheticalResult(q, hypotheticalRow(2, 2, 1, 1))
		s.Args = []any{"b", "a"}
		orderedSetBoth(t, p, c, b, s)
		// Direct evaluation is not suppressed by a terminal empty-input decision.
		for _, tail := range []string{" WHERE FALSE", " HAVING FALSE", ""} {
			filter := ""
			if tail == "" {
				filter = " FILTER(WHERE FALSE)"
			}
			q := "SELECT rank(1/(SELECT 0)) WITHIN GROUP(ORDER BY v)" + filter + " FROM hs_input" + tail
			orderedSetRuntimeError(t, p, c, b, q, "22012", "division by zero")
		}
		orderedSetBoth(t, p, c, b, orderedSetValue("SELECT rank(1) WITHIN GROUP(ORDER BY 1/(v-v)) FILTER(WHERE FALSE) AS v FROM hs_input", "INT8", int64(1)))
	})
}
