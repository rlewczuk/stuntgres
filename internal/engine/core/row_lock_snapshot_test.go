package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/enginetest"
)

func TestRowLockKeyShareSnapshot(t *testing.T) {
	e, a, b, c := rowLockEngineFixture(t)
	enginetest.SessionSQL(t, b, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	enginetest.TransactionRows(t, b, "SELECT v FROM t WHERE id=1", 10)
	enginetest.SessionSQL(t, a, "UPDATE t SET v=20 WHERE id=1")
	// A keyless UPDATE's NO KEY UPDATE is compatible with KEY SHARE. RR keeps
	// the original visible values while its stable-RowID grant protects the chain.
	enginetest.TransactionRows(t, b, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", 10)
	enginetest.TransactionRows(t, b, "SELECT v FROM t WHERE v=10 FOR KEY SHARE", 10)
	enginetest.TransactionRows(t, b, "SELECT a.v FROM t a JOIN t b USING(id) WHERE a.id=1 AND b.v=10 FOR KEY SHARE", 10)
	enginetest.TransactionRows(t, c, "SELECT v FROM t WHERE id=1", 20)
	enginetest.SessionSQL(t, b, "ROLLBACK")
	enginetest.LockEmpty(t, e)
}

func TestRowLockKeyShareDeletedReplacement(t *testing.T) {
	e, a, b, _ := rowLockEngineFixture(t)
	enginetest.SessionSQL(t, b, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	enginetest.TransactionRows(t, b, "SELECT v FROM t WHERE id=1", 10)
	enginetest.SessionSQL(t, a, "UPDATE t SET v=20 WHERE id=1")
	enginetest.SessionSQL(t, a, "DELETE FROM t WHERE id=1")
	op, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(op, enginetest.Statement(t, "SELECT v FROM t WHERE id=1 FOR KEY SHARE"), 0, nil)
	op.Done()
	if result.Rows != nil {
		result.Rows.Close()
		t.Fatal("deleted RR chain exposed rows")
	}
	waitsDiagnostic(t, err, "40001", "could not serialize access due to concurrent update")
	enginetest.SessionSQL(t, b, "ROLLBACK")
	enginetest.LockEmpty(t, e)
}
