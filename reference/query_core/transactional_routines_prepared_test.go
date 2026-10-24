package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalRoutinesPrepared(t *testing.T) {
	t.Run("utility-current-authority", transactionalRoutineUtilityAuthority)
	t.Run("hard-default-soft-authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"routine_phase_reader"})
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c, "CREATE ROLE routine_phase_reader", "GRANT USAGE ON SCHEMA public TO routine_phase_reader",
				"CREATE FUNCTION routine_phase_callee(integer) RETURNS integer RETURN $1+1000", "CREATE FUNCTION routine_phase_hard() RETURNS integer RETURN routine_phase_callee(1)", "CREATE FUNCTION routine_phase_default(x integer DEFAULT routine_phase_callee(1)) RETURNS integer RETURN x", "CREATE FUNCTION routine_phase_soft() RETURNS integer LANGUAGE SQL AS $$SELECT routine_phase_callee(1)$$", "REVOKE EXECUTE ON FUNCTION routine_phase_callee(integer) FROM PUBLIC", "GRANT EXECUTE ON FUNCTION routine_phase_callee(integer) TO routine_phase_reader", "ALTER FUNCTION routine_phase_callee(integer) RENAME TO routine_phase_renamed", "CREATE FUNCTION routine_phase_callee(integer) RETURNS integer RETURN $1+2000", "SET ROLE routine_phase_reader")
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			q := "SELECT routine_phase_hard() AS v"
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			held := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
			checkHeld := func() {
				step := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1001)}})
				step.CheckMetadata = routineScalarMetadata(b, "v", "INT4", false, false)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			}
			checkHeld()
			routineValue(t, p, b, c, "SELECT routine_phase_default() AS v", "v", "INT4", int64(1001))
			routineValue(t, p, b, c, "SELECT routine_phase_soft() AS v", "v", "INT4", int64(2001))
			routineAlterRows(t, p, b, c, "SELECT routine_phase_hard(),routine_phase_default(),routine_phase_soft()", []string{"routine_phase_hard", "routine_phase_default", "routine_phase_soft"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1001), int64(1001), int64(2001)}})
			routineExec(t, p, b, c, "RESET ROLE", "REVOKE EXECUTE ON FUNCTION routine_phase_renamed(integer) FROM routine_phase_reader", "SET ROLE routine_phase_reader")
			for _, query := range []string{q, "SELECT routine_phase_default()", "SELECT routine_phase_default(routine_phase_renamed(1))"} {
				step := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function routine_phase_renamed"))
				step.SelectRows, step.ErrorStage = true, "execute"
				if !b.PG && query == q {
					native := *step.Diagnostic
					native.Context = "SQL function \"routine_phase_hard\" statement 1"
					step.Diagnostic = &native
				}
				var e fixture.Executor = c
				if query == q {
					e = held
				}
				fixture.RunStep(t, p.Ctx, e, b.Recorder, step)
			}
			routineAlterRows(t, p, b, c, "SELECT routine_phase_default(9),routine_phase_soft()", []string{"routine_phase_default", "routine_phase_soft"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(2001)}})
			routineValue(t, p, b, c, "SELECT routine_phase_default(routine_phase_callee(1)) AS v", "v", "INT4", int64(2001))
			routineExec(t, p, b, c, "RESET ROLE", "GRANT EXECUTE ON FUNCTION routine_phase_renamed(integer) TO routine_phase_reader", "SET ROLE routine_phase_reader")
			checkHeld()
			routineAlterRows(t, p, b, c, "SELECT routine_phase_hard(),routine_phase_default()", []string{"routine_phase_hard", "routine_phase_default"}, []string{"INT4", "INT4"}, [][]any{{int64(1001), int64(1001)}})
		})
	})
	t.Run("aggregate-support-actor", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"routine_aggregate_owner", "routine_aggregate_reader"})
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c, "CREATE ROLE routine_aggregate_owner", "CREATE ROLE routine_aggregate_reader", "GRANT USAGE,CREATE ON SCHEMA public TO routine_aggregate_owner", "GRANT USAGE ON SCHEMA public TO routine_aggregate_reader", "SET ROLE routine_aggregate_owner", "CREATE FUNCTION routine_aggregate_step(bigint,integer) RETURNS bigint RETURN $1+$2", "REVOKE EXECUTE ON FUNCTION routine_aggregate_step(bigint,integer) FROM PUBLIC", "CREATE AGGREGATE routine_aggregate_sum(integer) (SFUNC=routine_aggregate_step,STYPE=bigint,INITCOND='0')", "REVOKE EXECUTE ON FUNCTION routine_aggregate_sum(integer) FROM PUBLIC", "GRANT EXECUTE ON FUNCTION routine_aggregate_sum(integer) TO routine_aggregate_reader", "CREATE VIEW routine_aggregate_view AS SELECT routine_aggregate_sum(x) AS v FROM(VALUES(2),(3)) s(x)", "GRANT SELECT ON routine_aggregate_view TO routine_aggregate_reader", "ALTER FUNCTION routine_aggregate_sum(integer) RENAME TO routine_aggregate_renamed", "ALTER FUNCTION routine_aggregate_step(bigint,integer) RENAME TO routine_aggregate_step_new", "RESET ROLE", "SET ROLE routine_aggregate_reader")
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			q := "SELECT v FROM routine_aggregate_view"
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			held := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
			check := func() {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(5)}}))
			}
			check()
			denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function routine_aggregate_step_new")
			step := fixture.ErrorDiagnostic("SELECT routine_aggregate_step_new(0::bigint,1)", denied)
			step.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			routineExec(t, p, b, c, "RESET ROLE", "SET ROLE routine_aggregate_owner", "REVOKE EXECUTE ON FUNCTION routine_aggregate_step_new(bigint,integer) FROM routine_aggregate_owner", "RESET ROLE", "SET ROLE routine_aggregate_reader")
			step = fixture.ErrorDiagnostic(q, denied)
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			routineExec(t, p, b, c, "RESET ROLE", "SET ROLE routine_aggregate_owner", "GRANT EXECUTE ON FUNCTION routine_aggregate_step_new(bigint,integer) TO routine_aggregate_owner", "RESET ROLE", "SET ROLE routine_aggregate_reader")
			check()
		})
	})
	t.Run("body-cache-and-shape", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c, "CREATE TABLE routine_unit_data(x integer)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_unit_data VALUES(4)", 1))
			routineExec(t, p, b, c, "CREATE FUNCTION routine_unit_read() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM routine_unit_data$$")
			q := "SELECT routine_unit_read() AS v"
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			held := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
			value := func(n int64) {
				step := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{n}})
				step.CheckMetadata = routineScalarMetadata(b, "v", "INT4", false, false)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			}
			value(4)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE routine_unit_data SET x=9", 1))
			value(9)
			routineExec(t, p, b, c, "DISCARD PLANS")
			value(9)
			routineExec(t, p, b, c, "BEGIN", "SAVEPOINT original_source", "ALTER FUNCTION routine_unit_read() RENAME TO routine_unit_moved")
			missing := fixture.ControlsReferenceDiagnostic("42883", "function routine_unit_read() does not exist")
			missing.Position, missing.Hint = "8", "No function matches the given name and argument types. You might need to add explicit type casts."
			stale := fixture.ErrorDiagnostic(q, missing)
			stale.SelectRows, stale.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, stale)
			routineExec(t, p, b, c, "ROLLBACK TO original_source", "RELEASE original_source", "COMMIT")
			value(9)
			routineExec(t, p, b, c, "BEGIN", "DROP FUNCTION routine_unit_read()", "CREATE FUNCTION routine_unit_read() RETURNS bigint RETURN 12::bigint")
			step := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			routineExec(t, p, b, c, "ROLLBACK")
			value(9)
		})
	})
}
