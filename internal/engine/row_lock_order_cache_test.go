package engine

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRowLockOrderedCacheFrontier(t *testing.T) {
	for _, source := range []string{"t", "w", "stored"} {
		for _, change := range []string{"UPDATE t SET v=30 WHERE id=1", "DELETE FROM t WHERE id=1"} {
			for _, commit := range []bool{false, true} {
				t.Run(source+change+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
					e, a, b, c := rowLockEngineFixture(t)
					sessionSQL(t, c, "CREATE VIEW inner_view AS SELECT id,v+1 AS val FROM t ORDER BY val,id")
					sessionSQL(t, c, "CREATE VIEW w AS SELECT id,val+1 AS shifted FROM inner_view ORDER BY shifted,id")
					sessionSQL(t, c, "CREATE VIEW stored AS SELECT id,v FROM t ORDER BY v+1,id FOR UPDATE")
					expression := "v+1"
					want := []int64{11, 21}
					if source == "w" {
						expression = "shifted+1"
						want = []int64{13, 23}
					}
					query := "SELECT " + expression + " AS k FROM " + source + " ORDER BY k,id"
					if source != "stored" {
						query += " FOR UPDATE"
					}
					ar := transactionBegin(t, a)
					transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
					sessionSQL(t, a, change)
					done := rowLockViewAsync(t, b, context.Background(), query)
					rowLockQueued(t, e, a.transactions.ticket, lock.ForUpdate)
					transactionEnd(t, a, ar, commit)
					if commit {
						if change[0] == 'D' {
							want = want[1:]
						} else {
							want[0] += 20
							if source == "stored" {
								want[0], want[1] = want[1], want[0]
							}
						}
					}
					rowLockAwait(t, done, want...)
					lockEmpty(t, e)
				})
			}
		}
	}
	for _, source := range []string{"t", "w", "stored", "stored_rows"} {
		for _, replacement := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run("volatile/"+source+map[bool]string{false: "/unchanged", true: "/replacement"}[replacement]+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
					e, a, b, c := rowLockEngineFixture(t)
					sessionSQL(t, c, "CREATE SEQUENCE s")
					sessionSQL(t, c, "CREATE VIEW w AS SELECT id,v FROM t WHERE id=1")
					sessionSQL(t, c, "CREATE VIEW stored AS SELECT id,nextval('s') AS n FROM t WHERE id=1 ORDER BY n,n FOR UPDATE")
					sessionSQL(t, c, "CREATE VIEW stored_rows AS SELECT id,v FROM t WHERE id=1 FOR UPDATE")
					query := "SELECT nextval('s') AS n FROM " + source + " WHERE id=1 ORDER BY n,n FOR UPDATE"
					if source == "stored" {
						query = "SELECT n FROM stored"
					}
					if source == "stored_rows" {
						query = "SELECT nextval('s') AS n FROM stored_rows ORDER BY n,n"
					} // Stored-local marks settle before the consuming outer Sort.
					want := int64(1)
					if replacement {
						ar := transactionBegin(t, a)
						transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
						sessionSQL(t, a, "UPDATE t SET v=30 WHERE id=1")
						done := rowLockViewAsync(t, b, context.Background(), query)
						rowLockQueued(t, e, a.transactions.ticket, lock.ForUpdate)
						transactionEnd(t, a, ar, commit)
						if commit && source != "stored_rows" {
							want = 2
						}
						rowLockAwait(t, done, want)
					} else {
						rowLockViewRows(t, e, b, query, true, want)
					}
					transactionRows(t, b, "SELECT currval('s')", want)
					lockEmpty(t, e)
				})
			}
		}
	}
	for _, clause := range rowLockTestClauses {
		t.Run("RR/"+clause, func(t *testing.T) {
			e, a, b, c := rowLockEngineFixture(t)
			sessionSQL(t, c, "CREATE VIEW w AS SELECT id,v+1 AS val FROM t ORDER BY val,id")
			sessionSQL(t, b, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			transactionRows(t, b, "SELECT v FROM t ORDER BY id", 10, 20)
			sessionSQL(t, a, "UPDATE t SET v=30 WHERE id=1")
			query := "SELECT val+1 AS k FROM w ORDER BY k,id " + clause
			if clause == "FOR KEY SHARE" {
				rowLockViewRows(t, e, b, query, true, 12, 22)
			} else {
				result, err := rowLockViewExecute(t, e, b, query, true)
				if result.Rows != nil {
					result.Rows.Close()
					t.Fatal("error cursor")
				}
				waitsDiagnostic(t, err, "40001", "could not serialize access due to concurrent update")
			}
			sessionSQL(t, b, "ROLLBACK")
			lockEmpty(t, e)
		})
	}
}

func TestRowLockOrderedCacheRejection(t *testing.T) {
	e, a, b, c := rowLockEngineFixture(t)
	sessionSQL(t, c, "CREATE TABLE u(id INT)")
	sessionSQL(t, c, "INSERT INTO u VALUES(1),(1),(2),(2)")
	sessionSQL(t, c, "CREATE VIEW w AS SELECT t.id,t.v+1 AS k FROM t JOIN u USING(id) WHERE t.v<25 ORDER BY k,id")
	ar := transactionBegin(t, a)
	transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
	sessionSQL(t, a, "UPDATE t SET v=30 WHERE id=1")
	done := rowLockViewAsync(t, b, context.Background(), "SELECT k+1 AS output FROM w ORDER BY output,id FOR UPDATE")
	rowLockQueued(t, e, a.transactions.ticket, lock.ForUpdate)
	transactionEnd(t, a, ar, true)
	rowLockAwait(t, done, 22, 22)
	lockEmpty(t, e)
	sessionSQL(t, c, "CREATE VIEW stored AS SELECT id,v+1 AS k FROM t ORDER BY k,id FOR UPDATE")
	sessionSQL(t, c, "CREATE VIEW grouped AS SELECT count(*) AS n FROM stored WHERE id=1")
	ar = transactionBegin(t, a)
	transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 30)
	sessionSQL(t, a, "UPDATE t SET v=40 WHERE id=1")
	done = rowLockViewAsync(t, b, context.Background(), "SELECT n FROM grouped")
	rowLockQueued(t, e, a.transactions.ticket, lock.ForUpdate)
	transactionEnd(t, a, ar, true)
	rowLockAwait(t, done, 1)
	lockEmpty(t, e)
}

func TestRowLockOrderedCacheRefreshCounts(t *testing.T) {
	_, query, _ := queryCoreFixture(t, "SELECT id FROM t WHERE f ORDER BY id")
	usage := query.Root.Schema()[0].Usage
	value, err := sqltype.Integer(usage.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	calls := [3]int{}
	probe := func(slot int, properties expr.Properties) expr.Expr {
		return queryProbeExpression{Info: expr.Info{Usage: usage, Property: properties}, eval: func(*EvalContext) (sqltype.Value, error) { calls[slot]++; return value, nil }}
	}
	volatile := probe(0, expr.Properties{Volatility: expr.Volatile})
	hidden := probe(1, expr.Properties{Volatility: expr.Volatile})
	immutable := probe(2, expr.Properties{})
	sorted := &plan.SortNode{Keys: []plan.SortKey{{Expression: volatile, Cache: 0, Output: true}, {Expression: volatile, Cache: 0, Output: true}, {Expression: hidden, Cache: 1}, {Expression: immutable, Cache: 2, Output: true}}}
	evaluation := &EvalContext{evalFrame: evalFrame{OrderValues: []sqltype.Value{value, value, value}}}
	if err := RefreshLockedOrder(sorted, nil, evaluation); err != nil {
		t.Fatal(err)
	}
	if calls != [3]int{1, 0, 0} {
		t.Fatal("replacement cache evaluations", calls)
	}
}
