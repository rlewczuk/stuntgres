package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func objectGrantRoutineCleanup(t *testing.T, p *fixture.Pair, c *sql.Conn, targets []string) {
	t.Helper()
	if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
		t.Error("routine ACL cleanup identity", err)
		return
	}
	for _, target := range targets {
		for _, q := range []string{"GRANT EXECUTE ON FUNCTION " + target + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + target + " FROM og_object_reader,og_object_other CASCADE"} {
			if _, err := c.ExecContext(p.Ctx, q); err != nil {
				t.Error("routine ACL cleanup", q, err)
			}
		}
	}
}

func TestReferenceObjectGrantRoutinesScalar(t *testing.T) {
	objectGrantScenario(t, []string{"og_routine_scalar"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.abs(integer)", "pg_catalog.abs(bigint)", "pg_catalog.pi()"})
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_abs_source(x INT)", 0))
		run(fixture.Exec("INSERT INTO og_abs_source VALUES(-3),(-4)", 2))
		run(fixture.Exec("GRANT SELECT ON og_abs_source TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT abs(-3::INTEGER) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
			run(fixture.Select("SELECT abs(-3::BIGINT) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
		})
		query := "SELECT abs($1::INTEGER) AS v"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		want := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}, int64(-3))
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, want) })
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(IN input INTEGER) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function abs"}, int64(-3))
			denied.SelectRows = true
			fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
			fixture.RoleGrantPermissionDenied(run, "SELECT abs(-3::INTEGER) AS v", "function", "abs")
			fixture.RoleGrantPermissionDenied(run, "SELECT abs(x) AS v FROM og_abs_source WHERE x>100", "function", "abs")
			// Strict NULL folding eliminates this invocation. A statically false
			// source still initializes a nonfolded call and checks its permission.
			run(fixture.Select("SELECT abs(NULL::INTEGER) AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
			fixture.RoleGrantPermissionDenied(run, "SELECT abs(x) AS v FROM og_abs_source WHERE FALSE", "function", "abs")
			run(fixture.Select("SELECT abs(-3::BIGINT) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
		})
		run(fixture.Exec("GRANT ALL PRIVILEGES ON ROUTINE pg_catalog.abs(IN input INTEGER) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, want) })
		run(fixture.ImplicitError("GRANT EXECUTE ON FUNCTION pg_catalog.abs TO og_object_reader", fixture.Diagnostic{Code: "42725", Message: `function name "pg_catalog.abs" is not unique`, Hint: "Specify the argument list to select the function unambiguously."}))
		fixture.RoleGrantError(run, "GRANT EXECUTE ON FUNCTION pg_catalog.abs() TO og_object_reader", "42883", "function pg_catalog.abs() does not exist")
		fixture.RoleGrantError(run, "GRANT EXECUTE ON PROCEDURE pg_catalog.abs(INTEGER) TO og_object_reader", "42809", "pg_catalog.abs(integer) is not a procedure")
		// An omitted signature is legal for a genuinely unique function.
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.pi TO og_object_reader", 0))

		run(fixture.Exec("CREATE SCHEMA og_routine_scalar", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA og_routine_scalar", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_routine_scalar TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT og_routine_scalar.unaccent('Hôtel') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION og_routine_scalar.unaccent(TEXT) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT og_routine_scalar.unaccent('Hôtel') AS v", "function", "unaccent")
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION og_routine_scalar.unaccent(IN input TEXT) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT og_routine_scalar.unaccent('Hôtel') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
		})
	})
}
