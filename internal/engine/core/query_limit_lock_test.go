package core_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func queryLimitAsync(t *testing.T, e *engine.Engine, s *session.Session, q string, prepared bool) <-chan deleteOutcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ast := enginetest.Statement(t, q)
	done := make(chan deleteOutcome, 1)
	go func() {
		op, _, err := s.AcquireCommand(ctx)
		var result engine.Result
		if err == nil {
			var description engine.StatementDescription
			if prepared {
				description, err = e.Describe(op, ast)
			}
			if err == nil {
				var scope *engine.StatementScope
				scope, err = e.StartStatement(op)
				if err == nil {
					if prepared {
						scope.Expected = &description
					}
					result, err = scope.Execute(ast, 0, nil)
					err = scope.Finish(err)
				}
			}
			err = op.CommandError(err)
			op.Done()
		}
		done <- deleteOutcome{result, err}
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case outcome := <-done:
			if outcome.result.Rows != nil {
				outcome.result.Rows.Close()
			}
		case <-time.After(5 * time.Second):
			t.Error("limit waiter did not join")
		}
	})
	return done
}

func TestQueryLimitLocks(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"direct", "stored", "join"} {
			for _, change := range []string{"DELETE FROM t WHERE id=2", "UPDATE t SET v=60 WHERE id=2"} {
				t.Run(source+change+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					e, a, b, c := rowLockEngineFixture(t)
					enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(3,30),(4,40)")
					enginetest.SessionSQL(t, c, "CREATE VIEW marked AS SELECT id,v FROM t WHERE v<50 FOR UPDATE")
					enginetest.SessionSQL(t, c, "CREATE TABLE u(id INT)")
					enginetest.SessionSQL(t, c, "INSERT INTO u VALUES(1),(2),(3),(4)")
					q := "SELECT v FROM t WHERE v<50 ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE"
					if source == "stored" {
						q = "SELECT v FROM marked ORDER BY id OFFSET 1 LIMIT 1"
					}
					if source == "join" {
						q = "SELECT t.v FROM t JOIN u ON t.id=u.id WHERE t.v<50 ORDER BY t.id OFFSET 1 LIMIT 1 FOR UPDATE"
					}
					ar := enginetest.TransactionBegin(t, a)
					enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=2 FOR UPDATE", 20)
					enginetest.SessionSQL(t, a, change)
					br := enginetest.TransactionBegin(t, b)
					done := queryLimitAsync(t, e, b, q, prepared)
					rowLockQueued(t, e, a.Transactions.Ticket, lock.ForUpdate)
					enginetest.TransactionEnd(t, a, ar, true)
					rowLockAwait(t, done, 30)
					if source != "join" {
						ids := []int{1, 2, 3}
						if source == "stored" {
							ids = append(ids, 4)
						}
						rowLockViewGrants(t, e, b.Transactions.Ticket, "t", ids...)
					} else {
						// Both joined physical occurrences are due only through the
						// skipped, rejected and surviving candidate frontier.
						queryLimitGrants(t, e, b.Transactions.Ticket, map[string][]int{"t": {1, 2, 3}, "u": {1, 2, 3}})
					}
					enginetest.TransactionEnd(t, b, br, false)
					enginetest.LockEmpty(t, e)
				})
			}
		}
	}
}

func TestQueryLimitLocalMarkBoundaries(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			definition, query string
			want              []int64
			holds             []int
		}{
			{"SELECT id,v FROM t FOR UPDATE", "SELECT count(*) FROM marked WHERE id=1", []int64{1}, []int{1}},
			{"SELECT id,v FROM t ORDER BY id FOR UPDATE", "SELECT v FROM marked WHERE id=1", []int64{10}, []int{1}},
			{"SELECT id,v FROM t FOR UPDATE", "SELECT v FROM marked ORDER BY id OFFSET 1 LIMIT 1", []int64{20}, []int{1, 2, 3, 4}},
			{"SELECT id,v FROM t LIMIT 1 FOR UPDATE", "SELECT v FROM marked WHERE id=2", nil, []int{1}},
			{"SELECT id,v FROM t OFFSET 1 LIMIT 1 FOR UPDATE", "SELECT v FROM marked WHERE id=3", nil, []int{1, 2}},
			{"SELECT id,v FROM t OFFSET 1 LIMIT 1 FOR UPDATE", "SELECT v FROM marked LIMIT 0", nil, nil},
			{"SELECT id,v FROM t FOR UPDATE", "SELECT count(*) FROM marked", []int64{4}, []int{1, 2, 3, 4}},
		} {
			t.Run(tc.definition+tc.query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e, _, b, c := rowLockEngineFixture(t)
				enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(3,30),(4,40)")
				enginetest.SessionSQL(t, c, "CREATE VIEW marked AS "+tc.definition)
				br := enginetest.TransactionBegin(t, b)
				rowLockViewRows(t, e, b, tc.query, prepared, tc.want...)
				rowLockViewGrants(t, e, b.Transactions.Ticket, "t", tc.holds...)
				enginetest.TransactionEnd(t, b, br, false)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestQueryLimitLockCancellation(t *testing.T) {
	e, a, b, c := rowLockEngineFixture(t)
	enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(3,30),(4,40)")
	ar := enginetest.TransactionBegin(t, a)
	enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=2 FOR UPDATE", 20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := rowLockViewAsync(t, b, ctx, "SELECT v FROM t ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE")
	rowLockQueued(t, e, a.Transactions.Ticket, lock.ForUpdate)
	cancel()
	select {
	case outcome := <-done:
		if outcome.result.Rows != nil || outcome.err == nil {
			t.Fatal("cancel exposed result", outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled frontier did not join")
	}
	enginetest.TransactionEnd(t, a, ar, false)
	enginetest.LockEmpty(t, e)
}

func queryLimitGrants(t *testing.T, e *engine.Engine, ticket *lock.Ticket, tables map[string][]int) {
	t.Helper()
	want := make(map[lock.Resource]bool)
	for table, ids := range tables {
		ref := enginetest.RoleTableRef(t, e, table)
		e.Mu.Lock()
		for _, id := range ids {
			want[lock.Resource{ObjectRef: ref, Row: e.Storage.Relations[ref.Address].Versions[id-1].Row}] = true
		}
		e.Mu.Unlock()
	}
	lockState := e.Locks.Inspect()
	got := make(map[lock.Resource]bool)
	for resource, entry := range lockState.Resources {
		if resource.Row != 0 && entry.Holders[ticket].Mode != 0 {
			got[resource] = true
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("exact physical count frontier", got, want)
	}
}

func rowLockViewAsync(t *testing.T, s *session.Session, parent context.Context, q string) <-chan deleteOutcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	ast := enginetest.Statement(t, q)
	done := make(chan deleteOutcome, 1)
	go func() {
		op, _, err := s.AcquireCommand(ctx)
		var result engine.Result
		if err == nil {
			result, err = engine.EngineOf(s).Execute(op, ast, 0, nil)
			err = op.CommandError(err)
			op.Done()
		}
		done <- deleteOutcome{result, err}
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case remaining := <-done:
			if remaining.result.Rows != nil {
				remaining.result.Rows.Close()
			}
		case <-time.After(5 * time.Second):
			t.Error("view row waiter did not join")
		}
	})
	return done
}
