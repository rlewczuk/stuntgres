package engine

import (
	"context"
	"testing"
)

func TestRowLockKeyShareSnapshot(t *testing.T) {
	e, a, b, c := rowLockEngineFixture(t)
	sessionSQL(t, b, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	transactionRows(t, b, "SELECT v FROM t WHERE id=1", 10)
	sessionSQL(t, a, "UPDATE t SET v=20 WHERE id=1")
	// A keyless UPDATE's NO KEY UPDATE is compatible with KEY SHARE. RR keeps
	// the original visible values while its stable-RowID grant protects the chain.
	transactionRows(t, b, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", 10)
	transactionRows(t, b, "SELECT v FROM t WHERE v=10 FOR KEY SHARE", 10)
	transactionRows(t, b, "SELECT a.v FROM t a JOIN t b USING(id) WHERE a.id=1 AND b.v=10 FOR KEY SHARE", 10)
	transactionRows(t, c, "SELECT v FROM t WHERE id=1", 20)
	sessionSQL(t, b, "ROLLBACK")
	lockEmpty(t, e)
}

func TestRowLockKeyShareDeletedReplacement(t *testing.T) {
	e, a, b, _ := rowLockEngineFixture(t)
	sessionSQL(t, b, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	transactionRows(t, b, "SELECT v FROM t WHERE id=1", 10)
	sessionSQL(t, a, "UPDATE t SET v=20 WHERE id=1")
	sessionSQL(t, a, "DELETE FROM t WHERE id=1")
	op, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(op, statement(t, "SELECT v FROM t WHERE id=1 FOR KEY SHARE"), 0, nil)
	op.Done()
	if result.Rows != nil {
		result.Rows.Close()
		t.Fatal("deleted RR chain exposed rows")
	}
	waitsDiagnostic(t, err, "40001", "could not serialize access due to concurrent update")
	sessionSQL(t, b, "ROLLBACK")
	lockEmpty(t, e)
}
