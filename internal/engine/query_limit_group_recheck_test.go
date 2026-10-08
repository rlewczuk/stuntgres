package engine

import "testing"

func TestQueryLimitAggregateSiblingRecheck(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			e, a, b, c := rowLockEngineFixture(t)
			sessionSQL(t, c, "CREATE TABLE u(k INT)")
			sessionSQL(t, c, "INSERT INTO u VALUES(1),(2)")
			sessionSQL(t, c, "CREATE VIEW marked AS SELECT id,v FROM t FOR UPDATE")
			sessionSQL(t, c, "CREATE VIEW grouped AS SELECT k,count(*) AS n FROM u GROUP BY k")
			ar := transactionBegin(t, a)
			transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
			sessionSQL(t, a, "UPDATE t SET v=30 WHERE id=1")
			done := queryLimitAsync(t, e, b, "SELECT marked.v+grouped.k AS value FROM marked CROSS JOIN grouped WHERE marked.id=1 ORDER BY grouped.k LIMIT 2", prepared)
			rowLockQueued(t, e, a.transactions.ticket, rowUpdate)
			transactionEnd(t, a, ar, true)
			rowLockAwait(t, done, 31, 32)
			lockEmpty(t, e)
		})
	}
}
