package engine

import "testing"

func TestRowLockViewFailingFirst(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		t.Run(clause, func(t *testing.T) {
			e, a, b, _ := rowLockEngineFixture(t)
			sessionSQL(t, a, "CREATE VIEW v AS SELECT id,v FROM t")
			ar := transactionBegin(t, a)
			transactionRows(t, a, "SELECT v FROM v WHERE id=1 "+clause, 10)
			transactionRows(t, b, "SELECT v FROM t WHERE id=1", 10)
			transactionEnd(t, a, ar, false)
			lockEmpty(t, e)
		})
	}
}
