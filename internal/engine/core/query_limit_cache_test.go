package core_test

import (
	"database/sql/driver"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
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

func rowLockEngineFixture(t *testing.T) (*engine.Engine, *session.Session, *session.Session, *session.Session) {
	t.Helper()
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	c := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, c, "CREATE TABLE t(id INT,v INT)")
	enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(1,10),(2,20)")
	return e, a, b, c
}

func rowLockAwait(t *testing.T, done <-chan deleteOutcome, want ...int64) {
	t.Helper()
	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if outcome.result.Rows == nil {
			t.Fatal("locker returned no cursor")
		}
		defer outcome.result.Rows.Close()
		got := enginetest.Tuples(outcome.result)
		expected := make([][]driver.Value, len(want))
		for i, v := range want {
			expected[i] = []driver.Value{v}
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("rows %v want %v", got, expected)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("locker failed to finish")
	}
}
