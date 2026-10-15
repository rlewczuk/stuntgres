package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceQueryCycleFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := cycleConn(t, p, backend)
			run := subqueryRun(t, p, backend, c)
			cycleSeed(run)
			t.Run("closing row and paths", func(t *testing.T) {
				s := cycleGraphStep(cycleGraph + "SELECT * FROM r")
				s.CheckMetadata = cycleMetadata(backend, s.Columns, s.Types)
				recursiveCTERoutes(t, p, backend, c, s)
			})
			t.Run("fresh held seeds", func(t *testing.T) {
				q := strings.Replace(cycleGraph, "SELECT 'a'::text", "SELECT $1::text", 1) + "SELECT * FROM r"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range []struct {
					seed any
					rows [][]any
				}{
					{"a", cycleGraphRows()},
					{"b", [][]any{{"b", false, "{(b)}"}, {"a", false, "{(b),(a)}"}, {"b", true, "{(b),(a),(b)}"}, {"c", false, "{(b),(a),(c)}"}}},
					{nil, [][]any{{nil, false, "{()}"}}},
					{"a", cycleGraphRows()},
				} {
					s := cycleGraphStep(q)
					s.Args = []any{tc.seed}
					s.Rows = tc.rows
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
				}
			})
			t.Run("null multiple keys", func(t *testing.T) {
				q := "WITH RECURSIVE r(x,y) AS (SELECT 1,NULL::int UNION ALL SELECT x,CASE WHEN y IS NULL THEN 2 ELSE NULL END FROM r) CYCLE x,y SET cyc USING path SELECT * FROM r"
				subqueryBoth(subqueryRun(t, p, backend, c), cteBag(q, []string{"x", "y", "cyc", "path"}, []string{"INT4", "INT4", "BOOL", "_RECORD"}, [][]any{{int64(1), nil, false, `{"(1,)"}`}, {int64(1), int64(2), false, `{"(1,)","(1,2)"}`}, {int64(1), nil, true, `{"(1,)","(1,2)","(1,)"}`}}))
			})
			t.Run("ambiguous conflict reference", func(t *testing.T) {
				q := "/*é*/ WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET n USING path SELECT * FROM r"
				cycleAssertDiagnostic(t, p, backend, c, q, cycleDiagnostic("42702", `column reference "n" is ambiguous`, q, "n FROM"))
			})
			t.Run("conflict", func(t *testing.T) {
				q := "/*é*/ WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 1 FROM r) CYCLE n SET n USING path SELECT * FROM r"
				cycleAssertDiagnostic(t, p, backend, c, q, cycleDiagnostic("42601", `cycle mark column name "n" already used in WITH query column list`, q, "CYCLE"))
			})
			t.Run("incompatible marks", func(t *testing.T) {
				q := "/*é*/ WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET cyc TO true DEFAULT 0 USING path SELECT * FROM r"
				cycleAssertDiagnostic(t, p, backend, c, q, cycleDiagnostic("42804", "CYCLE types boolean and integer cannot be matched", q, "0 USING"))
			})
			t.Run("stored control recipe", func(t *testing.T) { cycleSmokeStored(t, p, backend, c) })
			t.Run("late failure and atomicity", func(t *testing.T) { cycleFailure(t, p, backend, c) })
			t.Run("genuine cancellation and recovery", func(t *testing.T) { cycleCancellation(t, p, backend, c) })
		})
	}
}

func cycleSmokeStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := subqueryRun(t, p, b, c)
	run(fixture.Exec("CREATE VIEW cycle_smoke_view AS "+cycleGraph+"SELECT n,cyc,path::text AS path FROM r", 0))
	check := cteBag("SELECT * FROM cycle_smoke_view", []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "TEXT"}, cycleGraphRows())
	subqueryBoth(run, check)
	if !b.PG {
		dump := fixture.ViewDump(t, p)
		rows, err := c.QueryContext(p.Ctx, cycleGraph+"SELECT * FROM r")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		metadata := cycleMetadata(b, []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "_RECORD"})
		metadata(t, rows)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.ViewRestore(t, p, dump)
		metadata(t, rows)
		var retained [][]any
		for rows.Next() {
			var n, cyc, path any
			if err := rows.Scan(&n, &cyc, &path); err != nil {
				t.Fatal(err)
			}
			retained = append(retained, []any{n, cyc, path})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		cycleAssertBag(t, retained, cycleGraphRows())
		subqueryBoth(run, check)
		foreign := fixture.NewFixture(t, false, false)
		if err := stuntgres.Restore(foreign.Ctx, foreign.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		for _, fb := range foreign.Backends() {
			fixture.RunStep(t, foreign.Ctx, fb.DB, fb.Recorder, check)
		}
	}
	run(fixture.Exec("INSERT INTO cycle_edges VALUES('c','d')", 1))
	check.Rows = append(cycleGraphRows(), []any{"d", false, "{(a),(c),(d)}"})
	subqueryBoth(run, check)
}
