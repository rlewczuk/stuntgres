package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRoutineConditionalEquality(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.int4eq(integer,integer)"})
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_conditional_source(x INT)", 0))
		run(fixture.Exec("INSERT INTO og_conditional_source VALUES(1),(2),(NULL)", 3))
		run(fixture.Exec("GRANT SELECT ON og_conditional_source TO og_object_reader", 0))
		want := fixture.Select("SELECT NULLIF(x,2) AS n,CASE x WHEN 2 THEN 99 ELSE 0 END AS c,x IS DISTINCT FROM 2 AS d,x IS NOT DISTINCT FROM 2 AS e FROM og_conditional_source ORDER BY x", []string{"n", "c", "d", "e"}, []string{"INT4", "INT4", "BOOL", "BOOL"}, [][]any{{int64(1), int64(0), true, false}, {nil, int64(99), false, true}, {nil, int64(0), true, false}})
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(want) })
		query := "SELECT NULLIF($1::INTEGER,$2::INTEGER) AS v"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.int4eq(INTEGER,INTEGER) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for _, expression := range []string{"NULLIF(x,2)", "CASE x WHEN 2 THEN 99 ELSE 0 END", "x IS DISTINCT FROM 2", "x IS NOT DISTINCT FROM 2"} {
				fixture.RoleGrantPermissionDenied(run, "SELECT "+expression+" AS v FROM og_conditional_source", "function", "int4eq")
				fixture.RoleGrantPermissionDenied(run, "SELECT "+expression+" AS v FROM og_conditional_source WHERE FALSE", "function", "int4eq")
			}
			fixture.RoleGrantPermissionDenied(run, "SELECT NULLIF(1::INTEGER,2::INTEGER) AS v", "function", "int4eq")
			run(fixture.Select("SELECT NULLIF(NULL::INTEGER,2::INTEGER) AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
			run(fixture.Select("SELECT CASE WHEN FALSE THEN NULLIF(x,2) ELSE x END AS v FROM og_conditional_source ORDER BY x", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}))
			denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function int4eq"}, int64(1), int64(2))
			denied.SelectRows = true
			fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.int4eq(INTEGER,INTEGER) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(want)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}, int64(1), int64(2)))
		})
	})
}
