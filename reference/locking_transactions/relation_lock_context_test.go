package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRelationLockContextBoundaries(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
						run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, backend.Recorder, s) }
						run(a, fixture.Exec("CREATE TABLE t(v INT)", 0))
						cached, err := b.PrepareContext(p.Ctx, "SELECT v FROM t ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						defer cached.Close()
						tx, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						run(tx, fixture.Step{Query: "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", Prepared: prepared, Affected: 0})
						run(tx, fixture.Step{Query: "INSERT INTO t VALUES(1)", Prepared: prepared, Affected: 1})
						if !backend.PG {
							for _, query := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
								bounded, cancel := context.WithTimeout(p.Ctx, 30*time.Millisecond)
								o := fixture.RowExecute(bounded, b, query, prepared)
								cancel()
								if o.Result != nil || !errors.Is(o.Err, context.DeadlineExceeded) {
									t.Fatal(query, o.Result, o.Err)
								}
								var state interface{ SQLState() string }
								if errors.As(o.Err, &state) {
									t.Fatal("caller deadline acquired SQLSTATE", o.Err)
								}
							}
							bounded, cancel := context.WithTimeout(p.Ctx, 30*time.Millisecond)
							rows, err := cached.QueryContext(bounded)
							cancel()
							if rows != nil {
								rows.Close()
								t.Fatal("canceled cached query returned a cursor")
							}
							if !errors.Is(err, context.DeadlineExceeded) {
								t.Fatal(err)
							}
							var state interface{ SQLState() string }
							if errors.As(err, &state) {
								t.Fatal("cached caller deadline acquired SQLSTATE", err)
							}
						}
						var await func() fixture.RelationOutcome
						if backend.PG {
							bp, ap := fixture.LockPID(t, p.Ctx, b), fixture.LockPID(t, p.Ctx, a)
							await = fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
								rows, err := cached.QueryContext(ctx)
								return fixture.RelationOutcome{Rows: rows, Err: err}
							})
							fixture.LockWaitBehind(t, p.Ctx, bp, ap, fixture.LockOID(t, p.Ctx, "t"), "AccessShareLock")
						}
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						want := []int64{}
						if commit {
							want = []int64{1}
						}
						if await != nil {
							fixture.LimitLockValues(t, await(), true, want...)
						}
						rows, err := cached.QueryContext(p.Ctx)
						fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, backend.PG, want...)
						run(b, fixture.Step{Query: "INSERT INTO t VALUES(2)", Prepared: prepared, Affected: 1})
						if commit {
							want = []int64{1, 2}
						} else {
							want = []int64{2}
						}
						run(b, fixture.TransactionRows(false, "SELECT v FROM t ORDER BY v", want...))
					})
				}
			})
		}
	}
}
