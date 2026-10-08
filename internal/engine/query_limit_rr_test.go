package engine

import "testing"

func TestQueryLimitLockRepeatableRead(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range rowLockTestClauses {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e, a, b, _ := rowLockEngineFixture(t)
				sessionSQL(t, b, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				transactionRows(t, b, "SELECT v FROM t ORDER BY id", 10, 20)
				sessionSQL(t, a, "UPDATE t SET v=30 WHERE id=1")
				q := "SELECT v FROM t ORDER BY id LIMIT 1 " + clause
				if clause == "FOR KEY SHARE" {
					rowLockViewRows(t, e, b, q, prepared, 10)
				} else {
					r, err := rowLockViewExecute(t, e, b, q, prepared)
					if r.Rows != nil {
						r.Rows.Close()
						t.Fatal("serialization exposed cursor")
					}
					if r.Count != 0 {
						t.Fatal("serialization exposed count", r.Count)
					}
					waitsDiagnostic(t, err, "40001", "could not serialize access due to concurrent update")
				}
				sessionSQL(t, b, "ROLLBACK")
				lockEmpty(t, e)
			})
		}
	}
}
