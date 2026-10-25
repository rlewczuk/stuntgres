package core_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestRowLockViewAggregateComposition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			query string
			want  []int64
			ids   []int
		}{
			{"SELECT count(*) AS n FROM stored WHERE id=1", []int64{1}, []int{1}},
			{"SELECT count(*) AS n FROM stored", []int64{3}, []int{1, 2, 3}},
			{"SELECT n FROM grouped ORDER BY id", []int64{1, 1, 1}, []int{1, 2, 3}},
			{"SELECT n FROM hidden", nil, []int{1, 2, 3}},
			{"SELECT n FROM grouped WHERE FALSE", nil, []int{1, 2, 3}},
			{"SELECT count(*) AS n FROM grouped", []int64{3}, []int{1, 2, 3}},
		} {
			t.Run(tc.query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e, a, b, c := rowLockEngineFixture(t)
				enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(3,30)")
				enginetest.SessionSQL(t, c, "CREATE VIEW stored AS SELECT id,v FROM t FOR UPDATE")
				enginetest.SessionSQL(t, c, "CREATE VIEW grouped AS SELECT id,count(*) AS n FROM stored GROUP BY id")
				enginetest.SessionSQL(t, c, "CREATE VIEW hidden AS SELECT count(*) AS n FROM stored HAVING FALSE")
				ar := enginetest.TransactionBegin(t, a)
				rowLockViewRows(t, e, a, tc.query, prepared, tc.want...)
				rowLockViewGrants(t, e, a.Transactions.Ticket, "t", tc.ids...)
				enginetest.TransactionEnd(t, a, ar, false)
				enginetest.LockEmpty(t, e)
				enginetest.SessionSQL(t, b, "DELETE FROM t WHERE id=1")
			})
		}
	}
	for _, tc := range []struct {
		name, change, query string
		commit              bool
		want                []int64
	}{
		{"definition_reject", "UPDATE t SET v=20 WHERE id=1", "SELECT count(*) AS n FROM stored", true, []int64{0}},
		{"delete", "DELETE FROM t WHERE id=1", "SELECT count(*) AS n FROM stored", true, []int64{0}},
		{"rollback", "UPDATE t SET v=20 WHERE id=1", "SELECT count(*) AS n FROM stored", false, []int64{1}},
		{"group_replacement", "UPDATE t SET v=20 WHERE id=1", "SELECT v FROM grouped", true, []int64{20}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, a, b, c := rowLockEngineFixture(t)
			body := "SELECT id,v FROM t WHERE v=10 FOR UPDATE"
			if tc.name == "group_replacement" {
				body = "SELECT id,v FROM t WHERE id=1 FOR UPDATE"
			}
			enginetest.SessionSQL(t, c, "CREATE VIEW stored AS "+body)
			enginetest.SessionSQL(t, c, "CREATE VIEW grouped AS SELECT v,count(*) AS n FROM stored GROUP BY v")
			ar := enginetest.TransactionBegin(t, a)
			enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
			enginetest.SessionSQL(t, a, tc.change)
			done := rowLockViewAsync(t, b, context.Background(), tc.query)
			rowLockQueued(t, e, a.Transactions.Ticket, lock.ForUpdate)
			// A newly qualifying row is not in the already captured aggregate input.
			enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(3,10)")
			enginetest.TransactionEnd(t, a, ar, tc.commit)
			rowLockAwait(t, done, tc.want...)
			enginetest.LockEmpty(t, e)
		})
	}
	for _, clause := range rowLockTestClauses {
		for _, deleted := range []bool{false, true} {
			t.Run("RR/"+clause+map[bool]string{false: "/update", true: "/delete"}[deleted], func(t *testing.T) {
				e, a, b, c := rowLockEngineFixture(t)
				enginetest.SessionSQL(t, c, "CREATE VIEW stored AS SELECT id,v FROM t WHERE id=1 "+clause)
				enginetest.SessionSQL(t, c, "CREATE VIEW grouped AS SELECT v,count(*) AS n FROM stored GROUP BY v")
				enginetest.SessionSQL(t, b, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				enginetest.TransactionRows(t, b, "SELECT v FROM t WHERE id=1", 10)
				change := "UPDATE t SET v=20 WHERE id=1"
				if deleted {
					change = "DELETE FROM t WHERE id=1"
				}
				enginetest.SessionSQL(t, a, change)
				result, err := rowLockViewExecute(t, e, b, "SELECT v FROM grouped", true)
				if clause == "FOR KEY SHARE" && !deleted {
					if err != nil || result.Rows == nil {
						t.Fatal(result, err)
					}
					defer result.Rows.Close()
					if got := enginetest.Tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(10)}}) {
						t.Fatal(got)
					}
				} else {
					if result.Rows != nil {
						result.Rows.Close()
						t.Fatal("serialization error cursor")
					}
					waitsDiagnostic(t, err, "40001", "could not serialize access due to concurrent update")
				}
				enginetest.SessionSQL(t, b, "ROLLBACK")
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
