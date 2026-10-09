package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestReferenceQueryOuterJoinsContention(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, placement := range []string{"sorted_nullable", "nested_sorted_nullable", "nested_sorted_preserved"} {
			for _, action := range []string{"update_commit", "key_commit", "delete_commit", "update_rollback", "cancel"} {
				t.Run(placement+"/"+action+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							a, b := referenceLockConnections(t, p.ctx, backend.db)
							for _, step := range []referenceStep{
								referenceExec("CREATE TABLE t(id INTEGER,v INTEGER)", 0),
								referenceExec("INSERT INTO t VALUES(1,10)", 1),
								referenceExec("CREATE TABLE sibling(id INTEGER)", 0),
								referenceExec("INSERT INTO sibling VALUES(1)", 1),
								referenceExec("CREATE SEQUENCE s", 0),
								referenceExec("CREATE VIEW marked AS SELECT id,v+1 AS val,nextval('s') AS n FROM t ORDER BY n FOR UPDATE", 0),
								referenceExec("CREATE VIEW nested AS SELECT * FROM marked", 0),
							} {
								referenceRunStep(t, p.ctx, a, backend.recorder, step)
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
							if backend.pg {
								ap, bp = referenceRowPIDs(t, p.ctx, a, b)
							}
							holder, err := a.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer holder.Rollback()
							reader, err := b.BeginTx(p.ctx, nil)
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
							referenceLockExec(t, p.ctx, holder, change, 1)
							queryCtx, cancel := context.WithCancel(p.ctx)
							defer cancel()
							done := referenceRowAsync(t, queryCtx, reader, query, prepared, true, backend.recorder.rawBackend)
							waitCtx, stopWait := context.WithTimeout(p.ctx, 5*time.Second)
							defer stopWait()
							if backend.pg {
								referenceRowWait(t, waitCtx, bp, ap)
							} else {
								// Positive pre-lock ORDER recipe evaluation captures the
								// original candidate; not a sleep-only wait assumption.
								referenceLimitSequenceCapture(t, waitCtx, holder)
							}
							select {
							case outcome := <-done:
								outerJoinsContentionClose(outcome)
								t.Fatal("outer query completed before competing writer released", outcome.err)
							default:
							}
							if action == "cancel" {
								cancel()
								outcome := referenceLockResult(t, done)
								outerJoinsContentionClose(outcome)
								if outcome.err == nil || outcome.rows != nil {
									t.Fatal("cancelled waiting query returned a cursor", outcome.err)
								}
								if !backend.pg && !errors.Is(outcome.err, context.Canceled) {
									t.Fatal("native cancellation did not preserve context cause", outcome.err)
								}
								if err := reader.Rollback(); err != nil {
									// pq's direct QueryContext cancellation discards its
									// physical connection. This observed driver boundary is
									// not a successful rollback or a reusable-session claim.
									if !backend.pg || prepared || !errors.Is(err, driver.ErrBadConn) {
										t.Fatal("cancelled reader rollback", err)
									}
								}
								if backend.pg && !prepared {
									b.Close()
									b, err = backend.db.Conn(p.ctx)
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
							outerJoinsContentionRows(t, referenceLockResult(t, done), want)
							// The unrelated sibling must not acquire an outward mark.
							referenceRowAffected(t, p.ctx, a, "UPDATE sibling SET id=id", prepared, 1)
							var ownership <-chan relationOutcome
							if action != "delete_commit" {
								probe, err := backend.db.Conn(p.ctx)
								if err != nil {
									t.Fatal(err)
								}
								defer probe.Close()
								if backend.pg {
									readerPID, probePID := referenceRowPIDs(t, p.ctx, b, probe)
									ownership = referenceRowAsync(t, p.ctx, probe, "UPDATE t SET v=v", prepared, false)
									referenceRowWait(t, waitCtx, probePID, readerPID)
								} else {
									bounded, stop := context.WithTimeout(p.ctx, 30*time.Millisecond)
									outcome := referenceRowExecute(bounded, probe, "UPDATE t SET v=v", prepared)
									stop()
									if !errors.Is(outcome.err, context.DeadlineExceeded) || outcome.result != nil {
										t.Fatal("real local marked input did not retain ownership", outcome.err)
									}
								}
							}
							if err := reader.Rollback(); err != nil {
								t.Fatal(err)
							}
							if ownership != nil {
								outcome := referenceLockResult(t, ownership)
								if outcome.err != nil {
									t.Fatal("ownership probe after reader rollback", outcome.err)
								}
								referenceLockAffected(t, outcome.result, 1)
							}
							outerJoinsContentionRelease(t, p, a, b, backend, prepared)
						})
					}
				})
			}
		}
	}
}

func outerJoinsContentionClose(outcome relationOutcome) {
	if outcome.rows != nil {
		outcome.rows.Close()
	}
	if outcome.stmt != nil {
		outcome.stmt.Close()
	}
}

func outerJoinsContentionRows(t *testing.T, outcome relationOutcome, want [][]any) {
	t.Helper()
	defer outerJoinsContentionClose(outcome)
	if outcome.err != nil || outcome.rows == nil {
		t.Fatal(outcome.err)
	}
	columns, err := outcome.rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"aid", "mid", "val"}) {
		t.Fatal(columns, err)
	}
	types, err := outcome.rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		t.Fatal(types, err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "INT4" {
			t.Fatal(typ.DatabaseTypeName())
		}
	}
	var got [][]any
	for outcome.rows.Next() {
		var a, m, v any
		if err := outcome.rows.Scan(&a, &m, &v); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{referenceNormalize(a), referenceNormalize(m), referenceNormalize(v)})
	}
	if err := outcome.rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("write-first outer result", got, "want", want)
	}
	if err := outcome.rows.Close(); err != nil {
		t.Fatal(err)
	}
	if outcome.stmt != nil {
		if err := outcome.stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func outerJoinsContentionRelease(t *testing.T, p *referencePair, a, b *sql.Conn, backend referenceBackend, prepared bool) {
	t.Helper()
	referenceRunStep(t, p.ctx, b, backend.recorder, referenceSelect("SELECT id AS alive FROM sibling", []string{"alive"}, []string{"INT4"}, [][]any{{int64(1)}}))
	tx, err := a.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	referenceLockExec(t, p.ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	referenceLockExec(t, p.ctx, tx, "LOCK TABLE sibling IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	// An actual row write, not only relation-lock availability, proves release.
	outcome := referenceRowExecute(p.ctx, tx, "UPDATE t SET v=v", prepared)
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
