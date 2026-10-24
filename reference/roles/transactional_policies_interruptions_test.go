package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalPolicyInterruptions(t *testing.T) {
	for _, recovery := range []string{"cancel_rollback_to", "cancel_rollback", "timeout_dispose"} {
		t.Run("explicit_interruption/"+recovery, func(t *testing.T) {
			transactionalPolicyCases(t, func(s *transactionalPolicies) {
				s.exec("CREATE POLICY p ON t TO tp_reader USING(id<=2)")
				s.exec("CREATE POLICY p ON u TO tp_reader USING(id=1)")
				s.run(fixture.Exec("INSERT INTO u VALUES(1,TRUE),(2,FALSE)", 2))
				tOID, uOID := s.oid("t", "p"), s.oid("u", "p")
				pid := transactionalPolicyLockIdentity(s, s.c)
				var uRelation int64
				if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'u'::regclass::oid").Scan(&uRelation); err != nil {
					t.Fatal(err)
				}
				peer := *s
				peer.x = s.peer
				peer.exec("BEGIN")
				peer.exec("LOCK TABLE t IN ACCESS SHARE MODE")
				s.exec("BEGIN")
				// Prior private work uses u: touching t here would already own AX
				// and prevent the independent AS blocker from being established.
				s.ddl("ALTER POLICY p ON u USING(id=2)")
				s.run(fixture.Exec("INSERT INTO u VALUES(4,TRUE)", 1))
				s.exec("SAVEPOINT before_wait")
				const q = "ALTER POLICY p ON t USING(FALSE)"
				h, err := s.c.PrepareContext(s.p.Ctx, q)
				if err != nil {
					t.Fatal("quiet held policy Prepare", err)
				}
				defer h.Close()
				if recovery == "timeout_dispose" {
					s.exec("SET lock_timeout='100ms'")
				}
				ctx, cancel := context.WithCancel(s.p.Ctx)
				defer cancel()
				done := make(chan error, 1)
				joined := false
				defer func() {
					cancel()
					s.peer.ExecContext(s.p.Ctx, "ROLLBACK")
					if !joined {
						<-done
					}
				}()
				go policyLockExecute(ctx, s.c, h, q, done)
				if recovery != "timeout_dispose" {
					if s.b.PG {
						fixture.LockWait(t, s.p.Ctx, pid)
						var canceled bool
						err = fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled)
						if err != nil || !canceled {
							t.Fatal(canceled, err)
						}
					} else {
						policyWaitNative(t, ctx, done)
						cancel()
					}
				}
				err = <-done
				joined = true
				if recovery == "timeout_dispose" {
					// The exact lock-timeout outcome proves target admission. Do not
					// race a server observer against the short timer.
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"})
				} else if s.b.PG {
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
				} else if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				// Lock-free failed-block probe; never SHOW/RESET an armed timer.
				s.failure("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block")
				peer.exec("ROLLBACK")

				// An independent healthy follower queues behind the prior private
				// policy lock, then sees exactly the chosen recovery's publication.
				follower, err := s.b.DB.Conn(s.p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer follower.Close()
				f := *s
				f.x = follower
				f.exec("SET ROLE tp_reader")
				waiter := transactionalPolicyLockIdentity(s, follower)
				readCtx, stopRead := context.WithCancel(s.p.Ctx)
				defer stopRead()
				readDone := make(chan struct{})
				join := fixture.LockOperation(t, readCtx, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := follower.QueryContext(ctx, "SELECT id FROM u ORDER BY id")
					close(readDone)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				readJoined := false
				defer func() {
					stopRead()
					if !readJoined {
						o := join()
						if o.Rows != nil {
							o.Rows.Close()
						}
					}
				}()
				fixture.RelationLockPending(t, s.p, s.b, waiter, pid, uRelation, "AccessShareLock", readDone)
				if recovery == "timeout_dispose" {
					h.Close()
					if err := s.c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
						t.Fatal("disposing timer-armed transaction owner", err)
					}
				} else {
					if recovery == "cancel_rollback_to" {
						s.exec("ROLLBACK TO before_wait")
						s.sameOID("t", "p", tOID)
						s.sameOID("u", "p", uOID)
						s.ids(1, 2)
						s.exec("SET ROLE tp_reader")
						s.run(fixture.Select("SELECT id FROM u ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
						s.exec("RESET ROLE")
						s.run(fixture.Select("SELECT id FROM u ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(4)}}))
						s.exec("RELEASE before_wait")
						s.exec("COMMIT")
					} else {
						s.exec("ROLLBACK")
					}
					// Server cancellation/native context cancellation has completed;
					// the original physical owner is healthy after recovery.
					s.run(fixture.Select("SELECT 1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}}))
				}
				o := join()
				readJoined = true
				if o.Err != nil {
					t.Fatal(o.Err)
				}
				defer o.Rows.Close()
				want := int64(1)
				if recovery == "cancel_rollback_to" {
					want = 2
				}
				var got int64
				if !o.Rows.Next() {
					t.Fatal("missing follower row", o.Rows.Err())
				}
				if err := o.Rows.Scan(&got); err != nil || got != want {
					t.Fatal("follower row", got, want, err)
				}
				if o.Rows.Next() || o.Rows.Err() != nil {
					t.Fatal("follower tail", o.Rows.Err())
				}
				o.Rows.Close()
				f.exec("RESET ROLE")
				// Observe both identities and real eligible-role behavior BEFORE
				// any successful ALTER can overwrite the interrupted candidate.
				peer.sameOID("t", "p", tOID)
				peer.sameOID("u", "p", uOID)
				peer.ids(1, 2)
				peer.exec("SET ROLE tp_reader")
				peer.run(fixture.Select("SELECT id FROM u ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{want}}))
				peer.exec("RESET ROLE")
				rows := [][]any{{int64(1)}, {int64(2)}}
				if recovery == "cancel_rollback_to" {
					rows = append(rows, []any{int64(4)})
				}
				peer.run(fixture.Select("SELECT id FROM u ORDER BY id", []string{"id"}, []string{"INT4"}, rows))
				peer.ddl("ALTER POLICY p ON t USING(id=2)")
				peer.ids(2)
			})
		})
	}
}
