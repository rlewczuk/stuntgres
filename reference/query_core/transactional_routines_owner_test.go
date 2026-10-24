package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalRoutinePrivateOwner(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit=%v", commit), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"txr_old", "txr_new", "txr_reader", "txr_sentinel"})
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				routineExec(t, p, b, c, "CREATE ROLE txr_old", "CREATE ROLE txr_new", "CREATE ROLE txr_reader", "CREATE ROLE txr_sentinel", "GRANT USAGE,CREATE ON SCHEMA public TO txr_old,txr_new", "GRANT USAGE ON SCHEMA public TO txr_reader,txr_sentinel", "SET ROLE txr_old", "CREATE FUNCTION txr_owned(integer) RETURNS integer RETURN $1+1", "REVOKE EXECUTE ON FUNCTION txr_owned(integer) FROM PUBLIC", "GRANT EXECUTE ON FUNCTION txr_owned(integer) TO txr_reader", "RESET ROLE")
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				routineExec(t, p, b, peer, "SET SESSION AUTHORIZATION txr_reader")
				defer peer.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				q := "SELECT txr_owned(4) AS v"
				s, err := peer.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				held := fixture.HeldStatement{Executor: peer, Statement: s, SQL: q}
				check := func() {
					step := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}})
					step.CheckMetadata = routineScalarMetadata(b, "v", "INT4", false, false)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
				}
				check()
				oid := transactionalRoutineOID(t, p, c, "SELECT 'txr_owned(integer)'::regprocedure::oid")
				routineExec(t, p, b, c, "BEGIN", "ALTER FUNCTION txr_owned(integer) OWNER TO txr_new")
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT proowner='txr_new'::regrole AS owned FROM pg_proc WHERE oid='txr_owned(integer)'::regprocedure", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT proowner='txr_old'::regrole AS owned FROM pg_proc WHERE oid='txr_owned(integer)'::regprocedure", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}))
				check()
				end, want := "ROLLBACK", "txr_old"
				if commit {
					end, want = "COMMIT", "txr_new"
				}
				routineExec(t, p, b, c, end)
				if after := transactionalRoutineOID(t, p, c, "SELECT 'txr_owned(integer)'::regprocedure::oid"); after != oid {
					t.Fatal("owner transfer replaced executable identity", oid, after)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT proowner='"+want+"'::regrole AS owned FROM pg_proc WHERE oid='txr_owned(integer)'::regprocedure", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}))
				check()
				routineExec(t, p, b, c, "SET SESSION AUTHORIZATION txr_sentinel")
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				denied := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function txr_owned"))
				denied.SelectRows = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
				routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "SET ROLE "+want)
				routineValue(t, p, b, c, q, "v", "INT4", int64(5))
				routineExec(t, p, b, c, "RESET ROLE")
			})
		})
	}
}
