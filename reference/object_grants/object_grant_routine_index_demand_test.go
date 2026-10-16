package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRoutineIndexDemand(t *testing.T) {
	objectGrantScenario(t, []string{"og_index_demand"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.json_array_length(json)"})
		run(fixture.Exec("CREATE SCHEMA og_index_demand", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_index_demand TO og_object_reader", 0))
		for _, name := range []string{"key_t", "false_t", "null_t", "predicate_t"} {
			run(fixture.Exec("CREATE TABLE og_index_demand."+name+"(n INT,j JSON)", 0))
			run(fixture.Exec("GRANT SELECT,INSERT ON og_index_demand."+name+" TO og_object_reader", 0))
		}
		run(fixture.Exec("CREATE INDEX og_demand_key ON og_index_demand.key_t(json_array_length(j))", 0))
		run(fixture.Exec("CREATE INDEX og_demand_false ON og_index_demand.false_t(json_array_length(j)) WHERE n<0", 0))
		run(fixture.Exec("CREATE INDEX og_demand_null ON og_index_demand.null_t(json_array_length(NULL::json))", 0))
		run(fixture.Exec("CREATE INDEX og_demand_predicate ON og_index_demand.predicate_t(n) WHERE json_array_length(j)>0", 0))
		q := "INSERT INTO og_index_demand.key_t VALUES($1,$2::json)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(1), "[1,2]")) })
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("INSERT INTO og_index_demand.key_t SELECT 2,'[1]'::json WHERE FALSE", 0))
			run(fixture.Exec("INSERT INTO og_index_demand.false_t VALUES(1,'[1]')", 1))
			run(fixture.Exec("INSERT INTO og_index_demand.null_t VALUES(1,NULL)", 1))
			for _, query := range []string{
				"INSERT INTO og_index_demand.key_t VALUES(2,'[1]')",
				"INSERT INTO og_index_demand.key_t VALUES(2,NULL)",
				"INSERT INTO og_index_demand.false_t VALUES(-1,'[1]')",
				"INSERT INTO og_index_demand.predicate_t VALUES(1,'[1]')",
				"INSERT INTO og_index_demand.predicate_t VALUES(1,NULL)",
			} {
				fixture.RoleGrantPermissionDenied(run, query, "function", "json_array_length")
			}
			// Constant RETURNING planning and row-demand RETURNING are distinct
			// precedence questions; their full oracle diagnostics stay independent.
			run(fixture.ImplicitError("INSERT INTO og_index_demand.key_t VALUES(2,'[1]') RETURNING 1/0", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			fixture.RoleGrantPermissionDenied(run, "INSERT INTO og_index_demand.key_t VALUES(2,'[1]') RETURNING n/(n-n)", "function", "json_array_length")
			denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function json_array_length"}, int64(2), "[1]")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
			run(fixture.Select("SELECT n FROM og_index_demand.key_t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT n FROM og_index_demand.false_t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT n FROM og_index_demand.predicate_t", []string{"n"}, []string{"INT4"}, nil))
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(2), "[1]"))
			run(fixture.Exec("INSERT INTO og_index_demand.key_t VALUES(3,NULL)", 1))
			run(fixture.Exec("INSERT INTO og_index_demand.predicate_t VALUES(1,NULL)", 1))
			run(fixture.Select("SELECT n FROM og_index_demand.key_t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		})
	})
}
