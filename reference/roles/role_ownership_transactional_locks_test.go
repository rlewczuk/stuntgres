package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipTransactionalLocks(t *testing.T) {
	for _, family := range []struct{ name, create, alter, relation string }{
		{"table", "CREATE TABLE tx_lock_root(id INT)", "ALTER TABLE tx_lock_root OWNER TO own_target", "tx_lock_root"},
		{"view", "CREATE VIEW tx_lock_root AS SELECT 7::INT AS id", "ALTER VIEW tx_lock_root OWNER TO own_target", "tx_lock_root"},
		{"materialized", "CREATE MATERIALIZED VIEW tx_lock_root AS SELECT 7::INT AS id", "ALTER MATERIALIZED VIEW tx_lock_root OWNER TO own_target", "tx_lock_root"},
		{"reassign", "CREATE TABLE tx_lock_root(id INT)", "REASSIGN OWNED BY own_source TO own_target", "tx_lock_root"},
		{"drop", "CREATE TABLE tx_lock_root(id INT)", "DROP OWNED BY own_source CASCADE", "tx_lock_root"},
	} {
		t.Run(family.name, func(t *testing.T) {
			for _, failure := range []string{"cancel", "timeout"} {
				t.Run(failure, func(t *testing.T) {
					ownershipTransactionCases(t, func(s *membershipScenario, route string) {
						ownershipDefinition(s, family.create)
						seedOwner := "ALTER TABLE tx_lock_root OWNER TO own_source"
						switch family.name {
						case "view":
							seedOwner = "ALTER VIEW tx_lock_root OWNER TO own_source"
						case "materialized":
							seedOwner = "ALTER MATERIALIZED VIEW tx_lock_root OWNER TO own_source"
						}
						s.exec(seedOwner)
						if family.name == "table" || family.name == "reassign" || family.name == "drop" {
							s.run(fixture.Exec("INSERT INTO tx_lock_root VALUES(7)", 1))
						}
						s.exec("CREATE TABLE tx_lock_unrelated(id INT)")
						holder, err := s.backend.DB.Conn(s.p.Ctx)
						if err != nil {
							s.t.Fatal(err)
						}
						defer holder.Close()
						var oid int64
						if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'tx_lock_root'::regclass::oid::BIGINT").Scan(&oid); err != nil || oid <= 0 {
							s.t.Fatal(oid, err)
						}
						waiterPID, holderPID := 0, 0
						if s.backend.PG {
							waiterPID = fixture.LockPID(s.t, s.p.Ctx, s.c)
							holderPID = fixture.LockPID(s.t, s.p.Ctx, holder)
						}
						// Preparing must not wait behind an existing AccessShare holder.
						fixture.LockExec(s.t, s.p.Ctx, holder, "BEGIN", 0)
						defer holder.ExecContext(s.p.Ctx, "ROLLBACK")
						// A completed SELECT (with rows drained/closed by RunStep)
						// retains AccessShare until transaction end, including on MVs.
						fixture.RunStep(s.t, s.p.Ctx, holder, s.record, fixture.Select("SELECT id FROM tx_lock_root", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
						x := ownershipBegin(s, route, family.alter)
						defer x.abort()
						if route != "auto" {
							x.exec("SAVEPOINT lock_mark")
						}
						if failure == "timeout" {
							x.exec("SET lock_timeout='10ms'")
						}
						ctx, cancel := context.WithCancel(s.p.Ctx)
						defer cancel()
						finished := make(chan struct{})
						join := fixture.LockOperation(s.t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							var out fixture.RelationOutcome
							if stmt := x.held[family.alter]; stmt != nil {
								out.Result, out.Err = stmt.ExecContext(ctx)
							} else {
								out.Result, out.Err = x.e.ExecContext(ctx, family.alter)
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
						unrelated := fixture.Exec("INSERT INTO tx_lock_unrelated VALUES(9)", 1)
						if failure == "cancel" {
							fixture.RelationLockPending(s.t, s.p, s.backend, waiterPID, holderPID, oid, "AccessExclusiveLock", finished)
							// Independent committed work while the real command is parked.
							fixture.RunStep(s.t, s.p.Ctx, s.backend.DB, s.record, unrelated)
							if s.backend.PG {
								var canceled bool
								if err := fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT pg_cancel_backend($1)", waiterPID).Scan(&canceled); err != nil || !canceled {
									s.t.Fatal(canceled, err)
								}
							} else {
								cancel()
							}
						}
						out := join()
						joined = true
						if out.Result != nil {
							s.t.Fatal("failed waiter returned result", out.Result)
						}
						if failure == "timeout" {
							// The exact lock-timeout outcome proves the waiter queued
							// behind the holder; never race an observer against the
							// short timer. The cancel branch covers work while parked.
							d := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
							fixture.AssertDiagnostic(s.t, out.Err, &d)
							fixture.RunStep(s.t, s.p.Ctx, s.backend.DB, s.record, unrelated)
						} else if s.backend.PG {
							d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
							fixture.AssertDiagnostic(s.t, out.Err, &d)
						} else if !errors.Is(out.Err, context.Canceled) {
							s.t.Fatal("ordinary native cancellation", out.Err)
						}
						fixture.LockExec(s.t, s.p.Ctx, holder, "ROLLBACK", 0)
						if route != "auto" {
							x.run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
							if failure == "cancel" {
								x.exec("ROLLBACK TO lock_mark")
								x.exec("RELEASE lock_mark")
							}
						}
						if failure == "timeout" {
							// The timeout-armed physical owner is disposable. Do not
							// reset its timer or issue a successful retry through it.
							x.abort()
							if err := s.c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
								s.t.Fatal("dispose timeout owner", err)
							}
							s.c.Close()
							_, fresh, record := fixture.NoticeReferencePool(s.t, s.p, s.backend.PG, s.notices)
							s.c, s.record = fresh, record
							ownershipOwner(s, "tx_lock_root", "own_source")
							recovered := ownershipBegin(s, route, family.alter)
							defer recovered.abort()
							recovered.exec(family.alter)
							recovered.end(true)
						} else {
							// Cancellation recovery deliberately retains the same owner.
							x.exec(family.alter)
							x.end(true)
						}
						ownershipSelect(s, "SELECT id FROM tx_lock_unrelated", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
						if family.name == "drop" {
							ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='tx_lock_root'", 0)
						} else {
							ownershipOwner(s, "tx_lock_root", "own_target")
						}
						// Fresh follower requires the command's retained AX locks to be gone.
						fixture.LockExec(s.t, s.p.Ctx, holder, "BEGIN", 0)
						if family.name != "drop" {
							fixture.RunStep(s.t, s.p.Ctx, holder, s.record, fixture.Select("SELECT id FROM tx_lock_root", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
						}
						fixture.LockExec(s.t, s.p.Ctx, holder, "ROLLBACK", 0)
					})
				})
			}
		})
	}
}

func TestReferenceRoleOwnershipTransactionalPolicyLocks(t *testing.T) {
	for _, end := range []string{"mark", "rollback", "commit", "cancel", "timeout"} {
		t.Run(end, func(t *testing.T) {
			ownershipCases(t, func(s *membershipScenario) {
				// No owned object root exists: only actual sole-target policy
				// deletion requires AX on the surviving relation. Mixed/PUBLIC
				// policies and an ACL-only survivor must not gain blanket AX.
				for _, name := range []string{"tx_policy_partial", "tx_policy_acl", "tx_policy_probe"} {
					ownershipSeed(s, name, "own_other")
				}
				for _, q := range []string{
					"GRANT SELECT ON tx_policy_partial,tx_policy_probe TO PUBLIC",
					"GRANT SELECT ON tx_policy_acl TO own_source",
					"CREATE POLICY tx_partial_mixed ON tx_policy_partial TO own_source,own_reader,own_source USING(id>0)",
					"ALTER TABLE tx_policy_partial ENABLE ROW LEVEL SECURITY,FORCE ROW LEVEL SECURITY",
					"CREATE POLICY tx_policy_mixed ON tx_policy_probe TO own_source,own_reader,own_source USING(id>0)",
					"CREATE POLICY tx_policy_source ON tx_policy_probe TO own_source USING(id>0)",
					"CREATE POLICY tx_policy_alt ON tx_policy_probe TO own_alt USING(id>0)",
					"CREATE POLICY tx_policy_public ON tx_policy_probe TO PUBLIC USING(false)",
					"ALTER TABLE tx_policy_probe ENABLE ROW LEVEL SECURITY,FORCE ROW LEVEL SECURITY",
				} {
					s.exec(q)
				}
				ownershipCount(s, "SELECT count(*) FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE r.rolname IN('own_source','own_alt')", 0)
				const inventory = "SELECT policyname,roles::TEXT FROM pg_policies WHERE tablename='tx_policy_probe' ORDER BY policyname"
				var before [][]any
				if s.backend.PG {
					before = ownershipTransactionSnapshot(s, s.c, inventory)
					if len(before) != 4 {
						s.t.Fatal("complete sole/mixed/PUBLIC policy inventory", before)
					}
				}
				state := func(e fixture.Executor, cleaned bool) {
					for _, actor := range []string{"own_source", "own_alt", "own_reader", "own_downstream"} {
						fixture.LockExec(s.t, s.p.Ctx, e, "SET ROLE "+actor, 0)
						var want [][]any
						if actor == "own_reader" || !cleaned && actor != "own_downstream" {
							want = [][]any{{int64(7)}}
						}
						fixture.RunStep(s.t, s.p.Ctx, e, s.record, fixture.Select("SELECT id FROM tx_policy_probe", []string{"id"}, []string{"INT4"}, want))
						fixture.LockExec(s.t, s.p.Ctx, e, "RESET ROLE", 0)
					}
				}
				state(s.c, false)
				peer, err := s.backend.DB.Conn(s.p.Ctx)
				if err != nil {
					s.t.Fatal(err)
				}
				defer peer.Close()
				var oid int64
				if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'tx_policy_probe'::regclass::oid::BIGINT").Scan(&oid); err != nil || oid <= 0 {
					s.t.Fatal(oid, err)
				}
				ownerPID, peerPID := 0, 0
				if s.backend.PG {
					ownerPID = fixture.LockPID(s.t, s.p.Ctx, s.c)
					peerPID = fixture.LockPID(s.t, s.p.Ctx, peer)
				}
				fixture.LockExec(s.t, s.p.Ctx, peer, "SET ROLE own_source", 0)
				fixture.LockExec(s.t, s.p.Ctx, peer, "BEGIN", 0)
				defer peer.ExecContext(s.p.Ctx, "ROLLBACK")
				fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Select("SELECT id FROM tx_policy_probe", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				const cleanup = "DROP OWNED BY own_source,own_alt,own_source CASCADE"
				x := ownershipBegin(s, "sql-held-before", cleanup)
				defer x.abort()
				x.exec("SAVEPOINT policy_mark")
				if end == "timeout" {
					x.exec("SET lock_timeout='10ms'")
				}
				ctx, cancel := context.WithCancel(s.p.Ctx)
				defer cancel()
				finished := make(chan struct{})
				join := fixture.LockOperation(s.t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					var out fixture.RelationOutcome
					if stmt := x.held[cleanup]; stmt != nil {
						out.Result, out.Err = stmt.ExecContext(ctx)
					} else {
						out.Result, out.Err = x.e.ExecContext(ctx, cleanup)
					}
					return out
				})
				joined := false
				defer func() {
					cancel()
					peer.ExecContext(s.p.Ctx, "ROLLBACK")
					if !joined {
						join()
					}
				}()
				// A timeout's exact 55P03 outcome proves the wait; never race an
				// observer against the short timer.
				if end != "timeout" {
					fixture.RelationLockPending(s.t, s.p, s.backend, ownerPID, peerPID, oid, "AccessExclusiveLock", finished)
				}
				if end == "cancel" {
					if s.backend.PG {
						var canceled bool
						if err := fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT pg_cancel_backend($1)", ownerPID).Scan(&canceled); err != nil || !canceled {
							s.t.Fatal(canceled, err)
						}
					} else {
						cancel()
					}
				}
				if end != "cancel" && end != "timeout" {
					fixture.LockExec(s.t, s.p.Ctx, peer, "ROLLBACK", 0)
				}
				out := join()
				joined = true
				if end == "cancel" || end == "timeout" {
					if out.Result != nil {
						s.t.Fatal("failed policy deletion exposed a result")
					}
					if end == "timeout" {
						d := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						fixture.AssertDiagnostic(s.t, out.Err, &d)
					} else if s.backend.PG {
						d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						fixture.AssertDiagnostic(s.t, out.Err, &d)
					} else if !errors.Is(out.Err, context.Canceled) {
						s.t.Fatal(out.Err)
					}
					fixture.LockExec(s.t, s.p.Ctx, peer, "ROLLBACK", 0)
					x.run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					if end == "timeout" {
						x.abort()
						if err := s.c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
							s.t.Fatal("dispose timeout owner", err)
						}
						s.c.Close()
						_, fresh, record := fixture.NoticeReferencePool(s.t, s.p, s.backend.PG, s.notices)
						s.c, s.record = fresh, record
					} else {
						x.exec("ROLLBACK TO policy_mark")
						x.exec("RELEASE policy_mark")
						x.end(true)
					}
					state(s.c, false)
					if s.backend.PG {
						ownershipTransactionSnapshotEqual(s, s.c, inventory, before)
					}
					return
				}
				if out.Err != nil {
					s.t.Fatal(out.Err)
				}
				fixture.LockAffected(s.t, out.Result, 0)
				state(x.e, true)
				if s.backend.PG {
					ownershipTransactionSnapshotEqual(s, peer, inventory, before)
				}
				// Partial-target and ACL-only cleanup does not prevent the
				// ordinary peer from reading its still-committed authority.
				for _, table := range []string{"tx_policy_partial", "tx_policy_acl"} {
					read := fixture.LockOperation(s.t, s.p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						return fixture.RowQuery(ctx, peer, "SELECT id AS v FROM "+table, s.prepared, s.backend.Recorder.RawBackend)
					})
					fixture.RowValues(s.t, read(), 7)
				}
				peerFinished := make(chan struct{})
				peerContext, peerCancel := context.WithCancel(s.p.Ctx)
				defer peerCancel()
				peerJoin := fixture.LockOperation(s.t, peerContext, func(ctx context.Context) fixture.RelationOutcome {
					defer close(peerFinished)
					return fixture.RowQuery(ctx, peer, "SELECT id AS v FROM tx_policy_probe", s.prepared, s.backend.Recorder.RawBackend)
				})
				peerJoined := false
				defer func() {
					peerCancel()
					if !peerJoined {
						pending := peerJoin()
						if pending.Rows != nil {
							pending.Rows.Close()
						}
						if pending.Stmt != nil {
							pending.Stmt.Close()
						}
					}
				}()
				fixture.RelationLockPending(s.t, s.p, s.backend, peerPID, ownerPID, oid, "AccessShareLock", peerFinished)
				if end == "mark" {
					x.exec("ROLLBACK TO policy_mark")
				} else {
					x.end(end == "commit")
				}
				read := peerJoin()
				peerJoined = true
				if end == "commit" {
					fixture.RowValues(s.t, read)
				} else {
					fixture.RowValues(s.t, read, 7)
				}
				peerCancel()
				if end == "mark" {
					state(x.e, false)
					x.exec("ROLLBACK TO policy_mark")
					x.exec("RELEASE policy_mark")
					x.exec(cleanup)
					state(x.e, true)
					x.end(true)
				}
				committed := end != "rollback"
				state(s.c, committed)
				fixture.LockExec(s.t, s.p.Ctx, peer, "RESET ROLE", 0)
				state(peer, committed)
				ownershipOwner(s, "tx_policy_probe", "own_other")
				ownershipRows(s, "tx_policy_probe")
				if s.backend.PG {
					if committed {
						ownershipSelect(s, inventory, []string{"policyname", "roles"}, []string{"NAME", "TEXT"}, [][]any{{"tx_policy_mixed", "{own_reader}"}, {"tx_policy_public", "{public}"}})
					} else {
						ownershipTransactionSnapshotEqual(s, s.c, inventory, before)
					}
				}
			})
		})
	}
}
