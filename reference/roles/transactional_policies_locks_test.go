package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalPolicyLockIdentity(s *transactionalPolicies, c *sql.Conn) int {
	if s.b.PG {
		return fixture.LockPID(s.t, s.p.Ctx, c)
	}
	return 0
}
func transactionalPolicyRelation(s *transactionalPolicies) int64 {
	var oid int64
	if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 't'::regclass::oid").Scan(&oid); err != nil {
		s.t.Fatal(err)
	}
	return oid
}

func TestReferenceTransactionalPoliciesLocks(t *testing.T) {
	transactionalPolicyInterruptions(t)
	for _, ending := range []string{"commit", "rollback", "rollback_to"} {
		t.Run("peer_read/"+ending, func(t *testing.T) {
			transactionalPolicyCases(t, func(s *transactionalPolicies) {
				s.exec("CREATE POLICY p ON t TO tp_reader USING(id<=2)")
				relation := transactionalPolicyRelation(s)
				holder, waiter := transactionalPolicyLockIdentity(s, s.c), transactionalPolicyLockIdentity(s, s.peer)
				fixture.RunStep(t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("SET ROLE tp_reader", 0))
				s.exec("BEGIN")
				s.exec("SAVEPOINT before_policy")
				s.ddl("ALTER POLICY p ON t USING(id=2)")
				s.ids(2)
				// Unrelated committed work must survive the combined owner commit.
				fixture.RunStep(t, s.p.Ctx, s.b.DB, s.b.Recorder, fixture.Exec("INSERT INTO u VALUES(9,TRUE)", 1))
				done := make(chan struct{})
				ctx, cancel := context.WithCancel(s.p.Ctx)
				defer cancel()
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := s.peer.QueryContext(ctx, "SELECT id FROM t ORDER BY id")
					close(done)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				joined := false
				defer func() {
					cancel()
					s.c.ExecContext(s.p.Ctx, "ROLLBACK")
					if !joined {
						o := join()
						if o.Rows != nil {
							o.Rows.Close()
						}
					}
				}()
				fixture.RelationLockPending(t, s.p, s.b, waiter, holder, relation, "AccessShareLock", done)
				if ending == "rollback_to" {
					s.exec("ROLLBACK TO before_policy")
				} else if ending == "commit" {
					s.exec("COMMIT")
				} else {
					s.exec("ROLLBACK")
				}
				o := join()
				joined = true
				if o.Err != nil {
					t.Fatal(o.Err)
				}
				defer o.Rows.Close()
				want := []int64{1, 2}
				if ending == "commit" {
					want = []int64{2}
				}
				cols, err := o.Rows.Columns()
				if err != nil || len(cols) != 1 || cols[0] != "id" {
					t.Fatal(cols, err)
				}
				for _, id := range want {
					var got int64
					if !o.Rows.Next() {
						t.Fatal("blocked read missing row", o.Rows.Err())
					}
					if err := o.Rows.Scan(&got); err != nil || got != id {
						t.Fatal(got, id, err)
					}
				}
				if o.Rows.Next() || o.Rows.Err() != nil {
					t.Fatal("blocked read tail", o.Rows.Err())
				}
				o.Rows.Close()
				if ending == "rollback_to" {
					s.exec("COMMIT")
				}
				s.run(fixture.Select("SELECT id FROM u", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
			})
		})
	}
	for _, ending := range []string{"commit", "rollback"} {
		t.Run("name_conflict/"+ending, func(t *testing.T) {
			transactionalPolicyCases(t, func(s *transactionalPolicies) {
				relation := transactionalPolicyRelation(s)
				holder, waiter := transactionalPolicyLockIdentity(s, s.c), transactionalPolicyLockIdentity(s, s.peer)
				s.exec("BEGIN")
				s.ddl("CREATE POLICY p ON t TO tp_reader USING(id=1)")
				done := make(chan struct{})
				ctx, cancel := context.WithCancel(s.p.Ctx)
				defer cancel()
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					r, e := s.peer.ExecContext(ctx, "CREATE POLICY p ON t TO tp_reader USING(id=2)")
					close(done)
					return fixture.RelationOutcome{Result: r, Err: e}
				})
				joined := false
				defer func() {
					cancel()
					s.c.ExecContext(s.p.Ctx, "ROLLBACK")
					if !joined {
						join()
					}
				}()
				fixture.RelationLockPending(t, s.p, s.b, waiter, holder, relation, "AccessExclusiveLock", done)
				if ending == "commit" {
					s.exec("COMMIT")
				} else {
					s.exec("ROLLBACK")
				}
				o := join()
				joined = true
				if ending == "commit" {
					fixture.AssertDiagnostic(t, o.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `policy "p" for table "t" already exists`})
					if o.Result != nil {
						t.Fatal("duplicate returned result")
					}
					s.ids(1)
				} else {
					fixture.State(t, o.Err, "")
					fixture.LockAffected(t, o.Result, 0)
					s.ids(2)
				}
			})
		})
	}
	for _, operation := range []string{"cancel", "timeout"} {
		t.Run("reader_blocks_ddl/"+operation, func(t *testing.T) {
			transactionalPolicyCases(t, func(s *transactionalPolicies) {
				s.exec("CREATE POLICY p ON t TO tp_reader USING(id<=2)")
				oid := s.oid("t", "p")
				fixture.RunStep(t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("BEGIN", 0))
				fixture.RunStep(t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("LOCK TABLE t IN ACCESS SHARE MODE", 0))
				q := "ALTER POLICY p ON t USING(FALSE)"
				h, err := s.c.PrepareContext(s.p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer h.Close()
				pid := transactionalPolicyLockIdentity(s, s.c)
				if operation == "timeout" {
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
				if operation == "cancel" {
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
				if operation == "timeout" {
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"})
				} else if s.b.PG {
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
				} else if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if operation == "timeout" {
					h.Close()
					if err := s.c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
						t.Fatal("disposing timer-armed owner", err)
					}
				}
				// Observe the predecessor before a successful ALTER can mask
				// any partial installation by the interrupted statement.
				fixture.RunStep(t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("ROLLBACK", 0))
				observer := *s
				observer.x = s.peer
				observer.sameOID("t", "p", oid)
				observer.ids(1, 2)
				fixture.RunStep(t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("ALTER POLICY p ON t USING(id=2)", 0))
				fixture.RunStep(t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("SET ROLE tp_reader", 0))
				fixture.RunStep(t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
			})
		})
	}
	t.Run("rollback_to_retains_premark_lock", func(t *testing.T) {
		transactionalPolicyCases(t, func(s *transactionalPolicies) {
			var relation int64
			if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'u'::regclass::oid").Scan(&relation); err != nil {
				t.Fatal(err)
			}
			holder, waiter := transactionalPolicyLockIdentity(s, s.c), transactionalPolicyLockIdentity(s, s.peer)
			s.exec("BEGIN")
			s.exec("LOCK TABLE u IN ACCESS SHARE MODE")
			s.exec("SAVEPOINT mark")
			s.ddl("CREATE POLICY p ON t TO tp_reader USING(TRUE)")
			s.exec("ROLLBACK TO mark")
			fixture.RunStep(t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("BEGIN", 0))
			done := make(chan struct{})
			ctx, cancel := context.WithCancel(s.p.Ctx)
			defer cancel()
			join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				r, e := s.peer.ExecContext(ctx, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE")
				close(done)
				return fixture.RelationOutcome{Result: r, Err: e}
			})
			joined := false
			defer func() {
				cancel()
				s.c.ExecContext(s.p.Ctx, "ROLLBACK")
				if !joined {
					join()
				}
			}()
			fixture.RelationLockPending(t, s.p, s.b, waiter, holder, relation, "AccessExclusiveLock", done)
			s.exec("COMMIT")
			o := join()
			joined = true
			fixture.State(t, o.Err, "")
			fixture.LockAffected(t, o.Result, 0)
			fixture.RunStep(t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("COMMIT", 0))
			s.ids()
		})
	})
	t.Run("explicit_owner_revoked_after_wait", func(t *testing.T) {
		transactionalPolicyCases(t, func(s *transactionalPolicies) {
			s.exec("CREATE POLICY p ON t TO tp_reader USING(id<=2)")
			s.exec("ALTER TABLE t OWNER TO tp_group")
			fixture.RunStep(t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("LOCK TABLE t IN ACCESS SHARE MODE", 0))
			s.exec("BEGIN")
			s.exec("SET ROLE tp_reader")
			q := "ALTER POLICY p ON t USING(FALSE)"
			h, err := s.c.PrepareContext(s.p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			pid := transactionalPolicyLockIdentity(s, s.c)
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
			if s.b.PG {
				fixture.LockWait(t, s.p.Ctx, pid)
			} else {
				policyWaitNative(t, ctx, done)
			}
			fixture.RunStep(t, s.p.Ctx, s.b.DB, s.b.Recorder, fixture.Exec("REVOKE INHERIT OPTION FOR tp_group FROM tp_reader", 0))
			fixture.RunStep(t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("COMMIT", 0))
			err = <-done
			joined = true
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table t"})
			s.exec("ROLLBACK")
			s.exec("RESET ROLE")
			s.ids(1, 2)
		})
	})
}
