package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceQueryOuterJoinsContention(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, placement := range []string{"sorted_nullable", "nested_sorted_nullable", "nested_sorted_preserved"} {
			for _, action := range []string{"update_commit", "key_commit", "delete_commit", "update_rollback", "cancel"} {
				t.Run(placement+"/"+action+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
							for _, step := range []fixture.Step{
								fixture.Exec("CREATE TABLE t(id INTEGER,v INTEGER)", 0),
								fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
								fixture.Exec("CREATE TABLE sibling(id INTEGER)", 0),
								fixture.Exec("INSERT INTO sibling VALUES(1)", 1),
								fixture.Exec("CREATE SEQUENCE s", 0),
								fixture.Exec("CREATE VIEW marked AS SELECT id,v+1 AS val,nextval('s') AS n FROM t ORDER BY n FOR UPDATE", 0),
								fixture.Exec("CREATE VIEW nested AS SELECT * FROM marked", 0),
							} {
								fixture.RunStep(t, p.Ctx, a, backend.Recorder, step)
							}
							view := "marked"
							if placement != "sorted_nullable" {
								view = "nested"
							}
							query := "SELECT a.id AS aid,m.id AS mid,m.val FROM sibling a LEFT JOIN " + view + " m ON a.id=m.id"
							if placement == "nested_sorted_preserved" {
								query = "SELECT a.id AS aid,m.id AS mid,m.val FROM " + view + " m LEFT JOIN sibling a ON a.id=m.id"
							}
							ap, bp := 0, 0
							if backend.PG {
								ap, bp = fixture.RowPIDs(t, p.Ctx, a, b)
							}
							holder, err := a.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer holder.Rollback()
							reader, err := b.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer reader.Rollback()
							// Competing write precedes query capture; no after-drain probe
							// is being mistaken for a version-recheck witness.
							change := "UPDATE t SET v=20 WHERE id=1"
							if action == "key_commit" {
								change = "UPDATE t SET id=2,v=20 WHERE id=1"
							} else if action == "delete_commit" {
								change = "DELETE FROM t WHERE id=1"
							}
							fixture.LockExec(t, p.Ctx, holder, change, 1)
							queryCtx, cancel := context.WithCancel(p.Ctx)
							defer cancel()
							done := fixture.RowAsync(t, queryCtx, reader, query, prepared, true, backend.Recorder.RawBackend)
							waitCtx, stopWait := context.WithTimeout(p.Ctx, 5*time.Second)
							defer stopWait()
							if backend.PG {
								fixture.RowWait(t, waitCtx, bp, ap)
							} else {
								// Positive pre-lock ORDER recipe evaluation captures the
								// original candidate; not a sleep-only wait assumption.
								fixture.LimitSequenceCapture(t, waitCtx, holder)
							}
							select {
							case outcome := <-done:
								outerJoinsContentionClose(outcome)
								t.Fatal("outer query completed before competing writer released", outcome.Err)
							default:
							}
							if action == "cancel" {
								cancel()
								outcome := fixture.LockResult(t, done)
								outerJoinsContentionClose(outcome)
								if outcome.Err == nil || outcome.Rows != nil {
									t.Fatal("cancelled waiting query returned a cursor", outcome.Err)
								}
								if !backend.PG && !errors.Is(outcome.Err, context.Canceled) {
									t.Fatal("native cancellation did not preserve context cause", outcome.Err)
								}
								if err := reader.Rollback(); err != nil {
									// pq's direct QueryContext cancellation discards its
									// physical connection. This observed driver boundary is
									// not a successful rollback or a reusable-session claim.
									if !backend.PG || prepared || !errors.Is(err, driver.ErrBadConn) {
										t.Fatal("cancelled reader rollback", err)
									}
								}
								if backend.PG && !prepared {
									b.Close()
									b, err = backend.DB.Conn(p.Ctx)
									if err != nil {
										t.Fatal(err)
									}
									defer b.Close()
								}
								if err := holder.Rollback(); err != nil {
									t.Fatal(err)
								}
								// Joining precedes rollback/reuse. Neither tuple nor relation
								// resources may survive the cancelled statement transaction.
								outerJoinsContentionRelease(t, p, a, b, backend, prepared)
								return
							}
							if action == "update_rollback" {
								err = holder.Rollback()
							} else {
								err = holder.Commit()
							}
							if err != nil {
								t.Fatal(err)
							}
							want := [][]any{{int64(1), int64(1), int64(21)}}
							if action == "update_rollback" {
								want = [][]any{{int64(1), int64(1), int64(11)}}
							} else if action == "key_commit" || action == "delete_commit" {
								want = [][]any{{int64(1), nil, nil}}
								if placement == "nested_sorted_preserved" {
									want = nil
									if action == "key_commit" {
										want = [][]any{{nil, int64(2), int64(21)}}
									}
								}
							}
							outerJoinsContentionRows(t, fixture.LockResult(t, done), want)
							// The unrelated sibling must not acquire an outward mark.
							fixture.RowAffected(t, p.Ctx, a, "UPDATE sibling SET id=id", prepared, 1)
							var ownership <-chan fixture.RelationOutcome
							if action != "delete_commit" {
								probe, err := backend.DB.Conn(p.Ctx)
								if err != nil {
									t.Fatal(err)
								}
								defer probe.Close()
								if backend.PG {
									readerPID, probePID := fixture.RowPIDs(t, p.Ctx, b, probe)
									ownership = fixture.RowAsync(t, p.Ctx, probe, "UPDATE t SET v=v", prepared, false)
									fixture.RowWait(t, waitCtx, probePID, readerPID)
								} else {
									bounded, stop := context.WithTimeout(p.Ctx, 30*time.Millisecond)
									outcome := fixture.RowExecute(bounded, probe, "UPDATE t SET v=v", prepared)
									stop()
									if !errors.Is(outcome.Err, context.DeadlineExceeded) || outcome.Result != nil {
										t.Fatal("real local marked input did not retain ownership", outcome.Err)
									}
								}
							}
							if err := reader.Rollback(); err != nil {
								t.Fatal(err)
							}
							if ownership != nil {
								outcome := fixture.LockResult(t, ownership)
								if outcome.Err != nil {
									t.Fatal("ownership probe after reader rollback", outcome.Err)
								}
								fixture.LockAffected(t, outcome.Result, 1)
							}
							outerJoinsContentionRelease(t, p, a, b, backend, prepared)
						})
					}
				})
			}
		}
	}
}

func outerJoinsContentionClose(outcome fixture.RelationOutcome) {
	if outcome.Rows != nil {
		outcome.Rows.Close()
	}
	if outcome.Stmt != nil {
		outcome.Stmt.Close()
	}
}

func outerJoinsContentionRows(t *testing.T, outcome fixture.RelationOutcome, want [][]any) {
	t.Helper()
	defer outerJoinsContentionClose(outcome)
	if outcome.Err != nil || outcome.Rows == nil {
		t.Fatal(outcome.Err)
	}
	columns, err := outcome.Rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"aid", "mid", "val"}) {
		t.Fatal(columns, err)
	}
	types, err := outcome.Rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		t.Fatal(types, err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "INT4" {
			t.Fatal(typ.DatabaseTypeName())
		}
	}
	var got [][]any
	for outcome.Rows.Next() {
		var a, m, v any
		if err := outcome.Rows.Scan(&a, &m, &v); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{fixture.Normalize(a), fixture.Normalize(m), fixture.Normalize(v)})
	}
	if err := outcome.Rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("write-first outer result", got, "want", want)
	}
	if err := outcome.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	if outcome.Stmt != nil {
		if err := outcome.Stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func outerJoinsContentionRelease(t *testing.T, p *fixture.Pair, a, b *sql.Conn, backend fixture.Backend, prepared bool) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SELECT id AS alive FROM sibling", []string{"alive"}, []string{"INT4"}, [][]any{{int64(1)}}))
	tx, err := a.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE sibling IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	// An actual row write, not only relation-lock availability, proves release.
	outcome := fixture.RowExecute(p.Ctx, tx, "UPDATE t SET v=v", prepared)
	if outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
