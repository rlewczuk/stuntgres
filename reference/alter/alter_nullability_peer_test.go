package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// referenceNullabilityCursor preserves the already queued query's actual cursor.
type referenceNullabilityCursor struct {
	fixture.Executor
	rows *sql.Rows
}

func (c referenceNullabilityCursor) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return c.rows, nil
}

func TestReferenceAlterNullabilityReviewCombinedPeer(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
					holder, waiter := 0, 0
					var oid int64
					if backend.PG {
						holder = fixture.LockPID(t, p.Ctx, a)
						waiter = fixture.LockPID(t, p.Ctx, b)
						oid = fixture.LockOID(t, p.Ctx, "public.t")
					}
					tx, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("ALTER TABLE t DROP b, ALTER a SET NOT NULL", 0))
					fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(8)", 1))
					q := "SELECT * FROM t ORDER BY a"
					ctx, cancelQuery := context.WithCancel(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder))
					defer cancelQuery()
					type observation struct {
						rows *sql.Rows
						err  error
					}
					done := make(chan observation, 1)
					joined := false
					if !backend.PG {
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
						fixture.LockWaitBehind(t, p.Ctx, waiter, holder, oid, "AccessShareLock")
						var pending string
						if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT query FROM pg_stat_activity WHERE pid=$1", waiter).Scan(&pending); err != nil || pending != q {
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
					if !backend.PG {
						observed.rows, observed.err = b.QueryContext(ctx, q)
					} else {
						select {
						case observed = <-done:
							joined = true
						case <-p.Ctx.Done():
							t.Fatal(p.Ctx.Err())
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
					fixture.TableDDLColumnTypes(t, backend.PG, columns, names, types, nullable)
					cursor := referenceNullabilityCursor{Executor: b, rows: observed.rows}
					fixture.RunStep(t, ctx, cursor, backend.Recorder, fixture.Select(q, names, types, want))
				})
			}
		})
	}
}
