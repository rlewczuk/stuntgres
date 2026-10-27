package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRuleModeTransactions(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			ruleModeTransactions(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				base := transactionalRulesRun(t, p, b, c, nil)
				ruleModeSetup(base)
				q := "ALTER TABLE src DISABLE RULE r"
				ins := "INSERT INTO src VALUES($1,$2)"
				h := transactionalRulesHandles(t, p, c, held, q, ins)
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := transactionalRulesRun(t, p, b, e, h)
				run(fixture.Exec(q, 0))
				run(ruleModeCatalog("D"))
				run(fixture.Exec(ins, 1, int64(1), int64(10)))
				run(ruleModeValueRows("src", []any{int64(1), int64(10)}))
				run(ruleModeCount("sink1", 0))
				other := transactionalRulesRun(t, p, b, peer, nil)
				want := "O"
				if mode == "autocommit" {
					want = "D"
				}
				other(ruleModeCatalog(want))
				finish(commit)
				want = "O"
				if commit || mode == "autocommit" {
					want = "D"
				}
				base(ruleModeCatalog(want))
				other(ruleModeCatalog(want))
				base(fixture.Exec(ins, 1, int64(2), int64(20)))
				if want == "O" {
					base(ruleModeValueRows("sink1", []any{int64(2), int64(20)}))
				} else {
					base(ruleModeCount("sink1", 0))
				}
			})
		})
	}
	t.Run("savepoint", func(t *testing.T) {
		ruleModeTransactions(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
			base := transactionalRulesRun(t, p, b, c, nil)
			ruleModeSetup(base)
			ins := "INSERT INTO src VALUES($1,$2)"
			h := transactionalRulesHandles(t, p, c, held, ins, "ALTER TABLE src DISABLE RULE r")
			e, finish := transactionalViewsBegin(t, p, b, c, mode)
			run := transactionalRulesRun(t, p, b, e, h)
			run(fixture.Exec(ins, 1, int64(1), int64(10)))
			run(fixture.Exec("SAVEPOINT keep", 0))
			run(fixture.Exec("ALTER TABLE src DISABLE RULE r", 0))
			run(ruleModeCatalog("D"))
			run(fixture.Exec(ins, 1, int64(2), int64(20)))
			run(fixture.Exec("ROLLBACK TO keep", 0))
			run(ruleModeCatalog("O"))
			run(ruleModeMissing("ALTER TABLE src ENABLE RULE missing", "missing", "src"))
			run(ruleError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
			run(fixture.Exec("ROLLBACK TO keep", 0))
			run(ruleModeCatalog("O"))
			run(fixture.Exec(ins, 1, int64(3), int64(30)))
			finish(true)
			base(ruleModeValueRows("src", []any{int64(1), int64(10)}, []any{int64(3), int64(30)}))
			base(ruleModeValueRows("sink1", []any{int64(1), int64(10)}, []any{int64(3), int64(30)}))
		})
	})
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "peer-after-rollback", true: "peer-after-commit"}[commit], func(t *testing.T) {
			ruleModeTransactions(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				base := transactionalRulesRun(t, p, b, c, nil)
				ruleModeSetup(base)
				q := "ALTER TABLE src DISABLE RULE r"
				h := transactionalRulesHandles(t, p, c, held, q)
				pid, hpid := 0, 0
				var oid int64
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, peer)
					hpid = fixture.LockPID(t, p.Ctx, c)
					oid = fixture.LockOID(t, p.Ctx, "src")
				}
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := transactionalRulesRun(t, p, b, e, h)
				run(fixture.Exec(q, 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, ruleModeCatalog("O"))
				done := make(chan struct{})
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(done)
					r, err := peer.ExecContext(ctx, "INSERT INTO src VALUES(1,10)")
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				fixture.RelationLockPending(t, p, b, pid, hpid, oid, "RowExclusiveLock", done)
				finish(commit)
				out := join()
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				if n, err := out.Result.RowsAffected(); err != nil || n != 1 {
					t.Fatal(n, err)
				}
				if commit {
					base(ruleModeCount("sink1", 0))
				} else {
					base(ruleModeValueRows("sink1", []any{int64(1), int64(10)}))
				}
			})
		})
	}
}

func TestReferenceRuleModeAtomicity(t *testing.T) {
	for _, tc := range []struct {
		q       string
		failure fixture.Step
	}{
		{q: "ALTER TABLE src DISABLE RULE r, ENABLE RULE missing", failure: ruleModeMissing("ALTER TABLE src DISABLE RULE r, ENABLE RULE missing", "missing", "src")},
		{q: "ALTER TABLE src DISABLE RULE r, ENABLE ALWAYS RULE r, ENABLE RULE missing", failure: ruleModeMissing("ALTER TABLE src DISABLE RULE r, ENABLE ALWAYS RULE r, ENABLE RULE missing", "missing", "src")},
		{q: "ALTER TABLE src DISABLE RULE r, ADD COLUMN extra INT, ADD CONSTRAINT positive CHECK(v>0)", failure: fixture.ImplicitError("ALTER TABLE src DISABLE RULE r, ADD COLUMN extra INT, ADD CONSTRAINT positive CHECK(v>0)", fixture.Diagnostic{Code: "23514", Message: "check constraint \"positive\" of relation \"src\" is violated by some row", Schema: "public", Table: "src", Constraint: "positive"})},
		{q: "ALTER TABLE src DISABLE RULE r, ADD COLUMN extra INT, VALIDATE CONSTRAINT positive", failure: fixture.ImplicitError("ALTER TABLE src DISABLE RULE r, ADD COLUMN extra INT, VALIDATE CONSTRAINT positive", fixture.Diagnostic{Code: "23514", Message: "check constraint \"positive\" of relation \"src\" is violated by some row", Schema: "public", Table: "src", Constraint: "positive"})},
	} {
		t.Run(tc.q, func(t *testing.T) {
			ruleModeTransactions(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				base := transactionalRulesRun(t, p, b, c, nil)
				ruleModeSetup(base)
				base(fixture.Exec("INSERT INTO src VALUES(1,-10)", 1))
				base(fixture.Exec("ALTER TABLE src ADD CONSTRAINT positive CHECK(v>0) NOT VALID", 0))
				// The ADD witness uses a different constraint name from the preexisting VALIDATE witness.
				if tc.q == "ALTER TABLE src DISABLE RULE r, ADD COLUMN extra INT, ADD CONSTRAINT positive CHECK(v>0)" {
					base(fixture.Exec("ALTER TABLE src DROP CONSTRAINT positive", 0))
				}
				h := transactionalRulesHandles(t, p, c, held, tc.q)
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := transactionalRulesRun(t, p, b, e, h)
				if mode != "autocommit" {
					run(fixture.Exec("SAVEPOINT before_mode", 0))
				}
				s := tc.failure
				s.ErrorStage = "execute"
				run(s)
				if mode != "autocommit" {
					run(ruleError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
					run(fixture.Exec("ROLLBACK TO before_mode", 0))
				}
				run(ruleModeCatalog("O"))
				run(ruleModeValueRows("src", []any{int64(1), int64(-10)}))
				run(ruleModeValueRows("sink1", []any{int64(1), int64(-10)}))
				run(fixture.Select("SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name='src' ORDER BY ordinal_position", []string{"column_name"}, []string{"NAME"}, [][]any{{"id"}, {"v"}}))
				run(fixture.Exec("INSERT INTO src VALUES(2,20)", 1))
				run(ruleModeCount("sink1", 2))
				finish(true)
			})
		})
	}
}
