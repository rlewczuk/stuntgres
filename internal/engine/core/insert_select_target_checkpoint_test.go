package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestInsertSelectTargetBeforeSource(t *testing.T) {
	e := engine.New()
	holder, worker := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, holder, "CREATE TABLE t(i INT)")
	enginetest.SessionSQL(t, holder, "CREATE TABLE s(i INT)")
	enginetest.SessionSQL(t, worker, "SET lock_timeout='80ms'")
	tx := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "LOCK TABLE s IN ACCESS EXCLUSIVE MODE")
	for _, prepare := range []bool{false, true} {
		for _, tc := range []struct{ q, code, message, pos string }{
			{"INSERT INTO t(missing) SELECT i FROM s", "42703", `column "missing" of relation "t" does not exist`, "15"},
			{"INSERT INTO t(i,i) SELECT i FROM s", "42701", `column "i" specified more than once`, "17"},
			{"INSERT INTO t(missing) SELECT i FROM absent", "42703", `column "missing" of relation "t" does not exist`, "15"},
		} {
			op, _, err := worker.AcquireCommand(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ast := enginetest.Statement(t, tc.q)
			if prepare {
				_, err = e.Describe(op, ast)
			} else {
				_, err = e.Execute(op, ast, 0, nil)
			}
			err = op.CommandError(err)
			op.Done()
			waitsDiagnostic(t, err, tc.code, tc.message, tc.pos)
		}
	}
	enginetest.TransactionEnd(t, holder, tx, false)
	enginetest.LockEmpty(t, e)
}
