package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantSQLPreparedActualAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.abs(integer)"})
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE SEQUENCE og_actual_seq START 10", 0))
		run(fixture.Exec("GRANT USAGE ON SEQUENCE og_actual_seq TO og_object_reader", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("PREPARE og_actual(integer) AS SELECT $1 AS v", 0))
		const q = "EXECUTE og_actual(abs(-7))"
		const effect = "EXECUTE og_actual(abs(nextval('og_actual_seq')::integer))"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		phase := func(allowed bool) {
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				if !allowed {
					s = objectGrantAccessDenied(q, "abs", true)
				}
				run(s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			})
		}
		sequence := func(want int64) {
			run(fixture.Select("SELECT nextval('og_actual_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{want}}))
		}
		phase(true)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", 0))
		phase(false)
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(objectGrantAccessDenied(effect, "abs", true)) })
		sequence(10)
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_reader", 0))
		phase(true)
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select(effect, []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
		})
		sequence(12)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_reader", 0))
		phase(false)
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(objectGrantAccessDenied(effect, "abs", true)) })
		sequence(13)
	})
}

func TestReferenceObjectGrantSQLPreparedActualBody(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.oideq(oid,oid)"})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("PREPARE og_actual_body(text) AS SELECT $1 AS v", 0))
		const q = "EXECUTE og_actual_body(obj_description(0::oid,'pg_class'))"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		phase := func(allowed bool) {
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{nil}})
				if !allowed {
					s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function oideq", Context: `SQL function "obj_description" statement 1`})
					s.SelectRows, s.ErrorStage = true, "execute"
				}
				run(s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			})
		}
		phase(true)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.oideq(oid,oid) FROM PUBLIC", 0))
		phase(false)
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.oideq(oid,oid) TO og_object_reader", 0))
		phase(true)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.oideq(oid,oid) FROM og_object_reader", 0))
		phase(false)
	})
}
