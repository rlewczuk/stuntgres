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
			ownershipTransactionCases(t, func(s *membershipScenario, route string) {
				ownershipSeed(s, "tx_late_first", "own_source")
				s.exec("GRANT own_source TO own_actor WITH INHERIT TRUE,SET TRUE")
				s.exec("GRANT own_target TO own_actor WITH INHERIT TRUE,SET TRUE")
				s.exec("GRANT CREATE ON SCHEMA public TO own_target")
				q := "REASSIGN OWNED BY own_source,own_other TO own_target"
				want := fixture.Diagnostic{Code: "42501", Message: "permission denied to reassign objects", Detail: "Only roles with privileges of role \"own_other\" may reassign objects owned by it."}
				if kind == "class-eligibility" {
					// Relation roots can transfer without successor namespace
					// CREATE; the later collation class independently requires it.
					s.exec("CREATE SCHEMA own_class AUTHORIZATION own_middle")
					s.exec("GRANT own_middle TO own_source WITH INHERIT TRUE,SET FALSE")
					s.actor("own_source")
					s.exec(`CREATE COLLATION own_class.coll FROM "C"`)
					s.actor("")
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
	ownershipTransactionCases(t, func(s *membershipScenario, route string) {
		ownershipSeed(s, "tx_fresh_wait", "own_source")
		s.exec("GRANT own_source TO own_actor WITH INHERIT TRUE,SET TRUE")
		s.exec("GRANT own_target TO own_actor WITH INHERIT FALSE,SET TRUE")
		s.exec("GRANT CREATE ON SCHEMA public TO own_target")
		holder, err := s.backend.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer holder.Close()
		var oid int64
		if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'tx_fresh_wait'::regclass::oid::BIGINT").Scan(&oid); err != nil {
			s.t.Fatal(err)
		}
		waiterPID, holderPID := 0, 0
		if s.backend.PG {
			waiterPID = fixture.LockPID(s.t, s.p.Ctx, s.c)
			holderPID = fixture.LockPID(s.t, s.p.Ctx, holder)
		}
		fixture.LockExec(s.t, s.p.Ctx, holder, "BEGIN", 0)
		defer holder.ExecContext(s.p.Ctx, "ROLLBACK")
		fixture.LockExec(s.t, s.p.Ctx, holder, "LOCK TABLE tx_fresh_wait IN ACCESS SHARE MODE", 0)
		q := "ALTER TABLE tx_fresh_wait OWNER TO own_target"
		x := ownershipBegin(s, route, q)
		defer x.abort()
		if route != "auto" {
			x.exec("SAVEPOINT before_wait")
		}
		x.exec("SET ROLE own_actor")
		ctx, cancel := context.WithCancel(s.p.Ctx)
		defer cancel()
		finished := make(chan struct{})
		join := fixture.LockOperation(s.t, ctx, func(ctx context.Context) fixture.RelationOutcome {
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
			holder.ExecContext(s.p.Ctx, "ROLLBACK")
			if !joined {
				join()
			}
		}()
		fixture.RelationLockPending(s.t, s.p, s.backend, waiterPID, holderPID, oid, "AccessExclusiveLock", finished)
		fixture.RunStep(s.t, s.p.Ctx, s.backend.DB, s.record, fixture.Exec("REVOKE own_source FROM own_actor", 0))
		fixture.LockExec(s.t, s.p.Ctx, holder, "ROLLBACK", 0)
		out := join()
		joined = true
		if out.Result != nil {
			s.t.Fatal("denied admitted waiter returned result", out.Result)
		}
		d := fixture.ControlsReferenceDiagnostic("42501", "must be owner of table tx_fresh_wait")
		fixture.AssertDiagnostic(s.t, out.Err, &d)
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
