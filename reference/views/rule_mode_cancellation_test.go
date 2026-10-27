package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRuleModeCancellation(t *testing.T) {
	for _, action := range []string{"DISABLE RULE", "ENABLE REPLICA RULE", "ENABLE ALWAYS RULE"} {
		t.Run(action, func(t *testing.T) {
			ruleModeTransactions(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, holder *sql.Conn, mode string, held bool) {
				base := transactionalRulesRun(t, p, b, c, nil)
				ruleModeSetup(base)
				base(fixture.Exec("CREATE TABLE sentinel(id INT)", 0))
				base(fixture.Exec("INSERT INTO sentinel VALUES(99)", 1))
				q := "ALTER TABLE src " + action + " r"
				h := transactionalRulesHandles(t, p, c, held, q)
				pid, hpid := 0, 0
				var oid int64
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, c)
					hpid = fixture.LockPID(t, p.Ctx, holder)
					oid = fixture.LockOID(t, p.Ctx, "src")
				}
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				_ = e // Conn-owned held utility executes in the connection's active transaction.
				htx := fixture.QueryLockBegin(t, p.Ctx, holder)
				defer htx.Rollback()
				fixture.RunStep(t, p.Ctx, htx, b.Recorder, fixture.Exec("LOCK TABLE src IN ACCESS SHARE MODE", 0))
				var x fixture.Executor = c
				if stmt := h[q]; stmt != nil {
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
				fixture.RelationLockPending(t, p, b, pid, hpid, oid, "AccessExclusiveLock", done)
				if b.PG {
					var ok bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ok); err != nil || !ok {
						t.Fatal(ok, err)
					}
				} else {
					cancel()
				}
				out := join()
				if b.PG {
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					fixture.AssertDiagnostic(t, out.Err, &want)
				} else if !errors.Is(out.Err, context.Canceled) {
					t.Fatal(out.Err)
				}
				finish(false)
				if err := htx.Rollback(); err != nil {
					t.Fatal(err)
				}
				observe := transactionalRulesRun(t, p, b, holder, nil)
				observe(ruleModeCatalog("O"))
				observe(ruleModeCount("src", 0))
				observe(ruleModeCount("sink1", 0))
				observe(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
				observe(fixture.Exec("INSERT INTO src VALUES(1,10)", 1))
				observe(ruleModeValueRows("sink1", []any{int64(1), int64(10)}))
			})
		})
	}
}
