//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestReferenceQueryLimitLocks(t *testing.T) {
	t.Run("tail and zero frontiers", func(t *testing.T) {
		for _, prepared := range []bool{false, true} {
			for _, source := range []string{"t", "marked", "t JOIN u USING(id)"} {
				for _, zero := range []bool{false, true} {
					t.Run(source+map[bool]string{false: "/one", true: "/zero"}[zero]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						p := newReferencePair(t)
						for backend, db := range []*sql.DB{p.postgres, p.memory} {
							t.Run([]string{"postgres", "memory"}[backend], func(t *testing.T) {
								ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
								defer cancel()
								a, b := referenceLockConnections(t, ctx, db)
								for _, step := range []referenceStep{referenceExec("CREATE TABLE t(id INT,v INT)", 0), referenceExec("INSERT INTO t VALUES(1,10),(2,20),(3,30),(4,40)", 4), referenceExec("CREATE TABLE u(id INT)", 0), referenceExec("INSERT INTO u VALUES(1),(2),(3),(4)", 4), referenceExec("CREATE VIEW marked AS SELECT id,v FROM t FOR UPDATE", 0)} {
									referenceLockExec(t, ctx, a, step.query, step.affected)
								}
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
								referenceRowValues(t, referenceRowQuery(ctx, holder, query, prepared), want...)
								if source == "marked" && !zero {
									if backend == 0 {
										ap, bp := referenceRowPIDs(t, ctx, a, b)
										done := referenceRowAsync(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=4", prepared, false)
										referenceRowWait(t, ctx, bp, ap)
										if err := holder.Rollback(); err != nil {
											t.Fatal(err)
										}
										out := referenceLockResult(t, done)
										if out.err != nil {
											t.Fatal(out.err)
										}
										referenceLockAffected(t, out.result, 1)
									} else {
										// Native exact holds are asserted internally after production.
										if err := holder.Rollback(); err != nil {
											t.Fatal(err)
										}
										referenceRowAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=4", prepared, 1)
									}
									referenceRowAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=3", prepared, 1)
								} else {
									// Success, not a guessed timeout, proves the free frontier.
									referenceRowAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=3", prepared, 1)
									referenceRowAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=4", prepared, 1)
									if zero {
										referenceRowAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=1", prepared, 1)
										referenceRowAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=2", prepared, 1)
									}
								}
								if source == "t JOIN u USING(id)" {
									referenceRowAffected(t, ctx, b, "DELETE FROM u WHERE id=4", prepared, 1)
								}
							})
						}
					})
				}
			}
		}
	})
	t.Run("postgres observed skipped and returned holds", func(t *testing.T) {
		for _, id := range []string{"1", "2"} {
			t.Run(id, func(t *testing.T) {
				p := newReferencePair(t)
				a, b := referenceLockConnections(t, p.ctx, p.postgres)
				referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
				referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20),(3,30),(4,40)", 4)
				ap, bp := referenceRowPIDs(t, p.ctx, a, b)
				holder, err := a.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				referenceRowValues(t, referenceRowQuery(p.ctx, holder, "SELECT v FROM t ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE", false), 20)
				ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
				defer cancel()
				done := referenceRowAsync(t, ctx, b, "UPDATE t SET v=99 WHERE id="+id, false, false)
				referenceRowWait(t, ctx, bp, ap)
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				out := referenceLockResult(t, done)
				if out.err != nil {
					t.Fatal(out.err)
				}
				referenceLockAffected(t, out.result, 1)
			})
		}
	})
	t.Run("postgres surviving refill", func(t *testing.T) {
		for _, change := range []string{"DELETE FROM t WHERE id=2", "UPDATE t SET v=0 WHERE id=2"} {
			for _, source := range []string{"t", "marked", "t JOIN u USING(id)"} {
				for _, prepared := range []bool{false, true} {
					t.Run(change+source+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						p := newReferencePair(t)
						a, b := referenceLockConnections(t, p.ctx, p.postgres)
						for _, step := range []referenceStep{referenceExec("CREATE TABLE t(id INT,v INT)", 0), referenceExec("INSERT INTO t VALUES(1,10),(2,20),(3,30),(4,40)", 4), referenceExec("CREATE TABLE u(id INT)", 0), referenceExec("INSERT INTO u VALUES(1),(2),(3),(4)", 4), referenceExec("CREATE VIEW marked AS SELECT id,v FROM t FOR UPDATE", 0)} {
							referenceLockExec(t, p.ctx, a, step.query, step.affected)
						}
						ap, bp := referenceRowPIDs(t, p.ctx, a, b)
						writer, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer writer.Rollback()
						referenceRowValues(t, referenceRowQuery(p.ctx, writer, "SELECT v FROM t WHERE id=2 FOR UPDATE", false), 20)
						referenceLockExec(t, p.ctx, writer, change, 1)
						reader, err := b.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer reader.Rollback()
						ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
						defer cancel()
						done := referenceRowAsync(t, ctx, reader, "SELECT v FROM "+source+" WHERE v>0 ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE", prepared, true)
						referenceRowWait(t, ctx, bp, ap)
						if err := writer.Commit(); err != nil {
							t.Fatal(err)
						}
						referenceRowValues(t, referenceLockResult(t, done), 30)
						if source == "marked" {
							// Inner marks precede the outer sort, so surviving id4 is held.
							tail := referenceRowAsync(t, ctx, a, "UPDATE t SET v=41 WHERE id=4", false, false)
							referenceRowWait(t, ctx, ap, bp)
							if err := reader.Rollback(); err != nil {
								t.Fatal(err)
							}
							out := referenceLockResult(t, tail)
							if out.err != nil {
								t.Fatal(out.err)
							}
							referenceLockAffected(t, out.result, 1)
						} else {
							referenceRowAffected(t, ctx, a, "UPDATE t SET v=41 WHERE id=4", false, 1)
						}
					})
				}
			}
		}
	})
}
