package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRoutinesSetAggregateWindow(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.sum(integer)", "pg_catalog.count()", "pg_catalog.row_number()", "pg_catalog.generate_series(integer,integer)", "pg_catalog.string_to_table(text,text)"})
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_routine_set_source(x INT)", 0))
		run(fixture.Exec("INSERT INTO og_routine_set_source VALUES(1),(2)", 2))
		run(fixture.Exec("GRANT SELECT ON og_routine_set_source TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT sum(x) AS v,count(*) AS n FROM og_routine_set_source", []string{"v", "n"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(2)}}))
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.sum(INTEGER) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for _, q := range []string{
				"SELECT sum(x) AS v FROM og_routine_set_source",
				"SELECT sum(x) AS v FROM og_routine_set_source WHERE FALSE",
				"SELECT sum(NULL::INTEGER) AS v FROM og_routine_set_source",
				"SELECT sum(x) FILTER(WHERE FALSE) AS v FROM og_routine_set_source",
			} {
				fixture.RoleGrantPermissionDenied(run, q, "aggregate", "sum")
			}
			fixture.RoleGrantPermissionDenied(run, "SELECT sum(x) OVER() AS v FROM og_routine_set_source", "function", "sum")
			run(fixture.Select("SELECT sum(x::BIGINT) AS v FROM og_routine_set_source", []string{"v"}, []string{"NUMERIC"}, [][]any{{"3"}}))
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.sum(INTEGER) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT sum(x) OVER() AS v FROM og_routine_set_source ORDER BY x", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(3)}}))
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.count() FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT count(*) AS v FROM og_routine_set_source WHERE FALSE", "aggregate", "count")
			run(fixture.Select("SELECT count(x) AS v FROM og_routine_set_source", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}}))
		})
		run(fixture.Exec("GRANT EXECUTE ON ROUTINE pg_catalog.count() TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT count(*) AS v FROM og_routine_set_source", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}}))
		})
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT row_number() OVER(ORDER BY x) AS v FROM og_routine_set_source", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}}))
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.row_number() FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT row_number() OVER() AS v FROM og_routine_set_source", "function", "row_number")
			fixture.RoleGrantPermissionDenied(run, "SELECT row_number() OVER() AS v FROM og_routine_set_source WHERE FALSE", "function", "row_number")
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.row_number() TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT row_number() OVER(ORDER BY x) AS v FROM og_routine_set_source", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}}))
		})

		series := "SELECT v FROM generate_series(1,2) AS g(v)"
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select(series, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.generate_series(INTEGER,INTEGER) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for _, q := range []string{
				series,
				"SELECT v FROM generate_series(2,1) AS g(v)",
				"SELECT v FROM generate_series(NULL::INTEGER,2) AS g(v)",
				"SELECT generate_series(1,2) AS v WHERE FALSE",
				"SELECT v FROM ROWS FROM (generate_series(1,2)) AS g(v)",
				"SELECT g.v FROM og_routine_set_source t CROSS JOIN LATERAL generate_series(t.x,t.x) AS g(v)",
			} {
				fixture.RoleGrantPermissionDenied(run, q, "function", "generate_series")
			}
			run(fixture.Select("SELECT v FROM generate_series(1,2) AS g(v) WHERE FALSE", []string{"v"}, []string{"INT4"}, nil))
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.generate_series(INTEGER,INTEGER) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select(series, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Select("SELECT v FROM ROWS FROM (generate_series(1,2)) AS g(v)", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Select("SELECT g.v FROM og_routine_set_source t CROSS JOIN LATERAL generate_series(t.x,t.x) AS g(v) ORDER BY g.v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
		strings := "SELECT v FROM string_to_table('a,b',',') AS g(v)"
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Select(strings, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}})) })
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.string_to_table(TEXT,TEXT) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, strings, "function", "string_to_table")
			fixture.RoleGrantPermissionDenied(run, "SELECT v FROM string_to_table('',',') AS g(v)", "function", "string_to_table")
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.string_to_table(TEXT,TEXT) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Select(strings, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}})) })
	})
}
