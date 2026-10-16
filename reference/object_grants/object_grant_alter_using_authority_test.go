package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantAlterUsingAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		const outer = "pg_catalog.obj_description(oid,name)"
		const child = "pg_catalog.oideq(oid,oid)"
		// Reset ROLE before the existing ACL cleanup, including on assertion failure.
		defer objectGrantRoutineCleanup(t, p, c, []string{outer, child})
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
				t.Error("ALTER USING role cleanup", err)
			}
		}()
		fullDiagnostic := func(query, message, context string) fixture.Step {
			s := fixture.ImplicitError(query, fixture.Diagnostic{
				Code: "42501", Message: message, Context: context,
			})
			s.ErrorStage = "execute"
			return s
		}
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_other", 0))

		// Independent authorized witness: non-NULL OID0 demands the real inquiry
		// body even though its eventual result is SQL NULL, not the string NULL.
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("CREATE TABLE og_using_authorized(v OID)", 0))
			run(fixture.Exec("INSERT INTO og_using_authorized VALUES(0)", 1))
			run(fixture.Exec("ALTER TABLE og_using_authorized ALTER COLUMN v TYPE TEXT USING obj_description(v,'pg_class')", 0))
			run(fixture.Select("SELECT v FROM og_using_authorized", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
		})

		for _, route := range []string{"step", "original_held"} {
			table := "og_using_" + route
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Exec("CREATE TABLE "+table+"(id INT,v OID,w INT)", 0))
				run(fixture.Exec("INSERT INTO "+table+" VALUES(1,NULL,10),(2,0,20),(3,0,30)", 3))
			})
			run(fixture.Exec("GRANT ALL ON TABLE "+table+" TO og_object_other", 0))
			query := "ALTER TABLE " + table + " ALTER COLUMN w TYPE BIGINT USING w::BIGINT+100, ALTER COLUMN v TYPE TEXT USING obj_description(v,'pg_class')"
			var originalHeld fixture.HeldStatement
			if route == "original_held" {
				// Prepare once before any revoke. This is actual retained driver
				// utility state, not an assertion that Prepare cooks a USING plan.
				run(fixture.Exec("SET ROLE og_object_reader", 0))
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal("prepare original ALTER USING utility", err)
				}
				defer stmt.Close()
				originalHeld = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				run(fixture.Exec("RESET ROLE", 0))
			}
			attempt := func(s fixture.Step) {
				t.Helper()
				if route == "original_held" {
					// Do not ask RunStep to prepare this utility a second time.
					fixture.RunStep(t, p.Ctx, originalHeld, b.Recorder, s)
					return
				}
				run(s)
			}
			unchanged := func() {
				t.Helper()
				// SELECT * asserts complete original shape, column order, types,
				// NULL placement and all occurrences after every failed attempt.
				var zero any = int64(0)
				if b.PG {
					zero = "0"
				}
				run(fixture.Select("SELECT * FROM "+table+" ORDER BY id",
					[]string{"id", "v", "w"}, []string{"INT4", "OID", "INT4"},
					[][]any{{int64(1), nil, int64(10)}, {int64(2), zero, int64(20)}, {int64(3), zero, int64(30)}}))
			}
			ownerAttempt := func(s fixture.Step) {
				t.Helper()
				run(fixture.Exec("SET ROLE og_object_reader", 0))
				attempt(s)
				unchanged()
				run(fixture.Exec("RESET ROLE", 0))
			}

			// Even table ALL and routine EXECUTE do not confer ALTER ownership.
			fixture.RoleGrantActor(run, "og_object_other", func() {
				attempt(fullDiagnostic(query, "must be owner of table "+table, ""))
				unchanged()
			})
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+outer+" FROM PUBLIC", 0))
			ownerAttempt(fullDiagnostic(query, "permission denied for function obj_description", ""))
			// A grant to a different ordinary role cannot repair the owner actor.
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+outer+" TO og_object_other", 0))
			ownerAttempt(fullDiagnostic(query, "permission denied for function obj_description", ""))

			// Admit the outer invocation, then deny its actual SQL-body child.
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+outer+" TO og_object_reader", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+child+" FROM PUBLIC", 0))
			ownerAttempt(fullDiagnostic(query, "permission denied for function oideq", `SQL function "obj_description" statement 1`))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+child+" TO og_object_other", 0))
			ownerAttempt(fullDiagnostic(query, "permission denied for function oideq", `SQL function "obj_description" statement 1`))

			// Same target, original rows and (for original_held) same *sql.Stmt.
			// Regrant repairs real execution; no statement or table is recreated.
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+child+" TO og_object_reader", 0))
			run(fixture.Exec("SET ROLE og_object_reader", 0))
			attempt(fixture.Exec(query, 0))
			run(fixture.Select("SELECT * FROM "+table+" ORDER BY id",
				[]string{"id", "v", "w"}, []string{"INT4", "TEXT", "INT8"},
				[][]any{{int64(1), nil, int64(110)}, {int64(2), nil, int64(120)}, {int64(3), nil, int64(130)}}))
			run(fixture.Exec("RESET ROLE", 0))

			// Restore the starting PUBLIC baseline for the next independent route.
			for _, target := range []string{outer, child} {
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader,og_object_other", 0))
			}
		}
	})
}
