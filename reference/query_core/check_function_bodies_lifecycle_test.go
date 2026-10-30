package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCheckFunctionBodiesLifecycle(t *testing.T) {
	t.Run("session isolation", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			routineExec(t, p, b, a, "SET check_function_bodies=off", checkBodiesMismatch("cfb_session_a"))
			checkBodiesReads(t, p, b, a, "off")
			checkBodiesReads(t, p, b, peer, "on")
			checkBodiesFailure(t, p, b, peer, checkBodiesMismatch("cfb_session_b"), checkBodiesReturnDiagnostic("cfb_session_b", false), false)
			checkBodiesCount(t, p, b, peer, "cfb_session_a", 1)
			checkBodiesCount(t, p, b, peer, "cfb_session_b", 0)
		})
	})
	t.Run("transaction rollback", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			routineExec(t, p, b, c, "BEGIN", "SET check_function_bodies=off", checkBodiesMismatch("cfb_rolled_back"))
			checkBodiesReads(t, p, b, c, "off")
			checkBodiesCount(t, p, b, c, "cfb_rolled_back", 1)
			routineExec(t, p, b, c, "ROLLBACK")
			checkBodiesReads(t, p, b, c, "on")
			checkBodiesCount(t, p, b, c, "cfb_rolled_back", 0)
			checkBodiesFailure(t, p, b, c, checkBodiesMismatch("cfb_after_rollback"), checkBodiesReturnDiagnostic("cfb_after_rollback", false), false)
			checkBodiesCount(t, p, b, c, "cfb_after_rollback", 0)
		})
	})
	t.Run("local commit", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			routineExec(t, p, b, c, "BEGIN", "SET LOCAL check_function_bodies=off", checkBodiesMismatch("cfb_local_committed"))
			checkBodiesReads(t, p, b, c, "off")
			routineExec(t, p, b, c, "COMMIT")
			checkBodiesReads(t, p, b, c, "on")
			checkBodiesCount(t, p, b, c, "cfb_local_committed", 1)
			checkBodiesFailure(t, p, b, c, checkBodiesMismatch("cfb_after_local"), checkBodiesReturnDiagnostic("cfb_after_local", false), false)
		})
	})
	t.Run("persistent off savepoint", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			routineExec(t, p, b, c, "SET check_function_bodies=off", "BEGIN", "SAVEPOINT cfb_before_on", "SET check_function_bodies=on")
			// A checked CREATE aborts this subtransaction. Recover before issuing
			// any further query or attempting the prepared route.
			s := fixture.ErrorDiagnostic(checkBodiesMismatch("cfb_savepoint"), checkBodiesReturnDiagnostic("cfb_savepoint", false))
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			routineExec(t, p, b, c, "ROLLBACK TO cfb_before_on")
			checkBodiesReads(t, p, b, c, "off")
			checkBodiesCount(t, p, b, c, "cfb_savepoint", 0)
			routineExec(t, p, b, c, checkBodiesMismatch("cfb_savepoint"), "COMMIT")
			checkBodiesReads(t, p, b, c, "off")
			checkBodiesCount(t, p, b, c, "cfb_savepoint", 1)
		})
	})
	t.Run("nested savepoints", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			routineExec(t, p, b, c, "BEGIN", "SAVEPOINT cfb_outer", "SET check_function_bodies=off", checkBodiesMismatch("cfb_nested_first"), "SAVEPOINT cfb_inner", "SET LOCAL check_function_bodies=on")
			s := fixture.ErrorDiagnostic(checkBodiesMismatch("cfb_nested_second"), checkBodiesReturnDiagnostic("cfb_nested_second", false))
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			routineExec(t, p, b, c, "ROLLBACK TO cfb_inner")
			checkBodiesReads(t, p, b, c, "off")
			routineExec(t, p, b, c, checkBodiesMismatch("cfb_nested_second"), "ROLLBACK TO cfb_outer")
			checkBodiesReads(t, p, b, c, "on")
			checkBodiesCount(t, p, b, c, "cfb_nested_first", 0)
			checkBodiesCount(t, p, b, c, "cfb_nested_second", 0)
			s = fixture.ErrorDiagnostic(checkBodiesMismatch("cfb_nested_third"), checkBodiesReturnDiagnostic("cfb_nested_third", false))
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			routineExec(t, p, b, c, "ROLLBACK TO cfb_outer", "COMMIT")
			checkBodiesReads(t, p, b, c, "on")
			checkBodiesCount(t, p, b, c, "cfb_nested_third", 0)
		})
	})
	t.Run("routine effective environment", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c, "CREATE FUNCTION cfb_local_factory() RETURNS void LANGUAGE SQL SET check_function_bodies=off AS $outer$CREATE OR REPLACE FUNCTION cfb_local_child() RETURNS integer LANGUAGE SQL AS $$SELECT 'bad'::text$$$outer$")
			checkBodiesCount(t, p, b, c, "cfb_local_child", 0)
			routineValue(t, p, b, c, "SELECT 1 AS v FROM cfb_local_factory()", "v", "INT4", int64(1))
			checkBodiesCount(t, p, b, c, "cfb_local_child", 1)
			checkBodiesReads(t, p, b, c, "on")
			checkBodiesFailure(t, p, b, c, checkBodiesMismatch("cfb_after_factory"), checkBodiesReturnDiagnostic("cfb_after_factory", false), false)
		})
	})
	for _, issuer := range []string{"on", "off"} {
		for _, own := range []string{"on", "off"} {
			t.Run("CREATE issuer_"+issuer+"_own_"+own, func(t *testing.T) {
				routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					routineExec(t, p, b, c, "SET check_function_bodies="+issuer)
					q := "CREATE FUNCTION cfb_own_setting() RETURNS integer LANGUAGE SQL SET check_function_bodies=" + own + " AS $$SELECT 'bad'::text$$"
					count := int64(1)
					if issuer == "on" && own == "on" {
						checkBodiesFailure(t, p, b, c, q, checkBodiesReturnDiagnostic("cfb_own_setting", false), false)
						count = 0
					} else {
						routineExec(t, p, b, c, q)
						checkBodiesCount(t, p, b, c, "cfb_own_setting", 1)
						routineExec(t, p, b, c, "DROP FUNCTION cfb_own_setting()")
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(q, 0)))
					}
					checkBodiesCount(t, p, b, c, "cfb_own_setting", count)
					checkBodiesReads(t, p, b, c, issuer)
				})
			})
		}
	}
	t.Run("own on deferred invocation", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c, "SET check_function_bodies=off", "CREATE FUNCTION cfb_own_on() RETURNS integer LANGUAGE SQL SET check_function_bodies=on AS $$SELECT 'bad'::text$$")
			checkBodiesCount(t, p, b, c, "cfb_own_on", 1)
			runtime := checkBodiesReturnDiagnostic("cfb_own_on", false)
			runtime.Context += " statement 1"
			checkBodiesFailure(t, p, b, c, "SELECT cfb_own_on()", runtime, true)
			checkBodiesReads(t, p, b, c, "off")
		})
	})
}

func TestReferenceCheckFunctionBodiesAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cfb_writer"})
	fixture.SchemaReferenceCleanup(t, "cfb_private", "cfb_shared")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE ROLE cfb_writer", "CREATE SCHEMA cfb_private", "CREATE SCHEMA cfb_shared", "GRANT USAGE ON SCHEMA cfb_private,cfb_shared TO cfb_writer", "GRANT CREATE ON SCHEMA cfb_shared TO cfb_writer", "CREATE FUNCTION cfb_shared.predecessor() RETURNS integer LANGUAGE SQL AS $$SELECT 17$$", "SET SESSION AUTHORIZATION cfb_writer")
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		// User context permits ordinary SET; only the DDL is unauthorized.
		routineExec(t, p, b, c, "SET check_function_bodies=off")
		checkBodiesReads(t, p, b, c, "off")
		denied := "CREATE FUNCTION cfb_private.denied() RETURNS integer LANGUAGE SQL AS $$SELECT 'bad'::text$$"
		checkBodiesFailure(t, p, b, c, denied, fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema cfb_private"), false)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='cfb_private'::regnamespace AND proname='denied'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		replace := "CREATE OR REPLACE FUNCTION cfb_shared.predecessor() RETURNS integer LANGUAGE SQL AS $$SELECT 'bad'::text$$"
		checkBodiesFailure(t, p, b, c, replace, fixture.ControlsReferenceDiagnostic("42501", "must be owner of function predecessor"), false)
		routineValue(t, p, b, c, "SELECT cfb_shared.predecessor() AS v", "v", "INT4", int64(17))
		routineExec(t, p, b, c, "CREATE FUNCTION cfb_shared.ordinary() RETURNS integer LANGUAGE SQL AS $$SELECT 'bad'::text$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT prosrc FROM pg_proc WHERE oid='cfb_shared.ordinary()'::regprocedure", []string{"prosrc"}, []string{"TEXT"}, [][]any{{"SELECT 'bad'::text"}}))
	})
}
