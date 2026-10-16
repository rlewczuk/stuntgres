package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRoutineOperators(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.int4pl(integer,integer)", "pg_catalog.int4eq(integer,integer)"})
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_operator_source(x INT)", 0))
		run(fixture.Exec("INSERT INTO og_operator_source VALUES(1),(2)", 2))
		run(fixture.Exec("GRANT SELECT ON og_operator_source TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT 1::INTEGER+2::INTEGER AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
		})
		query := "SELECT $1::INTEGER+$2::INTEGER AS v"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		want := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}, int64(1), int64(2))
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, want) })
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.int4pl(INTEGER,INTEGER) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function int4pl"}, int64(1), int64(2))
			denied.SelectRows = true
			fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
			fixture.RoleGrantPermissionDenied(run, "SELECT 1::INTEGER+2::INTEGER AS v", "function", "int4pl")
			fixture.RoleGrantPermissionDenied(run, "SELECT 1::INTEGER+2::INTEGER AS v WHERE FALSE", "function", "int4pl")
			fixture.RoleGrantPermissionDenied(run, "SELECT x+1 AS v FROM og_operator_source WHERE x>100", "function", "int4pl")
			run(fixture.Select("SELECT NULL::INTEGER+2::INTEGER AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
			fixture.RoleGrantPermissionDenied(run, "SELECT x+1 AS v FROM og_operator_source WHERE FALSE", "function", "int4pl")
			run(fixture.Select("SELECT 1::BIGINT+2::BIGINT AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
		})
		run(fixture.Exec("GRANT EXECUTE ON ROUTINE pg_catalog.int4pl(INTEGER,INTEGER) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, want) })
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.int4eq(INTEGER,INTEGER) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT x=1 AS v FROM og_operator_source", "function", "int4eq")
			// DISTINCT admits its actual equality function even without an
			// explicit operator expression in the SQL target list.
			fixture.RoleGrantPermissionDenied(run, "SELECT DISTINCT x FROM og_operator_source ORDER BY x", "function", "int4eq")
			// These executor equality plans use the same checked grouping seam.
			fixture.RoleGrantPermissionDenied(run, "SELECT x,count(*) AS n FROM og_operator_source GROUP BY x ORDER BY x", "function", "int4eq")
			fixture.RoleGrantPermissionDenied(run, "SELECT row_number() OVER(ORDER BY x) AS v FROM og_operator_source", "function", "int4eq")
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM og_operator_source UNION SELECT x FROM og_operator_source", "function", "int4eq")
			// Plain sorting uses btree support rather than the checked equality plan.
			run(fixture.Select("SELECT x FROM og_operator_source ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.int4eq(INTEGER,INTEGER) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT x=1 AS v FROM og_operator_source ORDER BY x", []string{"v"}, []string{"BOOL"}, [][]any{{true}, {false}}))
			run(fixture.Select("SELECT DISTINCT x FROM og_operator_source ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Select("SELECT x,count(*) AS n FROM og_operator_source GROUP BY x ORDER BY x", []string{"x", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}}))
		})
	})
}
