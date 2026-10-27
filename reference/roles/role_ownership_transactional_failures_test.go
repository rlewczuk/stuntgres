package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipTransactionalLateFailure(t *testing.T) {
	for _, kind := range []string{"denied-source", "class-eligibility"} {
		t.Run(kind, func(t *testing.T) {
			ownershipTransactionCases(t, func(s *fixture.RoleMembershipScenario, route string) {
				ownershipSeed(s, "tx_late_first", "own_source")
				s.Exec("GRANT own_source TO own_actor WITH INHERIT TRUE,SET TRUE")
				s.Exec("GRANT own_target TO own_actor WITH INHERIT TRUE,SET TRUE")
				s.Exec("GRANT CREATE ON SCHEMA public TO own_target")
				q := "REASSIGN OWNED BY own_source,own_other TO own_target"
				want := fixture.Diagnostic{Code: "42501", Message: "permission denied to reassign objects", Detail: "Only roles with privileges of role \"own_other\" may reassign objects owned by it."}
				if kind == "class-eligibility" {
					// Relation roots can transfer without successor namespace
					// CREATE; the later collation class independently requires it.
					s.Exec("CREATE SCHEMA own_class AUTHORIZATION own_middle")
					s.Exec("GRANT own_middle TO own_source WITH INHERIT TRUE,SET FALSE")
					s.Actor("own_source")
					s.Exec(`CREATE COLLATION own_class.coll FROM "C"`)
					s.Actor("")
					q = "REASSIGN OWNED BY own_source TO own_target"
					want = fixture.Diagnostic{Code: "42501", Message: "permission denied for schema own_class"}
				}
				x := ownershipBegin(s, route, q)
				defer x.abort()
				if route != "auto" {
					x.exec("SAVEPOINT preflight")
				}
				x.exec("SET ROLE own_actor")
				failure := fixture.ImplicitError(q, want)
				failure.ErrorStage = "execute"
				x.run(failure)
				if route != "auto" {
					x.run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					x.exec("ROLLBACK TO preflight")
				} else {
					x.exec("RESET ROLE")
				}
				x.run(fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.relname='tx_late_first'", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_source"}}))
				x.run(fixture.Select("SELECT id FROM tx_late_first", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				x.end(false)
				ownershipOwner(s, "tx_late_first", "own_source")
			})
		})
	}
}

func TestReferenceRoleOwnershipTransactionalReadmission(t *testing.T) {
	ownershipTransactionCases(t, func(s *fixture.RoleMembershipScenario, route string) {
		ownershipSeed(s, "tx_fresh_wait", "own_source")
		s.Exec("GRANT own_source TO own_actor WITH INHERIT TRUE,SET TRUE")
		s.Exec("GRANT own_target TO own_actor WITH INHERIT FALSE,SET TRUE")
		s.Exec("GRANT CREATE ON SCHEMA public TO own_target")
		holder, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer holder.Close()
		var oid int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT 'tx_fresh_wait'::regclass::oid::BIGINT").Scan(&oid); err != nil {
			s.T.Fatal(err)
		}
		waiterPID, holderPID := 0, 0
		if s.Backend.PG {
			waiterPID = fixture.LockPID(s.T, s.P.Ctx, s.C)
			holderPID = fixture.LockPID(s.T, s.P.Ctx, holder)
		}
		fixture.LockExec(s.T, s.P.Ctx, holder, "BEGIN", 0)
		defer holder.ExecContext(s.P.Ctx, "ROLLBACK")
		fixture.LockExec(s.T, s.P.Ctx, holder, "LOCK TABLE tx_fresh_wait IN ACCESS SHARE MODE", 0)
		q := "ALTER TABLE tx_fresh_wait OWNER TO own_target"
		x := ownershipBegin(s, route, q)
		defer x.abort()
		if route != "auto" {
			x.exec("SAVEPOINT before_wait")
		}
		x.exec("SET ROLE own_actor")
		ctx, cancel := context.WithCancel(s.P.Ctx)
		defer cancel()
		finished := make(chan struct{})
		join := fixture.LockOperation(s.T, ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(finished)
			var out fixture.RelationOutcome
			if stmt := x.held[q]; stmt != nil {
				out.Result, out.Err = stmt.ExecContext(ctx)
			} else {
				out.Result, out.Err = x.e.ExecContext(ctx, q)
			}
			return out
		}, 10*time.Second)
		joined := false
		defer func() {
			cancel()
			holder.ExecContext(s.P.Ctx, "ROLLBACK")
			if !joined {
				join()
			}
		}()
		fixture.RelationLockPending(s.T, s.P, s.Backend, waiterPID, holderPID, oid, "AccessExclusiveLock", finished)
		fixture.RunStep(s.T, s.P.Ctx, s.Backend.DB, s.Record, fixture.Exec("REVOKE own_source FROM own_actor", 0))
		fixture.LockExec(s.T, s.P.Ctx, holder, "ROLLBACK", 0)
		out := join()
		joined = true
		if out.Result != nil {
			s.T.Fatal("denied admitted waiter returned result", out.Result)
		}
		d := fixture.ControlsReferenceDiagnostic("42501", "must be owner of table tx_fresh_wait")
		fixture.AssertDiagnostic(s.T, out.Err, &d)
		if route != "auto" {
			x.exec("ROLLBACK TO before_wait")
		} else {
			x.exec("RESET ROLE")
		}
		x.exec(q)
		x.end(true)
		ownershipOwner(s, "tx_fresh_wait", "own_target")
		ownershipRows(s, "tx_fresh_wait")
	})
}
