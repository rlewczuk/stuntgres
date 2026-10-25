package core_test

import (
	"testing"

	"stuntgres/internal/engine/enginetest"
)

func TestRowLockViewFailingFirst(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		t.Run(clause, func(t *testing.T) {
			e, a, b, _ := rowLockEngineFixture(t)
			enginetest.SessionSQL(t, a, "CREATE VIEW v AS SELECT id,v FROM t")
			ar := enginetest.TransactionBegin(t, a)
			enginetest.TransactionRows(t, a, "SELECT v FROM v WHERE id=1 "+clause, 10)
			enginetest.TransactionRows(t, b, "SELECT v FROM t WHERE id=1", 10)
			enginetest.TransactionEnd(t, a, ar, false)
			enginetest.LockEmpty(t, e)
		})
	}
}
