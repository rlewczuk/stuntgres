package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

// referenceNullabilityCursor preserves the already queued query's actual cursor.
type referenceNullabilityCursor struct {
	referenceExecutor
	rows *sql.Rows
}

func (c referenceNullabilityCursor) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return c.rows, nil
}

func TestReferenceAlterNullabilityReviewCombinedPeer(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, backend.recorder, s) }
					run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(7,9)", 1))
					holder, waiter := 0, 0
					var oid int64
					if backend.pg {
						holder = referenceLockPID(t, p.ctx, a)
						waiter = referenceLockPID(t, p.ctx, b)
						oid = referenceLockOID(t, p.ctx, "public.t")
					}
					tx, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("ALTER TABLE t DROP b, ALTER a SET NOT NULL", 0))
					referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("INSERT INTO t VALUES(8)", 1))
					q := "SELECT * FROM t ORDER BY a"
					ctx, cancelQuery := context.WithCancel(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder))
					defer cancelQuery()
					type observation struct {
						rows *sql.Rows
						err  error
					}
					done := make(chan observation, 1)
					joined := false
					if !backend.pg {
						blocked, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
						rows, e := b.QueryContext(blocked, q)
						cancel()
						if rows != nil {
							rows.Close()
							t.Fatal("failed native deadline query exposed a cursor")
						}
						if !errors.Is(e, context.DeadlineExceeded) {
							t.Fatal("native peer was not blocked", e)
						}
					} else {
						go func() { rows, e := b.QueryContext(ctx, q); done <- observation{rows, e} }()
						defer func() {
							cancelQuery()
							tx.Rollback()
							if !joined {
								pending := <-done
								if pending.rows != nil {
									pending.rows.Close()
								}
							}
						}()
						referenceLockWaitBehind(t, p.ctx, waiter, holder, oid, "AccessShareLock")
						var pending string
						if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT query FROM pg_stat_activity WHERE pid=$1", waiter).Scan(&pending); err != nil || pending != q {
							t.Fatal("different queued query", pending, err)
						}
					}
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					var observed observation
					if !backend.pg {
						observed.rows, observed.err = b.QueryContext(ctx, q)
					} else {
						select {
						case observed = <-done:
							joined = true
						case <-p.ctx.Done():
							t.Fatal(p.ctx.Err())
						}
					}
					if observed.err != nil {
						t.Fatal(observed.err)
					}
					defer observed.rows.Close()
					columns, err := observed.rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					names, types, nullable := []string{"a"}, []string{"INT4"}, []bool{false}
					want := [][]any{{int64(7)}, {int64(8)}}
					if !commit {
						names, types, nullable = []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, true}
						want = [][]any{{int64(7), int64(9)}}
					}
					referenceTableDDLColumnTypes(t, backend.pg, columns, names, types, nullable)
					cursor := referenceNullabilityCursor{referenceExecutor: b, rows: observed.rows}
					referenceRunStep(t, ctx, cursor, backend.recorder, referenceSelect(q, names, types, want))
				})
			}
		})
	}
}
