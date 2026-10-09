package reference_test

import (
	"context"
	"errors"
	"testing"
	"time"
)

var referenceRowClauses = []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"}
var referenceRowConflicts = [4][4]bool{{false, false, false, true}, {false, false, true, true}, {false, true, true, true}, {true, true, true, true}}

func TestReferenceRowLockMatrix(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
			ap, bp := 0, 0
			if backend.pg {
				ap, bp = referenceRowPIDs(t, p.ctx, a, b)
			}
			for _, prepared := range []bool{false, true} {
				for requested := range referenceRowClauses {
					for held := range referenceRowClauses {
						t.Run(referenceRowClauses[requested]+"/"+referenceRowClauses[held]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
							tx, err := a.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							referenceRowValues(t, referenceRowQuery(p.ctx, tx, "SELECT v FROM t WHERE id=1 "+referenceRowClauses[held], prepared, backend.recorder.rawBackend), 10)
							referenceRowValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t WHERE id=2 "+referenceRowClauses[requested], prepared, backend.recorder.rawBackend), 20)
							referenceRowValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t WHERE id=1", prepared, backend.recorder.rawBackend), 10)
							query := "SELECT v FROM t WHERE id=1 " + referenceRowClauses[requested]
							if referenceRowConflicts[requested][held] {
								if backend.pg {
									ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
									defer cancel()
									done := referenceRowAsync(t, ctx, b, query, prepared, true, backend.recorder.rawBackend)
									referenceRowWait(t, ctx, bp, ap)
									if err := tx.Rollback(); err != nil {
										t.Fatal(err)
									}
									referenceRowValues(t, referenceLockResult(t, done), 10)
								} else {
									// Public deadline is a boundary witness; native engine companions prove
									// exact journal enqueue, without introducing a public inspection API.
									ctx, cancel := context.WithTimeout(p.ctx, 30*time.Millisecond)
									got := referenceRowQuery(ctx, b, query, prepared, backend.recorder.rawBackend)
									cancel()
									if got.rows != nil {
										got.rows.Close()
										t.Fatal("conflict exposed rows")
									}
									if got.stmt != nil {
										got.stmt.Close()
									}
									if !errors.Is(got.err, context.DeadlineExceeded) {
										t.Fatal(got.err)
									}
									var state interface{ SQLState() string }
									if errors.As(got.err, &state) {
										t.Fatal("caller deadline fabricated SQLSTATE", got.err)
									}
									if err := tx.Rollback(); err != nil {
										t.Fatal(err)
									}
									referenceRowValues(t, referenceRowQuery(p.ctx, b, query, prepared, backend.recorder.rawBackend), 10)
								}
							} else {
								referenceRowValues(t, referenceRowQuery(p.ctx, b, query, prepared, backend.recorder.rawBackend), 10)
								if err := tx.Rollback(); err != nil {
									t.Fatal(err)
								}
							}
						})
					}
				}
			}
		})
	}
}
func TestReferenceRowLockDMLModes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
			ap, bp := 0, 0
			if backend.pg {
				ap, bp = referenceRowPIDs(t, p.ctx, a, b)
			}
			for _, prepared := range []bool{false, true} {
				for i, clause := range referenceRowClauses {
					for _, deleting := range []bool{false, true} {
						t.Run(clause+map[bool]string{false: "/update", true: "/delete"}[deleting]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
							for _, partitioned := range []bool{false, true} {
								t.Run(map[bool]string{false: "whole_table", true: "distinct_row"}[partitioned], func(t *testing.T) {
									referenceLockExec(t, p.ctx, a, "DELETE FROM t", 0)
									where := ""
									count := int64(1)
									if partitioned {
										referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
										where = " WHERE id=1"
										count = 2
									} else {
										referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10)", 1)
									}
									tx, err := a.BeginTx(p.ctx, nil)
									if err != nil {
										t.Fatal(err)
									}
									defer tx.Rollback()
									referenceRowValues(t, referenceRowQuery(p.ctx, tx, "SELECT v FROM t"+where+" "+clause, prepared, backend.recorder.rawBackend), 10)
									if partitioned {
										referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=21 WHERE id=2", prepared, 1)
									}
									query := "UPDATE t SET v=11" + where
									if deleting {
										query = "DELETE FROM t" + where
									}
									if deleting || i != 0 {
										if backend.pg {
											ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
											defer cancel()
											done := referenceRowAsync(t, ctx, b, query, prepared, false)
											referenceRowWait(t, ctx, bp, ap)
											if err := tx.Rollback(); err != nil {
												t.Fatal(err)
											}
											got := referenceLockResult(t, done)
											if got.err != nil {
												t.Fatal(got.err)
											}
											referenceLockAffected(t, got.result, 1)
										} else {
											ctx, cancel := context.WithTimeout(p.ctx, 30*time.Millisecond)
											outcome := referenceRowExecute(ctx, b, query, prepared)
											cancel()
											if !errors.Is(outcome.err, context.DeadlineExceeded) {
												t.Fatal(outcome.err)
											}
											if outcome.result != nil {
												t.Fatal("failed DML returned result", outcome.result)
											}
											var state interface{ SQLState() string }
											if errors.As(outcome.err, &state) {
												t.Fatal("caller deadline fabricated SQLSTATE", outcome.err)
											}
											if err := tx.Rollback(); err != nil {
												t.Fatal(err)
											}
											referenceRowAffected(t, p.ctx, b, query, prepared, 1)
										}
									} else {
										referenceRowAffected(t, p.ctx, b, query, prepared, 1)
										if err := tx.Rollback(); err != nil {
											t.Fatal(err)
										}
										if partitioned {
											referenceRowAffected(t, p.ctx, b, query, prepared, 1)
										}
									}
									if partitioned {
										referenceRowValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t WHERE id=2", prepared, backend.recorder.rawBackend), 21)
									}
									if deleting {
										referenceRowValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t"+where, prepared, backend.recorder.rawBackend))
										if partitioned {
											referenceLockExec(t, p.ctx, b, "DELETE FROM t", 1)
										}
									} else {
										referenceRowValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t"+where, prepared, backend.recorder.rawBackend), 11)
										referenceLockExec(t, p.ctx, b, "DELETE FROM t", count)
									}
								})
							}
						})
					}
				}
			}
		})
	}
}
