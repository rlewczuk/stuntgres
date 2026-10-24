package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalRulesDefinitionPrecedence(t *testing.T) {
	cases := []struct{ name, query, position string }{
		{
			"qualification-before-action-target",
			"CREATE RULE precedence AS ON INSERT TO src WHERE NEW.absent > 0 DO INSERT INTO absent VALUES(NEW.id,NEW.v)",
			"50",
		},
		{
			"earlier-action-before-later-target",
			"CREATE RULE precedence AS ON INSERT TO src DO (INSERT INTO sink1 VALUES(NEW.id,NEW.absent); INSERT INTO absent VALUES(NEW.id,NEW.v))",
			"80",
		},
	}
	for _, tc := range cases {
		for _, blocker := range []string{"missing-target", "event", "later-target"} {
			t.Run(tc.name+"/"+blocker, func(t *testing.T) {
				transactionalRulesRoutes(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, holder *sql.Conn, mode string, held bool) {
					base := transactionalRulesRun(t, p, b, c, nil)
					transactionalRulesSetup(base)
					q := tc.query
					if blocker == "later-target" {
						q = strings.Replace(q, "INTO absent", "INTO sink2", 1)
					}
					// Preparation must succeed before either transaction or lock exists.
					handles := transactionalRulesHandles(t, p, c, held, q)
					pid, hpid := 0, 0
					var oid int64
					if b.PG && blocker == "event" {
						pid = fixture.LockPID(t, p.Ctx, c)
						hpid = fixture.LockPID(t, p.Ctx, holder)
						oid = fixture.LockOID(t, p.Ctx, "src")
					}
					e, finish := transactionalViewsBegin(t, p, b, c, mode)
					run := transactionalRulesRun(t, p, b, e, handles)
					ended := false
					defer func() {
						if !ended {
							finish(false)
						}
						if blocker == "later-target" {
							// Never reset or reuse a timer-armed owner.
							c.Raw(func(any) error { return driver.ErrBadConn })
						}
					}()
					var htx *sql.Tx
					if blocker != "missing-target" {
						htx = fixture.QueryLockBegin(t, p.Ctx, holder)
						defer htx.Rollback()
						lock := "LOCK TABLE src IN ACCESS SHARE MODE"
						if blocker == "later-target" {
							lock = "LOCK TABLE sink2 IN ACCESS EXCLUSIVE MODE"
						}
						fixture.RunStep(t, p.Ctx, htx, b.Recorder, fixture.Exec(lock, 0))
					}
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42703", Message: "column new.absent does not exist", Position: tc.position})
					s.ErrorStage = "execute"
					if blocker == "event" {
						var x fixture.Executor = e
						if stmt := handles[q]; stmt != nil {
							x = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
						}
						done := make(chan struct{})
						join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(done)
							r, err := x.ExecContext(ctx, q)
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						// Event AX must precede even an invalid qualification/action.
						fixture.RelationLockPending(t, p, b, pid, hpid, oid, "AccessExclusiveLock", done)
						if err := htx.Rollback(); err != nil {
							t.Fatal(err)
						}
						out := join()
						fixture.State(t, out.Err, s.State)
						fixture.CheckDiagnostic(t, out.Err, s, "execute")
					} else {
						if blocker == "later-target" {
							// A later action's unavailable target must not overtake the
							// earlier error with 55P03. No timing-based observer is needed:
							// the holder retains AX until after the exact error is checked.
							run(fixture.Exec("SET lock_timeout='100ms'", 0))
						}
						run(s)
					}
					if mode != "autocommit" && blocker != "later-target" {
						run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					}
					finish(false)
					ended = true
					if blocker == "later-target" {
						if err := htx.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
					observe := transactionalRulesRun(t, p, b, holder, nil)
					observe(transactionalRulesNames())
					observe(transactionalRulesRows("src"))
					observe(transactionalRulesRows("sink1"))
					observe(transactionalRulesRows("sink2"))
					observe(fixture.Exec("INSERT INTO src VALUES(1,-10)", 1))
					observe(transactionalRulesRows("src", []any{int64(1), int64(-10)}))
					observe(transactionalRulesRows("sink1"))
					observe(transactionalRulesRows("sink2"))
				})
			})
		}
	}
}
