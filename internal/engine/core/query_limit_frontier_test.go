package core_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func TestQueryLimitFrontierHolds(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, joined := range []bool{false, true} {
			for _, zero := range []bool{false, true} {
				t.Run(map[bool]string{false: "table", true: "join"}[joined]+map[bool]string{false: "/one", true: "/zero"}[zero]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					e, _, b, c := rowLockEngineFixture(t)
					enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(3,30),(4,40)")
					enginetest.SessionSQL(t, c, "CREATE TABLE u(id INT)")
					enginetest.SessionSQL(t, c, "INSERT INTO u VALUES(1),(2),(3),(4)")
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
					br := enginetest.TransactionBegin(t, b)
					rowLockViewRows(t, e, b, q, prepared, want...)
					queryLimitGrants(t, e, b.Transactions.Ticket, holds)
					enginetest.TransactionEnd(t, b, br, false)
					enginetest.LockEmpty(t, e)
				})
			}
		}
	}
}

func TestQueryLimitAggregateSibling(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			e, _, b, c := rowLockEngineFixture(t)
			enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(3,30),(4,40)")
			enginetest.SessionSQL(t, c, "CREATE TABLE u(k INT)")
			enginetest.SessionSQL(t, c, "INSERT INTO u VALUES(1),(2)")
			enginetest.SessionSQL(t, c, "CREATE VIEW marked AS SELECT id,v FROM t FOR UPDATE")
			enginetest.SessionSQL(t, c, "CREATE VIEW scalar_wrapper AS SELECT id,v FROM marked")
			enginetest.SessionSQL(t, c, "CREATE VIEW grouped AS SELECT k,count(*) AS n FROM u GROUP BY k")
			br := enginetest.TransactionBegin(t, b)
			rowLockViewRows(t, e, b, "SELECT scalar_wrapper.v FROM scalar_wrapper CROSS JOIN grouped WHERE scalar_wrapper.id=1 ORDER BY grouped.k LIMIT 1", prepared, 10)
			rowLockViewGrants(t, e, b.Transactions.Ticket, "t", 1)
			enginetest.TransactionEnd(t, b, br, false)
			enginetest.LockEmpty(t, e)
		})
	}
}

func rowLockViewRows(t *testing.T, e *engine.Engine, s *session.Session, q string, prepared bool, want ...int64) {
	t.Helper()
	result, err := rowLockViewExecute(t, e, s, q, prepared)
	if err != nil {
		t.Fatal(q, err)
	}
	if result.Rows == nil {
		t.Fatal("missing view cursor", q)
	}
	defer result.Rows.Close()
	expected := make([][]driver.Value, len(want))
	for i, v := range want {
		expected[i] = []driver.Value{v}
	}
	if got := enginetest.Tuples(result); !reflect.DeepEqual(got, expected) {
		t.Fatal(q, got, expected)
	}
}

func rowLockViewGrants(t *testing.T, e *engine.Engine, ticket *lock.Ticket, table string, ids ...int) {
	t.Helper()
	ref := enginetest.RoleTableRef(t, e, table)
	e.Mu.Lock()
	want := make(map[lock.Resource]bool, len(ids))
	for _, id := range ids {
		want[lock.Resource{ObjectRef: ref, Row: e.Storage.Relations[ref.Address].Versions[id-1].Row}] = true
	}
	e.Mu.Unlock()
	lockState := e.Locks.Inspect()
	got := make(map[lock.Resource]bool)
	for resource, entry := range lockState.Resources {
		if resource.Row != 0 && entry.Holders[ticket].Mode != 0 {
			got[resource] = true
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("physical row grants", got, want)
	}
}

func rowLockViewExecute(t *testing.T, e *engine.Engine, s *session.Session, q string, prepared bool) (engine.Result, error) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	ast := enginetest.Statement(t, q)
	var description engine.StatementDescription
	if prepared {
		description, err = e.Describe(op, ast)
		if err != nil {
			return engine.Result{}, err
		}
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		return engine.Result{}, err
	}
	if prepared {
		scope.Expected = &description
	}
	result, err := scope.Execute(ast, 0, nil)
	return result, scope.Finish(err)
}
