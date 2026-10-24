package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalRulesLocks(t *testing.T) {
	for _, q := range []string{transactionalRulesCreate, transactionalRulesReplace, transactionalRulesRename, transactionalRulesDrop} {
		for _, action := range []string{"release", "cancel", "timeout"} {
			t.Run(q+"/"+action, func(t *testing.T) {
				transactionalRulesRoutes(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, holder *sql.Conn, mode string, held bool) {
					base := transactionalRulesRun(t, p, b, c, nil)
					transactionalRulesSetup(base)
					if q != transactionalRulesCreate {
						base(fixture.Exec(transactionalRulesCreate, 0))
					}
					if q == transactionalRulesDrop {
						base(fixture.Exec(transactionalRulesRename, 0))
					}
					handles := transactionalRulesHandles(t, p, c, held, q)
					pid, hpid := 0, 0
					var oid int64
					if b.PG {
						pid = fixture.LockPID(t, p.Ctx, c)
						hpid = fixture.LockPID(t, p.Ctx, holder)
						oid = fixture.LockOID(t, p.Ctx, "src")
					}
					_, finish := transactionalViewsBegin(t, p, b, c, mode)
					ended := mode == "autocommit"
					retired := false
					defer func() {
						if !ended {
							finish(false)
						}
						if action == "timeout" && !retired {
							c.Raw(func(any) error { return driver.ErrBadConn })
						}
					}()
					htx := fixture.QueryLockBegin(t, p.Ctx, holder)
					defer htx.Rollback()
					fixture.RunStep(t, p.Ctx, htx, b.Recorder, fixture.Exec("LOCK TABLE src IN ACCESS SHARE MODE", 0))
					if action == "timeout" {
						base(fixture.Exec("SET lock_timeout='100ms'", 0))
					}
					var x fixture.Executor = c
					if stmt := handles[q]; stmt != nil {
						x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					done := make(chan struct{})
					join := fixture.LockOperation(t, p.Ctx, func(context.Context) fixture.RelationOutcome {
						defer close(done)
						r, err := x.ExecContext(ctx, q)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					if action != "timeout" {
						fixture.RelationLockPending(t, p, b, pid, hpid, oid, "AccessExclusiveLock", done)
						if action == "release" {
							if err := htx.Rollback(); err != nil {
								t.Fatal(err)
							}
						} else if b.PG {
							var ok bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ok); err != nil || !ok {
								t.Fatal(ok, err)
							}
						} else {
							cancel()
						}
					}
					out := join()
					if action == "release" {
						if out.Err != nil {
							t.Fatal(out.Err)
						}
						n, err := out.Result.RowsAffected()
						if err != nil || n != 0 {
							t.Fatal(n, err)
						}
						finish(true)
						ended = true
					} else {
						if action == "timeout" {
							want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
							fixture.AssertDiagnostic(t, out.Err, &want)
						} else if b.PG {
							want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
							fixture.AssertDiagnostic(t, out.Err, &want)
						} else if !errors.Is(out.Err, context.Canceled) {
							t.Fatal(out.Err)
						}
						finish(false)
						ended = true
						if err := htx.Rollback(); err != nil {
							t.Fatal(err)
						}
						// Armed owners are retired, not reset or reused. The follower proves no
						// queued request or contribution survives cancellation/transaction end.
						if action == "timeout" {
							c.Raw(func(any) error { return driver.ErrBadConn })
							retired = true
						}
						observe := transactionalRulesRun(t, p, b, holder, nil)
						names := []string{"r"}
						if q == transactionalRulesCreate {
							names = nil
						}
						if q == transactionalRulesDrop {
							names = []string{"renamed"}
						}
						observe(transactionalRulesNames(names...))
						observe(transactionalRulesRows("src"))
						observe(transactionalRulesRows("sink1"))
						observe(transactionalRulesRows("sink2"))
						observe(fixture.Exec(q, 0))
					}
					observe := transactionalRulesRun(t, p, b, holder, nil)
					observe(fixture.Exec("INSERT INTO src VALUES(1,-10)", 1))
					var a, z [][]any
					if q == transactionalRulesReplace {
						z = [][]any{{int64(1), int64(-9)}}
					} else if q != transactionalRulesDrop {
						a = [][]any{{int64(1), int64(10)}}
					}
					observe(transactionalRulesRows("sink1", a...))
					observe(transactionalRulesRows("sink2", z...))
				})
			})
		}
	}
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "peer-after-rollback", true: "peer-after-commit"}[commit], func(t *testing.T) {
			transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				base := transactionalRulesRun(t, p, b, c, nil)
				transactionalRulesSetup(base)
				base(fixture.Exec(transactionalRulesCreate, 0))
				base(fixture.Exec("CREATE TABLE unrelated(id INT)", 0))
				pid, hpid := 0, 0
				var oid int64
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, peer)
					hpid = fixture.LockPID(t, p.Ctx, c)
					oid = fixture.LockOID(t, p.Ctx, "src")
				}
				handles := transactionalRulesHandles(t, p, c, held, transactionalRulesReplace)
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := transactionalRulesRun(t, p, b, e, handles)
				run(fixture.Exec(transactionalRulesReplace, 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalRulesEdge("sink1", 1))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO unrelated VALUES(99)", 1))
				done := make(chan struct{})
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(done)
					r, err := peer.ExecContext(ctx, "INSERT INTO src VALUES(1,20)")
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				fixture.RelationLockPending(t, p, b, pid, hpid, oid, "RowExclusiveLock", done)
				finish(commit)
				out := join()
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				n, err := out.Result.RowsAffected()
				if err != nil || n != 1 {
					t.Fatal(n, err)
				}
				var a, z [][]any
				if commit {
					z = [][]any{{int64(1), int64(21)}}
				} else {
					a = [][]any{{int64(1), int64(20)}}
				}
				base(transactionalRulesRows("sink1", a...))
				base(transactionalRulesRows("sink2", z...))
				base(fixture.Select("SELECT id FROM unrelated", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
			})
		})
	}
	t.Run("competing-definitions", func(t *testing.T) {
		transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
			base := transactionalRulesRun(t, p, b, c, nil)
			transactionalRulesSetup(base)
			base(fixture.Exec(transactionalRulesCreate, 0))
			pid, hpid := 0, 0
			var oid int64
			if b.PG {
				pid = fixture.LockPID(t, p.Ctx, peer)
				hpid = fixture.LockPID(t, p.Ctx, c)
				oid = fixture.LockOID(t, p.Ctx, "src")
			}
			e, finish := transactionalViewsBegin(t, p, b, c, mode)
			run := transactionalRulesRun(t, p, b, e, nil)
			run(fixture.Exec(transactionalRulesReplace, 0))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("ALTER RULE r ON other RENAME TO safe", 0))
			done := make(chan struct{})
			join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(done)
				r, err := peer.ExecContext(ctx, "ALTER RULE r ON src RENAME TO second")
				return fixture.RelationOutcome{Result: r, Err: err}
			})
			fixture.RelationLockPending(t, p, b, pid, hpid, oid, "AccessExclusiveLock", done)
			finish(true)
			if out := join(); out.Err != nil {
				t.Fatal(out.Err)
			}
			base(transactionalRulesNames("second"))
			base(fixture.Exec("INSERT INTO src VALUES(1,20)", 1))
			base(transactionalRulesRows("sink2", []any{int64(1), int64(21)}))
			base(fixture.Select("SELECT rulename FROM pg_rewrite WHERE ev_class='other'::regclass", []string{"rulename"}, []string{"NAME"}, [][]any{{"safe"}}))
		})
	})
}
