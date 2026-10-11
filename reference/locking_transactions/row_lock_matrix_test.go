package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

var referenceRowConflicts = [4][4]bool{{false, false, false, true}, {false, false, true, true}, {false, true, true, true}, {true, true, true, true}}

func TestReferenceRowLockMatrix(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
			ap, bp := 0, 0
			if backend.PG {
				ap, bp = fixture.RowPIDs(t, p.Ctx, a, b)
			}
			for _, prepared := range []bool{false, true} {
				for requested := range fixture.RowClauses {
					for held := range fixture.RowClauses {
						t.Run(fixture.RowClauses[requested]+"/"+fixture.RowClauses[held]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
							tx, err := a.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							fixture.RowValues(t, fixture.RowQuery(p.Ctx, tx, "SELECT v FROM t WHERE id=1 "+fixture.RowClauses[held], prepared, backend.Recorder.RawBackend), 10)
							fixture.RowValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t WHERE id=2 "+fixture.RowClauses[requested], prepared, backend.Recorder.RawBackend), 20)
							fixture.RowValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t WHERE id=1", prepared, backend.Recorder.RawBackend), 10)
							query := "SELECT v FROM t WHERE id=1 " + fixture.RowClauses[requested]
							if referenceRowConflicts[requested][held] {
								if backend.PG {
									ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
									defer cancel()
									done := fixture.RowAsync(t, ctx, b, query, prepared, true, backend.Recorder.RawBackend)
									fixture.RowWait(t, ctx, bp, ap)
									if err := tx.Rollback(); err != nil {
										t.Fatal(err)
									}
									fixture.RowValues(t, fixture.LockResult(t, done), 10)
								} else {
									// Public deadline is a boundary witness; native engine companions prove
									// exact journal enqueue, without introducing a public inspection API.
									ctx, cancel := context.WithTimeout(p.Ctx, 30*time.Millisecond)
									got := fixture.RowQuery(ctx, b, query, prepared, backend.Recorder.RawBackend)
									cancel()
									if got.Rows != nil {
										got.Rows.Close()
										t.Fatal("conflict exposed rows")
									}
									if got.Stmt != nil {
										got.Stmt.Close()
									}
									if !errors.Is(got.Err, context.DeadlineExceeded) {
										t.Fatal(got.Err)
									}
									var state interface{ SQLState() string }
									if errors.As(got.Err, &state) {
										t.Fatal("caller deadline fabricated SQLSTATE", got.Err)
									}
									if err := tx.Rollback(); err != nil {
										t.Fatal(err)
									}
									fixture.RowValues(t, fixture.RowQuery(p.Ctx, b, query, prepared, backend.Recorder.RawBackend), 10)
								}
							} else {
								fixture.RowValues(t, fixture.RowQuery(p.Ctx, b, query, prepared, backend.Recorder.RawBackend), 10)
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
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
			ap, bp := 0, 0
			if backend.PG {
				ap, bp = fixture.RowPIDs(t, p.Ctx, a, b)
			}
			for _, prepared := range []bool{false, true} {
				for i, clause := range fixture.RowClauses {
					for _, deleting := range []bool{false, true} {
						t.Run(clause+map[bool]string{false: "/update", true: "/delete"}[deleting]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
							for _, partitioned := range []bool{false, true} {
								t.Run(map[bool]string{false: "whole_table", true: "distinct_row"}[partitioned], func(t *testing.T) {
									fixture.LockExec(t, p.Ctx, a, "DELETE FROM t", 0)
									where := ""
									count := int64(1)
									if partitioned {
										fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
										where = " WHERE id=1"
										count = 2
									} else {
										fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10)", 1)
									}
									tx, err := a.BeginTx(p.Ctx, nil)
									if err != nil {
										t.Fatal(err)
									}
									defer tx.Rollback()
									fixture.RowValues(t, fixture.RowQuery(p.Ctx, tx, "SELECT v FROM t"+where+" "+clause, prepared, backend.Recorder.RawBackend), 10)
									if partitioned {
										fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=21 WHERE id=2", prepared, 1)
									}
									query := "UPDATE t SET v=11" + where
									if deleting {
										query = "DELETE FROM t" + where
									}
									if deleting || i != 0 {
										if backend.PG {
											ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
											defer cancel()
											done := fixture.RowAsync(t, ctx, b, query, prepared, false)
											fixture.RowWait(t, ctx, bp, ap)
											if err := tx.Rollback(); err != nil {
												t.Fatal(err)
											}
											got := fixture.LockResult(t, done)
											if got.Err != nil {
												t.Fatal(got.Err)
											}
											fixture.LockAffected(t, got.Result, 1)
										} else {
											ctx, cancel := context.WithTimeout(p.Ctx, 30*time.Millisecond)
											outcome := fixture.RowExecute(ctx, b, query, prepared)
											cancel()
											if !errors.Is(outcome.Err, context.DeadlineExceeded) {
												t.Fatal(outcome.Err)
											}
											if outcome.Result != nil {
												t.Fatal("failed DML returned result", outcome.Result)
											}
											var state interface{ SQLState() string }
											if errors.As(outcome.Err, &state) {
												t.Fatal("caller deadline fabricated SQLSTATE", outcome.Err)
											}
											if err := tx.Rollback(); err != nil {
												t.Fatal(err)
											}
											fixture.RowAffected(t, p.Ctx, b, query, prepared, 1)
										}
									} else {
										fixture.RowAffected(t, p.Ctx, b, query, prepared, 1)
										if err := tx.Rollback(); err != nil {
											t.Fatal(err)
										}
										if partitioned {
											fixture.RowAffected(t, p.Ctx, b, query, prepared, 1)
										}
									}
									if partitioned {
										fixture.RowValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t WHERE id=2", prepared, backend.Recorder.RawBackend), 21)
									}
									if deleting {
										fixture.RowValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t"+where, prepared, backend.Recorder.RawBackend))
										if partitioned {
											fixture.LockExec(t, p.Ctx, b, "DELETE FROM t", 1)
										}
									} else {
										fixture.RowValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t"+where, prepared, backend.Recorder.RawBackend), 11)
										fixture.LockExec(t, p.Ctx, b, "DELETE FROM t", count)
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
