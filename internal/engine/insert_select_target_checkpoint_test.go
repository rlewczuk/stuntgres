package engine

import (
	"context"
	"testing"
)

func TestInsertSelectTargetBeforeSource(t *testing.T) {
	e := New()
	holder, worker := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, holder, "CREATE TABLE t(i INT)")
	sessionSQL(t, holder, "CREATE TABLE s(i INT)")
	sessionSQL(t, worker, "SET lock_timeout='80ms'")
	tx := transactionBegin(t, holder)
	sessionSQL(t, holder, "LOCK TABLE s IN ACCESS EXCLUSIVE MODE")
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
			ast := statement(t, tc.q)
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
	transactionEnd(t, holder, tx, false)
	lockEmpty(t, e)
}
