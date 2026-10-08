package engine

import "testing"

func TestQueryLimitFrontierHolds(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, joined := range []bool{false, true} {
			for _, zero := range []bool{false, true} {
				t.Run(map[bool]string{false: "table", true: "join"}[joined]+map[bool]string{false: "/one", true: "/zero"}[zero]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					e, _, b, c := rowLockEngineFixture(t)
					sessionSQL(t, c, "INSERT INTO t VALUES(3,30),(4,40)")
					sessionSQL(t, c, "CREATE TABLE u(id INT)")
					sessionSQL(t, c, "INSERT INTO u VALUES(1),(2),(3),(4)")
					from := "t"
					if joined {
						from = "t JOIN u ON t.id=u.id"
					}
					q := "SELECT t.v FROM " + from + " ORDER BY t.id OFFSET 1 LIMIT 1 FOR UPDATE"
					want := []int64{20}
					holds := map[string][]int{"t": {1, 2}}
					if joined {
						holds["u"] = []int{1, 2}
					}
					if zero {
						q = "SELECT t.v FROM " + from + " ORDER BY t.id LIMIT 0 FOR UPDATE"
						want = nil
						holds = nil
					}
					br := transactionBegin(t, b)
					rowLockViewRows(t, e, b, q, prepared, want...)
					queryLimitGrants(t, e, b.transactions.ticket, holds)
					transactionEnd(t, b, br, false)
					lockEmpty(t, e)
				})
			}
		}
	}
}

func TestQueryLimitAggregateSibling(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			e, _, b, c := rowLockEngineFixture(t)
			sessionSQL(t, c, "INSERT INTO t VALUES(3,30),(4,40)")
			sessionSQL(t, c, "CREATE TABLE u(k INT)")
			sessionSQL(t, c, "INSERT INTO u VALUES(1),(2)")
			sessionSQL(t, c, "CREATE VIEW marked AS SELECT id,v FROM t FOR UPDATE")
			sessionSQL(t, c, "CREATE VIEW scalar_wrapper AS SELECT id,v FROM marked")
			sessionSQL(t, c, "CREATE VIEW grouped AS SELECT k,count(*) AS n FROM u GROUP BY k")
			br := transactionBegin(t, b)
			rowLockViewRows(t, e, b, "SELECT scalar_wrapper.v FROM scalar_wrapper CROSS JOIN grouped WHERE scalar_wrapper.id=1 ORDER BY grouped.k LIMIT 1", prepared, 10)
			rowLockViewGrants(t, e, b.transactions.ticket, "t", 1)
			transactionEnd(t, b, br, false)
			lockEmpty(t, e)
		})
	}
}
