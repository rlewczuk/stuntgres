package core_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

var rowLockTestClauses = []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"}
var rowLockTestConflicts = [4][4]bool{{false, false, false, true}, {false, false, true, true}, {false, true, true, true}, {true, true, true, true}}

func rowLockEngineFixture(t *testing.T) (*core.Engine, *session.Session, *session.Session, *session.Session) {
	t.Helper()
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	c := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, c, "CREATE TABLE t(id INT,v INT)")
	enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(1,10),(2,20)")
	return e, a, b, c
}
func rowLockQueued(t *testing.T, e *core.Engine, holder *lock.Ticket, mode lock.Mode) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		found := false
		for resource, entry := range lockState.Resources {
			if resource.Row == 0 || entry.Holders[holder].Mode == 0 {
				continue
			}
			for _, request := range entry.Pending {
				if request.Mode == mode && !request.Terminal && request.Ticket != holder {
					found = true
				}
			}
		}
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("exact row request not queued")
		}
		runtime.Gosched()
	}
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
func TestRowLockManagerMatrix(t *testing.T) {
	modes := []lock.Mode{lock.ForKeyShare, lock.ForShare, lock.ForNoKeyUpdate, lock.ForUpdate}
	for requested := range modes {
		for held := range modes {
			t.Run(rowLockTestClauses[requested]+"/"+rowLockTestClauses[held], func(t *testing.T) {
				e, a, b, c := rowLockEngineFixture(t)
				ar, br := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
				enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=1 "+rowLockTestClauses[held], 10)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				done := deleteAsync(t, b, ctx, "SELECT v FROM t WHERE id=1 "+rowLockTestClauses[requested])
				if rowLockTestConflicts[requested][held] {
					rowLockQueued(t, e, a.Transactions.Ticket, modes[requested])
					select {
					case got := <-done:
						t.Fatal("conflict completed early", got)
					default:
					}
					enginetest.TransactionRows(t, c, "SELECT v FROM t WHERE id=1", 10)
					enginetest.TransactionEnd(t, a, ar, false)
					rowLockAwait(t, done, 10)
				} else {
					rowLockAwait(t, done, 10)
					enginetest.TransactionEnd(t, a, ar, false)
				}
				enginetest.TransactionEnd(t, b, br, false)
				enginetest.LockEmpty(t, e)
			})
		}
	}
	t.Run("same_owner_sets", func(t *testing.T) {
		e, a, _, _ := rowLockEngineFixture(t)
		ar := enginetest.TransactionBegin(t, a)
		for _, clause := range rowLockTestClauses {
			enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=1 "+clause, 10)
		}
		lockState := e.Locks.Inspect()
		var held lock.Mode
		for resource, entry := range lockState.Resources {
			if resource.Row != 0 {
				held |= entry.Holders[a.Transactions.Ticket].Mode
			}
		}
		if held != lock.ForKeyShare|lock.ForShare|lock.ForNoKeyUpdate|lock.ForUpdate {
			t.Fatal("mode set lost", held)
		}
		enginetest.TransactionEnd(t, a, ar, false)
		enginetest.LockEmpty(t, e)
	})
}
func TestRowLockDMLModes(t *testing.T) {
	for i, clause := range rowLockTestClauses {
		for _, deleting := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/update", true: "/delete"}[deleting], func(t *testing.T) {
				e, a, b, c := rowLockEngineFixture(t)
				ar := enginetest.TransactionBegin(t, a)
				enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=1 "+clause, 10)
				query, mode := "UPDATE t SET v=v+1 WHERE id=1", lock.ForNoKeyUpdate
				if deleting {
					query, mode = "DELETE FROM t WHERE id=1", lock.ForUpdate
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				done := deleteAsync(t, b, ctx, query)
				if deleting || i != 0 {
					rowLockQueued(t, e, a.Transactions.Ticket, mode)
					enginetest.TransactionRows(t, c, "SELECT v FROM t WHERE id=1", 10)
					enginetest.TransactionEnd(t, a, ar, false)
					deleteAwait(t, done, 1, nil)
				} else {
					deleteAwait(t, done, 1, nil)
					enginetest.TransactionEnd(t, a, ar, false)
				}
				if deleting {
					enginetest.TransactionRows(t, c, "SELECT v FROM t WHERE id=1")
				} else {
					enginetest.TransactionRows(t, c, "SELECT v FROM t WHERE id=1", 11)
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
func TestRowLockReadCommitted(t *testing.T) {
	for _, change := range []string{"UPDATE t SET v=30 WHERE id=1", "DELETE FROM t WHERE id=1"} {
		for _, commit := range []bool{false, true} {
			for _, predicate := range []bool{false, true} {
				t.Run(change+map[bool]string{false: "/rollback", true: "/commit"}[commit]+map[bool]string{false: "/identity", true: "/predicate"}[predicate], func(t *testing.T) {
					e, a, b, c := rowLockEngineFixture(t)
					ar := enginetest.TransactionBegin(t, a)
					br := enginetest.TransactionBegin(t, b)
					enginetest.SessionSQL(t, a, change)
					query := "SELECT v FROM t WHERE id=1 FOR UPDATE"
					if predicate {
						query = "SELECT v FROM t WHERE v=10 FOR UPDATE"
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					done := deleteAsync(t, b, ctx, query)
					rowLockQueued(t, e, a.Transactions.Ticket, lock.ForUpdate)
					enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(3,10)")
					enginetest.SessionSQL(t, c, "UPDATE t SET v=10 WHERE id=2")
					enginetest.TransactionEnd(t, a, ar, commit)
					want := []int64{10}
					if commit {
						if predicate || change[0] == 'D' {
							want = nil
						} else {
							want = []int64{30}
						}
					}
					rowLockAwait(t, done, want...)
					if commit && predicate && change[0] != 'D' {
						blocked := deleteAsync(t, c, ctx, "UPDATE t SET v=31 WHERE id=1")
						rowLockQueued(t, e, b.Transactions.Ticket, lock.ForNoKeyUpdate)
						enginetest.TransactionEnd(t, b, br, false)
						deleteAwait(t, blocked, 1, nil)
					} else {
						enginetest.TransactionEnd(t, b, br, false)
					}
					enginetest.LockEmpty(t, e)
				})
			}
		}
	}
}
func TestRowLockOrdering(t *testing.T) {
	e, a, b, _ := rowLockEngineFixture(t)
	ar := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "UPDATE t SET v=30 WHERE id=1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := deleteAsync(t, b, ctx, "SELECT v FROM t ORDER BY v FOR SHARE")
	rowLockQueued(t, e, a.Transactions.Ticket, lock.ForShare)
	enginetest.TransactionEnd(t, a, ar, true)
	rowLockAwait(t, done, 30, 20)
	enginetest.LockEmpty(t, e)
}
func TestRowLockCancellation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e, a, b, _ := rowLockEngineFixture(t)
			ar := enginetest.TransactionBegin(t, a)
			enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
			if explicit {
				enginetest.TransactionBegin(t, b)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := deleteAsync(t, b, ctx, "SELECT v FROM t WHERE id=1 FOR KEY SHARE")
			rowLockQueued(t, e, a.Transactions.Ticket, lock.ForKeyShare)
			cancel()
			deleteAwait(t, done, 0, context.Canceled)
			if explicit {
				if b.TransactionState() != session.TxAborted {
					t.Fatal("explicit cancellation did not abort")
				}
				enginetest.SessionSQL(t, b, "ROLLBACK")
			}
			enginetest.TransactionEnd(t, a, ar, false)
			enginetest.TransactionRows(t, b, "SELECT v FROM t WHERE id=1 FOR SHARE", 10)
			enginetest.LockEmpty(t, e)
		})
	}
}
func TestRowLockJoins(t *testing.T) {
	for _, from := range []string{"t a,t b", "t a CROSS JOIN t b", "t a JOIN t b ON a.id=b.id", "t a JOIN t b USING(id)", "t a NATURAL JOIN t b", "(t a JOIN t b USING(id)) JOIN t c USING(id)"} {
		t.Run(from, func(t *testing.T) {
			e, a, b, c := rowLockEngineFixture(t)
			ar := enginetest.TransactionBegin(t, a)
			enginetest.TransactionRows(t, a, "SELECT a.v FROM "+from+" WHERE a.id=1 AND b.id=1 FOR SHARE", 10)
			enginetest.SessionSQL(t, b, "UPDATE t SET v=21 WHERE id=2")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := deleteAsync(t, b, ctx, "UPDATE t SET v=11 WHERE id=1")
			rowLockQueued(t, e, a.Transactions.Ticket, lock.ForNoKeyUpdate)
			enginetest.TransactionRows(t, c, "SELECT v FROM t WHERE id=1", 10)
			enginetest.TransactionEnd(t, a, ar, false)
			deleteAwait(t, done, 1, nil)
			enginetest.LockEmpty(t, e)
		})
	}
	t.Run("updated_join_key_recheck", func(t *testing.T) {
		e, a, b, c := rowLockEngineFixture(t)
		enginetest.SessionSQL(t, c, "CREATE TABLE u(id INT)")
		enginetest.SessionSQL(t, c, "INSERT INTO u VALUES(1)")
		ar := enginetest.TransactionBegin(t, a)
		enginetest.SessionSQL(t, a, "UPDATE t SET id=3 WHERE id=1")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done := deleteAsync(t, b, ctx, "SELECT t.v FROM t JOIN u ON t.id=u.id FOR SHARE")
		rowLockQueued(t, e, a.Transactions.Ticket, lock.ForShare)
		enginetest.TransactionEnd(t, a, ar, true)
		rowLockAwait(t, done)
		enginetest.LockEmpty(t, e)
	})
}
func TestRowLockIdentity(t *testing.T) {
	e, a, b, c := rowLockEngineFixture(t)
	ar := enginetest.TransactionBegin(t, a)
	enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
	enginetest.TransactionRows(t, b, "SELECT v FROM t WHERE id=2 FOR UPDATE", 20)
	enginetest.SessionSQL(t, a, "UPDATE t SET v=99 WHERE id=1")
	enginetest.TransactionRows(t, c, "SELECT v FROM t ORDER BY v", 10, 20)
	enginetest.TransactionEnd(t, a, ar, false)
	enginetest.LockEmpty(t, e)
}

func TestRowLockDMLCurrentValues(t *testing.T) {
	e, a, b, c := rowLockEngineFixture(t)
	ar := enginetest.TransactionBegin(t, a)
	enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR SHARE", 10)
	enginetest.SessionSQL(t, a, "UPDATE t SET v=20 WHERE id=1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := deleteAsync(t, b, ctx, "UPDATE t SET v=v+1 WHERE id=1")
	rowLockQueued(t, e, a.Transactions.Ticket, lock.ForNoKeyUpdate)
	enginetest.TransactionEnd(t, a, ar, true)
	deleteAwait(t, done, 1, nil)
	enginetest.TransactionRows(t, c, "SELECT v FROM t WHERE id=1", 21)
	enginetest.LockEmpty(t, e)
}
