package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalRoutineHeldRecovery(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		create := "CREATE FUNCTION public.txr_held(integer) RETURNS integer RETURN $1+1"
		utility, err := c.PrepareContext(p.Ctx, create)
		if err != nil {
			t.Fatal(err)
		}
		defer utility.Close()
		transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_held'", 0)
		q := "SELECT public.txr_held(4) AS v"
		var consumer *sql.Stmt
		defer func() {
			if consumer != nil {
				consumer.Close()
			}
		}()
		for range 2 {
			routineExec(t, p, b, c, "BEGIN")
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: utility, SQL: create}, b.Recorder, fixture.Exec(create, 0))
			if consumer == nil {
				consumer, err = c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
			}
			step := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}})
			step.CheckMetadata = routineScalarMetadata(b, "v", "INT4", false, false)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: consumer, SQL: q}, b.Recorder, step)
			routineExec(t, p, b, c, "ROLLBACK")
			transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_held'", 0)
		}
	})
}

func transactionalRoutineUtilityAuthority(t *testing.T) {
	for _, family := range []string{"function", "aggregate", "operator"} {
		t.Run(family, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"txr_reader"})
			fixture.SchemaReferenceCleanup(t, "txr_auth")
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				routineExec(t, p, b, c, "CREATE ROLE txr_reader", "CREATE SCHEMA txr_auth", "GRANT USAGE ON SCHEMA txr_auth TO txr_reader")
				q := "CREATE FUNCTION txr_auth.f(integer) RETURNS integer RETURN $1+1"
				call := "SELECT txr_auth.f(4) AS v"
				if family == "aggregate" {
					q = "CREATE AGGREGATE txr_auth.f(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')"
					call = "SELECT txr_auth.f(v) AS v FROM(VALUES(2),(3)) s(v)"
				}
				if family == "operator" {
					q = "CREATE OPERATOR txr_auth.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)"
					call = "SELECT 2 OPERATOR(txr_auth.#~#) 3 AS v"
				}
				s, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				held := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				routineExec(t, p, b, c, "SET SESSION AUTHORIZATION txr_reader", "BEGIN")
				denied := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema txr_auth"))
				denied.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
				routineExec(t, p, b, c, "ROLLBACK", "RESET SESSION AUTHORIZATION", "GRANT CREATE ON SCHEMA txr_auth TO txr_reader", "SET SESSION AUTHORIZATION txr_reader", "BEGIN")
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
				routineValueWithNativeNullability(t, p, b, c, call, "v", "INT4", int64(5), family == "aggregate", family == "aggregate")
				routineExec(t, p, b, c, "ROLLBACK", "RESET SESSION AUTHORIZATION")
				transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='txr_auth'::regnamespace AND proname='f'", 0)
				transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='txr_auth'::regnamespace AND oprname='#~#'", 0)
			})
		})
	}
}
