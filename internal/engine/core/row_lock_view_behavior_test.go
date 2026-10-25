package core_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

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

// Capture the physical target before launching a waiter; queue assertions must
// identify the source row, including after native layout remapping.
func rowLockViewFirstResource(t *testing.T, e *engine.Engine, table string) lock.Resource {
	t.Helper()
	ref := enginetest.RoleTableRef(t, e, table)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return lock.Resource{ObjectRef: ref, Row: e.Storage.Relations[ref.Address].Versions[0].Row}
}

func rowLockViewFixture(t *testing.T) (*engine.Engine, *session.Session, *session.Session, *session.Session) {
	e, a, b, c := rowLockEngineFixture(t)
	enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(3,30)")
	enginetest.SessionSQL(t, c, "CREATE VIEW v AS SELECT id,v FROM t WHERE id<3")
	enginetest.SessionSQL(t, c, "CREATE VIEW w AS SELECT id,v+1 AS val FROM v")
	return e, a, b, c
}

func TestRowLockViews(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e, a, b, _ := rowLockViewFixture(t)
				ar := enginetest.TransactionBegin(t, a)
				rowLockViewRows(t, e, a, "SELECT val FROM w WHERE id=1 "+clause, prepared, 11)
				rowLockViewGrants(t, e, a.Transactions.Ticket, "t", 1)
				resource := rowLockViewFirstResource(t, e, "t")
				enginetest.TransactionRows(t, b, "SELECT v FROM t WHERE id=1", 10)
				for _, id := range []string{"2", "3"} {
					if result := enginetest.SessionSQL(t, b, "UPDATE t SET v=v+1 WHERE id="+id); result.Count != 1 {
						t.Fatal(result)
					}
				}
				if clause == "FOR KEY SHARE" {
					enginetest.SessionSQL(t, b, "UPDATE t SET v=40 WHERE id=1")
				} else {
					ctx := lockObservedDeadline(t)
					done := rowLockViewAsync(t, b, ctx, "UPDATE t SET v=40 WHERE id=1")
					rowLockQueued(t, e, a.Transactions.Ticket, lock.ForNoKeyUpdate)
					lockObservedRequest(t, e, b, resource, lock.ForNoKeyUpdate)
					ctx.arm(50 * time.Millisecond)
					deleteAwait(t, done, 0, context.DeadlineExceeded)
				}
				ctx := lockObservedDeadline(t)
				done := rowLockViewAsync(t, b, ctx, "DELETE FROM t WHERE id=1")
				rowLockQueued(t, e, a.Transactions.Ticket, lock.ForUpdate)
				lockObservedRequest(t, e, b, resource, lock.ForUpdate)
				ctx.arm(50 * time.Millisecond)
				deleteAwait(t, done, 0, context.DeadlineExceeded)
				enginetest.TransactionEnd(t, a, ar, false)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
func TestRowLockViewProvenance(t *testing.T) {
	for _, body := range []string{
		"SELECT a.id,a.v FROM t a JOIN t b USING(id) WHERE a.id<3",
		"SELECT a.id,a.v FROM t a NATURAL JOIN t b WHERE a.id<3",
		"SELECT a.id,a.v FROM t a JOIN t b ON a.id=b.id WHERE a.id<3",
		"SELECT a.id,7 AS v FROM t a CROSS JOIN t b WHERE a.id=1 AND b.id=1",
	} {
		t.Run(body, func(t *testing.T) {
			e, a, b, _ := rowLockEngineFixture(t)
			enginetest.SessionSQL(t, b, "CREATE VIEW joined_view AS "+body)
			ar := enginetest.TransactionBegin(t, a)
			enginetest.TransactionRows(t, a, "SELECT id FROM joined_view WHERE id=1 FOR UPDATE", 1)
			rowLockViewGrants(t, e, a.Transactions.Ticket, "t", 1)
			enginetest.SessionSQL(t, b, "UPDATE t SET v=99 WHERE id=2")
			enginetest.TransactionEnd(t, a, ar, false)
			enginetest.LockEmpty(t, e)
		})
	}
	t.Run("hidden_distinct_contributor", func(t *testing.T) {
		e, a, b, _ := rowLockEngineFixture(t)
		enginetest.SessionSQL(t, b, "CREATE TABLE u(id INT)")
		enginetest.SessionSQL(t, b, "INSERT INTO u VALUES(1),(1),(3)")
		enginetest.SessionSQL(t, b, "CREATE VIEW j AS SELECT t.v FROM t JOIN u ON t.id=u.id")
		ar := enginetest.TransactionBegin(t, a)
		enginetest.TransactionRows(t, a, "SELECT v FROM j FOR SHARE", 10, 10)
		for _, table := range []string{"t", "u"} {
			resource := rowLockViewFirstResource(t, e, table)
			ctx := lockObservedDeadline(t)
			done := rowLockViewAsync(t, b, ctx, "DELETE FROM "+table+" WHERE id=1")
			rowLockQueued(t, e, a.Transactions.Ticket, lock.ForUpdate)
			lockObservedRequest(t, e, b, resource, lock.ForUpdate)
			ctx.arm(50 * time.Millisecond)
			deleteAwait(t, done, 0, context.DeadlineExceeded)
		}
		enginetest.SessionSQL(t, b, "UPDATE u SET id=4 WHERE id=3")
		enginetest.TransactionEnd(t, a, ar, false)
		enginetest.LockEmpty(t, e)
	})
	t.Run("zero_width_and_empty", func(t *testing.T) {
		e, a, b, _ := rowLockViewFixture(t)
		enginetest.SessionSQL(t, b, "CREATE VIEW z AS SELECT FROM t WHERE id=1")
		ar := enginetest.TransactionBegin(t, a)
		result, err := rowLockViewExecute(t, e, a, "SELECT FROM z FOR UPDATE", true)
		if err != nil || result.Count != 1 || result.Rows == nil {
			t.Fatal(result, err)
		}
		if len(result.Rows.Schema()) != 0 {
			t.Fatal(result.Rows.Schema())
		}
		result.Rows.Close()
		rowLockViewGrants(t, e, a.Transactions.Ticket, "t", 1)
		enginetest.TransactionEnd(t, a, ar, false)
		ar = enginetest.TransactionBegin(t, a)
		enginetest.TransactionRows(t, a, "SELECT val FROM w WHERE FALSE FOR UPDATE")
		rowLockViewGrants(t, e, a.Transactions.Ticket, "t")
		enginetest.TransactionEnd(t, a, ar, false)
		enginetest.LockEmpty(t, e)
	})
}
func TestRowLockViewRecheck(t *testing.T) {
	for _, tc := range []struct {
		body, query string
		want        []int64
	}{
		{"SELECT id,v+1 AS val FROM t", "SELECT val FROM w WHERE id=1 FOR UPDATE", []int64{21}},
		{"SELECT id,v+1 AS val FROM t WHERE v=10", "SELECT val FROM w WHERE id=1 FOR UPDATE", nil},
		{"SELECT id,v+1 AS val FROM t", "SELECT val FROM w WHERE val=11 FOR UPDATE", nil},
		{"SELECT a.id,a.v+1 AS val FROM t a JOIN t b ON a.v=b.v WHERE b.id=2", "SELECT val FROM w WHERE id=1 FOR UPDATE", nil},
	} {
		t.Run(tc.body+tc.query, func(t *testing.T) {
			e, a, b, c := rowLockEngineFixture(t)
			if strings.Contains(tc.body, "JOIN") {
				enginetest.SessionSQL(t, c, "UPDATE t SET v=10 WHERE id=2")
			}
			enginetest.SessionSQL(t, c, "CREATE VIEW w AS "+tc.body)
			ar := enginetest.TransactionBegin(t, a)
			enginetest.TransactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
			enginetest.SessionSQL(t, a, "UPDATE t SET v=20 WHERE id=1")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := rowLockViewAsync(t, b, ctx, tc.query)
			rowLockQueued(t, e, a.Transactions.Ticket, lock.ForUpdate)
			enginetest.TransactionEnd(t, a, ar, true)
			rowLockAwait(t, done, tc.want...)
			enginetest.LockEmpty(t, e)
		})
	}
	for _, clause := range rowLockTestClauses {
		t.Run("RR/"+clause, func(t *testing.T) {
			e, a, b, _ := rowLockViewFixture(t)
			enginetest.SessionSQL(t, b, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			enginetest.TransactionRows(t, b, "SELECT val FROM w WHERE id=1", 11)
			enginetest.SessionSQL(t, a, "UPDATE t SET v=20 WHERE id=1")
			result, err := rowLockViewExecute(t, e, b, "SELECT val FROM w WHERE val=11 "+clause, true)
			if clause == "FOR KEY SHARE" {
				if err != nil {
					t.Fatal(err)
				}
				defer result.Rows.Close()
				if got := enginetest.Tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(11)}}) {
					t.Fatal(got)
				}
			} else {
				if result.Rows != nil {
					result.Rows.Close()
					t.Fatal("error cursor")
				}
				waitsDiagnostic(t, err, "40001", "could not serialize access due to concurrent update")
			}
			enginetest.SessionSQL(t, b, "ROLLBACK")
			enginetest.LockEmpty(t, e)
		})
	}
}
func TestRowLockViewStored(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		t.Run(clause, func(t *testing.T) {
			e, a, b, _ := rowLockEngineFixture(t)
			enginetest.SessionSQL(t, b, "CREATE VIEW stored AS SELECT id,v FROM t "+clause)
			enginetest.LockEmpty(t, e)
			ar := enginetest.TransactionBegin(t, a)
			enginetest.TransactionRows(t, a, "SELECT v FROM stored WHERE id=1", 10)
			rowLockViewGrants(t, e, a.Transactions.Ticket, "t", 1)
			enginetest.SessionSQL(t, b, "UPDATE t SET v=30 WHERE id=2")
			enginetest.TransactionEnd(t, a, ar, false)
			enginetest.LockEmpty(t, e)
		})
	}
	t.Run("strength_and_unrelated_source", func(t *testing.T) {
		e, a, b, _ := rowLockEngineFixture(t)
		enginetest.SessionSQL(t, b, "CREATE TABLE u(id INT)")
		enginetest.SessionSQL(t, b, "INSERT INTO u VALUES(1)")
		enginetest.SessionSQL(t, b, "CREATE VIEW stored AS SELECT id,v FROM t FOR SHARE")
		ar := enginetest.TransactionBegin(t, a)
		enginetest.TransactionRows(t, a, "SELECT stored.v FROM stored CROSS JOIN u WHERE stored.id=1", 10)
		enginetest.SessionSQL(t, b, "DELETE FROM u")
		enginetest.TransactionEnd(t, a, ar, false)
		ar = enginetest.TransactionBegin(t, a)
		enginetest.TransactionRows(t, a, "SELECT v FROM stored WHERE id=1 FOR NO KEY UPDATE", 10)
		enginetest.TransactionRows(t, b, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", 10)
		resource := rowLockViewFirstResource(t, e, "t")
		ctx := lockObservedDeadline(t)
		done := rowLockViewAsync(t, b, ctx, "SELECT v FROM t WHERE id=1 FOR SHARE")
		rowLockQueued(t, e, a.Transactions.Ticket, lock.ForShare)
		lockObservedRequest(t, e, b, resource, lock.ForShare)
		ctx.arm(50 * time.Millisecond)
		deleteAwait(t, done, 0, context.DeadlineExceeded)
		enginetest.TransactionEnd(t, a, ar, false)
		enginetest.LockEmpty(t, e)
	})
	t.Run("aggregate_consumption", func(t *testing.T) {
		e, a, b, _ := rowLockEngineFixture(t)
		enginetest.SessionSQL(t, b, "CREATE VIEW stored AS SELECT id,v FROM t FOR UPDATE")
		enginetest.SessionSQL(t, b, "CREATE VIEW grouped AS SELECT count(*) AS n FROM stored")
		ar := enginetest.TransactionBegin(t, a)
		enginetest.TransactionRows(t, a, "SELECT n FROM grouped", 2)
		rowLockViewGrants(t, e, a.Transactions.Ticket, "t", 1, 2)
		enginetest.TransactionEnd(t, a, ar, false)
		enginetest.LockEmpty(t, e)
		enginetest.SessionSQL(t, b, "CREATE VIEW ordinary_group AS SELECT count(*) AS n FROM t")
		ar = enginetest.TransactionBegin(t, a)
		enginetest.TransactionRows(t, a, "SELECT ordinary_group.n FROM stored CROSS JOIN ordinary_group WHERE stored.id=1", 2)
		rowLockViewGrants(t, e, a.Transactions.Ticket, "t", 1)
		enginetest.TransactionEnd(t, a, ar, false)
		enginetest.LockEmpty(t, e)
	})
	t.Run("native_roundtrip_layout", func(t *testing.T) {
		e, a, b, _ := rowLockEngineFixture(t)
		enginetest.SessionSQL(t, b, "CREATE VIEW stored AS SELECT v FROM t ORDER BY v FOR UPDATE")
		enginetest.SessionSQL(t, b, "ALTER TABLE t RENAME COLUMN v TO val")
		enginetest.SessionSQL(t, b, "ALTER TABLE t DROP COLUMN id")
		var dump bytes.Buffer
		if err := engine.Dump(context.Background(), e, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "FOR UPDATE") {
			t.Fatal("lost stored mark", dump.String())
		}
		if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		ar := enginetest.TransactionBegin(t, a)
		enginetest.TransactionRows(t, a, "SELECT v FROM stored FOR KEY SHARE", 10, 20)
		resource := rowLockViewFirstResource(t, e, "t")
		ctx := lockObservedDeadline(t)
		done := rowLockViewAsync(t, b, ctx, "SELECT val FROM t WHERE val=10 FOR KEY SHARE")
		rowLockQueued(t, e, a.Transactions.Ticket, lock.ForKeyShare)
		lockObservedRequest(t, e, b, resource, lock.ForKeyShare)
		ctx.arm(50 * time.Millisecond)
		deleteAwait(t, done, 0, context.DeadlineExceeded)
		enginetest.TransactionEnd(t, a, ar, false)
		enginetest.LockEmpty(t, e)
	})
}
func TestRowLockViewDiagnostics(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		for _, tc := range []struct{ body, suffix string }{
			{"SELECT id FROM t GROUP BY id", "GROUP BY clause"},
			{"SELECT count(*) AS n FROM t HAVING TRUE", "HAVING clause"},
			{"SELECT count(*) AS n FROM t", "aggregate functions"},
		} {
			t.Run(clause+tc.suffix, func(t *testing.T) {
				e, a, b, _ := rowLockEngineFixture(t)
				enginetest.SessionSQL(t, b, "CREATE VIEW grouped AS "+tc.body)
				result, err := rowLockViewExecute(t, e, a, "SELECT * FROM grouped "+clause, true)
				if result.Rows != nil {
					result.Rows.Close()
					t.Fatal("error cursor")
				}
				waitsDiagnostic(t, err, "0A000", clause+" is not allowed with "+tc.suffix)
				enginetest.LockEmpty(t, e)
			})
		}
	}
	for _, clause := range rowLockTestClauses {
		t.Run("stored_readonly/"+clause, func(t *testing.T) {
			e, a, b, _ := rowLockEngineFixture(t)
			enginetest.SessionSQL(t, b, "CREATE VIEW stored AS SELECT v FROM t "+clause)
			enginetest.SessionSQL(t, a, "BEGIN READ ONLY")
			result, err := rowLockViewExecute(t, e, a, "SELECT v FROM stored WHERE FALSE", true)
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("readonly cursor")
			}
			waitsDiagnostic(t, err, "25006", "cannot execute SELECT in a read-only transaction")
			enginetest.SessionSQL(t, a, "ROLLBACK")
			enginetest.LockEmpty(t, e)
		})
	}
}
func TestRowLockVirtualCatalogBoundary(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		for _, prepared := range []bool{false, true} {
			for _, source := range []string{"pg_catalog.pg_class", "pg_catalog.pg_rewrite", "pg_catalog.pg_views"} {
				t.Run(clause+source+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					e, a, b, _ := rowLockEngineFixture(t)
					enginetest.SessionSQL(t, b, "CREATE VIEW catalog_view AS SELECT * FROM "+source)
					enginetest.SessionSQL(t, b, "CREATE VIEW stored_catalog AS SELECT * FROM catalog_view "+clause)
					for _, q := range []string{"SELECT * FROM " + source + " WHERE FALSE " + clause, "SELECT * FROM catalog_view WHERE FALSE " + clause, "SELECT * FROM stored_catalog WHERE FALSE", "SELECT t.v FROM t CROSS JOIN catalog_view WHERE FALSE " + clause} {
						result, err := rowLockViewExecute(t, e, a, q, prepared)
						if result.Rows != nil {
							result.Rows.Close()
							t.Fatal("virtual cursor")
						}
						waitsDiagnostic(t, err, "0A000", "row locking is not supported for virtual catalog relations")
						enginetest.LockEmpty(t, e)
					}
				})
			}
		}
	}
}
