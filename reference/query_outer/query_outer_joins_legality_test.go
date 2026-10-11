package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceQueryOuterJoinsRowLocks(t *testing.T) {
	fixture.OuterJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE VIEW outer_view AS SELECT a.k AS ak,a.l,b.k AS bk,b.r FROM a LEFT JOIN b ON a.k=b.k", 0),
			fixture.Exec("CREATE VIEW nested_outer AS SELECT * FROM outer_view", 0),
			fixture.Exec("CREATE VIEW grouped_outer AS SELECT ak,count(*) AS n FROM outer_view GROUP BY ak", 0),
		} {
			fixture.RunStep(t, p.Ctx, conn, recorder, s)
		}
		for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
			for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
				for _, operands := range []string{"a " + kind + " JOIN b", "empty_a a " + kind + " JOIN empty_b b"} {
					for _, condition := range []string{"a.k=b.k", "TRUE"} {
						for _, limit := range []string{"", " LIMIT 0"} {
							for _, prepared := range []bool{false, true} {
								outerJoinsAssert(t, p, conn, recorder, "SELECT * FROM "+operands+" ON "+condition+limit+" "+clause, prepared)
							}
						}
					}
				}
			}
			for _, view := range []string{"outer_view", "nested_outer", "grouped_outer"} {
				for _, limit := range []string{"", " LIMIT 0"} {
					for _, prepared := range []bool{false, true} {
						outerJoinsAssert(t, p, conn, recorder, "SELECT * FROM "+view+limit+" "+clause, prepared)
					}
				}
			}
		}
	})
}

// PostgreSQL observes actual blocker PIDs; native proves conflict through public
// cancellation. Neither mode uses sleeping as evidence that a row is locked.
// PostgreSQL's physical plan can build the unpreserved marked input first.
// Native preserves the specified left-major source-local demand frontier.
var outerJoinsNativeEmptyLocks = map[[2]string]bool{
	{"SELECT a.k,a.l,b.k,b.r FROM empty_a a LEFT JOIN locked_b b ON a.k=b.k", "UPDATE b SET r=r WHERE k=3"}:  false,
	{"SELECT a.k,a.l,b.k,b.r FROM empty_a a LEFT JOIN nested_b b ON a.k=b.k", "UPDATE b SET r=r WHERE k=3"}:  false,
	{"SELECT a.k,a.l,b.k,b.r FROM empty_a a LEFT JOIN sorted_b b ON a.k=b.k", "UPDATE b SET r=r WHERE k=3"}:  false,
	{"SELECT a.k,a.l,b.k,b.r FROM locked_a a RIGHT JOIN empty_b b ON a.k=b.k", "UPDATE a SET l=l WHERE k=2"}: true,
	{"SELECT a.k,a.l,b.k,b.r FROM nested_a a RIGHT JOIN empty_b b ON a.k=b.k", "UPDATE a SET l=l WHERE k=2"}: true,
	{"SELECT a.k,a.l,b.k,b.r FROM sorted_a a RIGHT JOIN empty_b b ON a.k=b.k", "UPDATE a SET l=l WHERE k=2"}: true,
}

func outerJoinsAssertPhysicalLock(t *testing.T, p *fixture.Pair, holderConn, contender *sql.Conn, backend fixture.Backend, query, update string, prepared bool, count int64) {
	t.Helper()
	holder, err := holderConn.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if !outerJoinsAssert(t, p, holder, backend.Recorder, query, prepared) {
		return
	}
	locked, ok := outerJoinsLocks[[2]string{query, update}]
	if !ok {
		t.Fatalf("missing independent lock expectation: %s / %s", query, update)
	}
	if !backend.PG {
		if native, differs := outerJoinsNativeEmptyLocks[[2]string{query, update}]; differs {
			locked = native
		}
	}
	if !locked {
		fixture.RowAffected(t, p.Ctx, contender, update, prepared, count)
	} else if backend.PG {
		holderPID, contenderPID := fixture.RowPIDs(t, p.Ctx, holderConn, contender)
		done := fixture.RowAsync(t, p.Ctx, contender, update, prepared, false)
		fixture.RowWait(t, p.Ctx, contenderPID, holderPID)
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		outcome := fixture.LockResult(t, done)
		if outcome.Err != nil {
			t.Fatal(outcome.Err)
		}
		fixture.LockAffected(t, outcome.Result, count)
	} else {
		bounded, cancel := context.WithTimeout(p.Ctx, 30*time.Millisecond)
		defer cancel()
		outcome := fixture.RowExecute(bounded, contender, update, prepared)
		if !errors.Is(outcome.Err, context.DeadlineExceeded) || outcome.Result != nil {
			t.Fatalf("selected marked row did not conflict: %#v", outcome)
		}
	}
	if err := holder.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	fixture.RowAffected(t, p.Ctx, contender, update, prepared, count)
}

func TestReferenceQueryOuterJoinsLocalViewMarks(t *testing.T) {
	for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
		for _, placement := range []struct{ name, left, right string }{
			{"left", "locked_a", "b"}, {"right", "a", "locked_b"}, {"both", "locked_a", "locked_b"},
			{"nested_left", "nested_a", "b"}, {"nested_right", "a", "nested_b"},
			{"sorted_left", "sorted_a", "b"}, {"sorted_right", "a", "sorted_b"},
		} {
			t.Run(kind+"/"+placement.name, func(t *testing.T) {
				fixture.OuterJoinsBackendScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, backend fixture.Backend) {
					for _, s := range []fixture.Step{
						fixture.Exec("CREATE VIEW locked_a AS SELECT * FROM a FOR UPDATE", 0), fixture.Exec("CREATE VIEW locked_b AS SELECT * FROM b FOR UPDATE", 0),
						fixture.Exec("CREATE VIEW nested_a AS SELECT * FROM locked_a", 0), fixture.Exec("CREATE VIEW nested_b AS SELECT * FROM locked_b", 0),
						fixture.Exec("CREATE VIEW sorted_a AS SELECT * FROM a ORDER BY k FOR UPDATE", 0), fixture.Exec("CREATE VIEW sorted_b AS SELECT * FROM b ORDER BY k FOR UPDATE", 0),
					} {
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
					}
					contender, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { contender.Close() })
					for _, prepared := range []bool{false, true} {
						suffixes := []string{"", " LIMIT 0"}
						if kind == "LEFT" && placement.name == "sorted_left" {
							suffixes = append(suffixes, " LIMIT 1")
						}
						for _, suffix := range suffixes {
							query := "SELECT a.k,a.l,b.k,b.r FROM " + placement.left + " a " + kind + " JOIN " + placement.right + " b ON a.k=b.k" + suffix
							for _, probe := range []struct {
								sql   string
								count int64
							}{
								{"UPDATE a SET l=l WHERE k=1", 2}, {"UPDATE a SET l=l WHERE k=2", 1}, {"UPDATE a SET l=l WHERE k IS NULL", 1},
								{"UPDATE b SET r=r WHERE k=1", 2}, {"UPDATE b SET r=r WHERE k=3", 1}, {"UPDATE b SET r=r WHERE k IS NULL", 1},
							} {
								outerJoinsAssertPhysicalLock(t, p, conn, contender, backend, query, probe.sql, prepared, probe.count)
							}
						}
						for _, query := range []string{
							"SELECT a.k,a.l,b.k,b.r FROM " + placement.left + " a " + kind + " JOIN empty_b b ON a.k=b.k",
							"SELECT a.k,a.l,b.k,b.r FROM empty_a a " + kind + " JOIN " + placement.right + " b ON a.k=b.k",
							"SELECT count(*) FROM " + placement.left + " a " + kind + " JOIN " + placement.right + " b ON a.k=b.k",
						} {
							outerJoinsAssertPhysicalLock(t, p, conn, contender, backend, query, "UPDATE a SET l=l WHERE k=2", prepared, 1)
							outerJoinsAssertPhysicalLock(t, p, conn, contender, backend, query, "UPDATE b SET r=r WHERE k=3", prepared, 1)
						}
					}
				})
			})
		}
	}
}
