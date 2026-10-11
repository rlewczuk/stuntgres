package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceQueryLimitLocks(t *testing.T) {
	t.Run("tail and zero frontiers", func(t *testing.T) {
		for _, prepared := range []bool{false, true} {
			for _, source := range []string{"t", "marked", "t JOIN u USING(id)"} {
				for _, zero := range []bool{false, true} {
					t.Run(source+map[bool]string{false: "/one", true: "/zero"}[zero]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						p := fixture.NewScenario(t)
						for _, backend := range p.Backends() {
							t.Run(backend.Name, func(t *testing.T) {
								db := backend.DB
								ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
								defer cancel()
								a, b := fixture.LockConnections(t, ctx, db)
								for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30),(4,40)", 4), fixture.Exec("CREATE TABLE u(id INT)", 0), fixture.Exec("INSERT INTO u VALUES(1),(2),(3),(4)", 4), fixture.Exec("CREATE VIEW marked AS SELECT id,v FROM t FOR UPDATE", 0)} {
									fixture.LockExec(t, ctx, a, step.Query, step.Affected)
								}
								fixture.LockExec(t, ctx, a, "CREATE VIEW v AS SELECT id,v FROM t WHERE id<3", 0)
								fixture.LockExec(t, ctx, a, "CREATE VIEW w AS SELECT id,v+1 AS val FROM v", 0)
								holder, err := a.BeginTx(ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer holder.Rollback()
								query := "SELECT v FROM " + source + " ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE"
								want := []int64{20}
								if zero {
									query = "SELECT v FROM " + source + " ORDER BY id LIMIT 0 FOR UPDATE"
									want = nil
								}
								fixture.LimitLockValues(t, fixture.RowQuery(ctx, holder, query, prepared), backend.PG, want...)
								if source == "marked" && !zero {
									if backend.PG {
										ap, bp := fixture.RowPIDs(t, ctx, a, b)
										done := fixture.RowAsync(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=4", prepared, false)
										fixture.RowWait(t, ctx, bp, ap)
										if err := holder.Rollback(); err != nil {
											t.Fatal(err)
										}
										out := fixture.LockResult(t, done)
										if out.Err != nil {
											t.Fatal(out.Err)
										}
										fixture.LockAffected(t, out.Result, 1)
									} else {
										// Native exact holds are asserted internally after production.
										if err := holder.Rollback(); err != nil {
											t.Fatal(err)
										}
										fixture.RowAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=4", prepared, 1)
									}
									fixture.RowAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=3", prepared, 1)
								} else {
									// Success, not a guessed timeout, proves the free frontier.
									fixture.RowAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=3", prepared, 1)
									fixture.RowAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=4", prepared, 1)
									if zero {
										fixture.RowAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=1", prepared, 1)
										fixture.RowAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=2", prepared, 1)
									}
								}
								first := 3
								if zero {
									first = 1
								}
								for id := first; id <= 4; id++ {
									result, err := b.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=$1", id)
									if err != nil {
										t.Fatal("unconsumed row was locked", id, err)
									}
									fixture.LockAffected(t, result, 1)
								}
								if source == "t JOIN u USING(id)" {
									fixture.RowAffected(t, ctx, b, "DELETE FROM u WHERE id=4", false, 1)
									fixture.RowAffected(t, ctx, b, "INSERT INTO u VALUES(4)", false, 1)
									fixture.RowAffected(t, ctx, b, "DELETE FROM u WHERE id=4", true, 1)
								}
							})
						}
					})
				}
			}
		}
	})
}

func TestReferenceQueryLimitSkippedAndReturnedLocks(t *testing.T) {
	if !*fixture.Mode {
		return // Explicit PG-only lock-observation complement.
	}
	t.Run("postgres observed skipped and returned holds", func(t *testing.T) {
		for _, id := range []string{"1", "2"} {
			t.Run(id, func(t *testing.T) {
				p := fixture.NewScenario(t)
				a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10),(2,20),(3,30),(4,40)", 4)
				ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
				holder, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				fixture.RowValues(t, fixture.RowQuery(p.Ctx, holder, "SELECT v FROM t ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE", false), 20)
				ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
				defer cancel()
				done := fixture.RowAsync(t, ctx, b, "UPDATE t SET v=99 WHERE id="+id, false, false)
				fixture.RowWait(t, ctx, bp, ap)
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				out := fixture.LockResult(t, done)
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				fixture.LockAffected(t, out.Result, 1)
			})
		}
	})
}

func TestReferenceQueryLimitSurvivingRefill(t *testing.T) {
	if !*fixture.Mode {
		return // Explicit PG-only recheck/refill complement.
	}
	t.Run("postgres surviving refill", func(t *testing.T) {
		for _, change := range []string{"DELETE FROM t WHERE id=2", "UPDATE t SET v=0 WHERE id=2"} {
			for _, source := range []string{"t", "marked", "t JOIN u USING(id)"} {
				for _, prepared := range []bool{false, true} {
					t.Run(change+source+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						p := fixture.NewScenario(t)
						a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
						for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30),(4,40)", 4), fixture.Exec("CREATE TABLE u(id INT)", 0), fixture.Exec("INSERT INTO u VALUES(1),(2),(3),(4)", 4), fixture.Exec("CREATE VIEW marked AS SELECT id,v FROM t FOR UPDATE", 0)} {
							fixture.LockExec(t, p.Ctx, a, step.Query, step.Affected)
						}
						ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
						writer, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer writer.Rollback()
						fixture.RowValues(t, fixture.RowQuery(p.Ctx, writer, "SELECT v FROM t WHERE id=2 FOR UPDATE", false), 20)
						fixture.LockExec(t, p.Ctx, writer, change, 1)
						reader, err := b.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer reader.Rollback()
						ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
						defer cancel()
						done := fixture.RowAsync(t, ctx, reader, "SELECT v FROM "+source+" WHERE v>0 ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE", prepared, true)
						fixture.RowWait(t, ctx, bp, ap)
						if err := writer.Commit(); err != nil {
							t.Fatal(err)
						}
						fixture.RowValues(t, fixture.LockResult(t, done), 30)
						if source == "marked" {
							// Inner marks precede the outer sort, so surviving id4 is held.
							tail := fixture.RowAsync(t, ctx, a, "UPDATE t SET v=41 WHERE id=4", false, false)
							fixture.RowWait(t, ctx, ap, bp)
							if err := reader.Rollback(); err != nil {
								t.Fatal(err)
							}
							out := fixture.LockResult(t, tail)
							if out.Err != nil {
								t.Fatal(out.Err)
							}
							fixture.LockAffected(t, out.Result, 1)
						} else {
							fixture.RowAffected(t, ctx, a, "UPDATE t SET v=41 WHERE id=4", false, 1)
						}
					})
				}
			}
		}
	})
}
