package core_test

import (
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestQueryLimitLockedCache(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"t", "own", "stored_rows"} {
			t.Run(source+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e, a, b, c := rowLockEngineFixture(t)
				enginetest.SessionSQL(t, c, "CREATE SEQUENCE s")
				enginetest.SessionSQL(t, c, "CREATE VIEW own AS SELECT id,nextval('s') AS n FROM t WHERE id=1 ORDER BY n,n LIMIT 1 FOR UPDATE")
				enginetest.SessionSQL(t, c, "CREATE VIEW stored_rows AS SELECT id,v FROM t WHERE id=1 FOR UPDATE")
				q := "SELECT nextval('s') AS n FROM t WHERE id=1 ORDER BY n,n LIMIT 1 FOR UPDATE"
				want := int64(2)
				if source == "own" {
					q = "SELECT n FROM own"
				}
				if source == "stored_rows" {
					q = "SELECT nextval('s') AS n FROM stored_rows ORDER BY n,n LIMIT 1"
					want = 1
				}
				ar := enginetest.TransactionBegin(t, a)
				enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
				enginetest.SessionSQL(t, a, "UPDATE t SET v=30 WHERE id=1")
				done := queryLimitAsync(t, e, b, q, prepared)
				rowLockQueued(t, e, a.Transactions.Ticket, lock.ForUpdate)
				enginetest.TransactionEnd(t, a, ar, true)
				rowLockAwait(t, done, want)
				enginetest.TransactionRows(t, b, "SELECT currval('s')", want)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestQueryLimitLocalOuterSort(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"stored", "stored_rows"} {
			t.Run(source+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e, a, b, c := rowLockEngineFixture(t)
				enginetest.SessionSQL(t, c, "CREATE SEQUENCE s")
				enginetest.SessionSQL(t, c, "CREATE VIEW stored AS SELECT id,v FROM t ORDER BY v+1,id FOR UPDATE")
				enginetest.SessionSQL(t, c, "CREATE VIEW stored_rows AS SELECT id,v FROM t WHERE id=1 FOR UPDATE")
				q := "SELECT v+1 AS k FROM stored ORDER BY k,id"
				want := []int64{21, 31}
				if source == "stored_rows" {
					q = "SELECT nextval('s') AS n FROM stored_rows ORDER BY n,n"
					want = []int64{1}
				}
				ar := enginetest.TransactionBegin(t, a)
				enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
				enginetest.SessionSQL(t, a, "UPDATE t SET v=30 WHERE id=1")
				done := queryLimitAsync(t, e, b, q, prepared)
				rowLockQueued(t, e, a.Transactions.Ticket, lock.ForUpdate)
				enginetest.TransactionEnd(t, a, ar, true)
				rowLockAwait(t, done, want...)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
