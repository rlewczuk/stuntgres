package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockViewProvenance(t *testing.T) {
	referenceViewReferenceProvenance(t)
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			body, query string
			joined      bool
		}{
			{"SELECT a.id,a.v FROM t a JOIN t b USING(id)", "SELECT v FROM joined WHERE id=1 FOR SHARE", false},
			{"SELECT a.id,a.v FROM t a NATURAL JOIN t b", "SELECT v FROM joined WHERE id=1 FOR SHARE", false},
			{"SELECT a.id,a.v FROM t a JOIN t b ON a.id=b.id", "SELECT v FROM joined WHERE id=1 FOR SHARE", false},
			{"SELECT t.id,t.v FROM t CROSS JOIN constant_view", "SELECT v FROM joined WHERE id=1 FOR SHARE", false},
			{"SELECT t.v FROM t JOIN u ON t.id=u.id", "SELECT v FROM joined FOR SHARE", true},
		} {
			t.Run("original/"+tc.body+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceViewFixture(t)
				for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE u(id INT)", 0), fixture.Exec("INSERT INTO u VALUES(1),(1),(4)", 3), fixture.Exec("CREATE VIEW constant_view AS SELECT num_nulls(NULL) AS n", 0), fixture.Exec("CREATE VIEW joined AS "+tc.body, 0)} {
					p.Step(t, step)
				}
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
						holder, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { holder.Rollback() })
						want := [][]any{{int64(10)}}
						if tc.joined {
							want = append(want, []any{int64(10)})
						}
						step := fixture.Select(tc.query, []string{"v"}, []string{"INT4"}, want)
						step.Prepared, step.Bag = prepared, true
						fixture.RunStep(t, p.Ctx, holder, backend.Recorder, step)
						fixture.RowAffected(t, p.Ctx, b, "UPDATE u SET id=5 WHERE id=4", prepared, 1)
						fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=2", prepared, 1)
						query, count := "DELETE FROM t WHERE id=1", int64(1)
						if tc.joined {
							if !backend.PG {
								fixture.ViewNativeBlocked(t, p.Ctx, b, query, prepared)
							} else {
								ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
								fixture.LockExec(t, p.Ctx, b, "SET lock_timeout='100ms'", 0)
								await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, b, query, prepared) })
								fixture.RowWait(t, p.Ctx, bp, ap)
								out := await()
								if out.Result != nil {
									t.Fatal("failed provenance DELETE returned result")
								}
								referenceRowTimeoutDiagnostic(t, out.Err, true, "55P03", "canceling statement due to lock timeout", "deleting")
								fixture.LockExec(t, p.Ctx, b, "RESET lock_timeout", 0)
							}
							query, count = "DELETE FROM u WHERE id=1", 2
						}
						if !backend.PG {
							fixture.ViewNativeBlocked(t, p.Ctx, b, query, prepared)
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
							fixture.RowAffected(t, p.Ctx, b, query, prepared, count)
						} else {
							ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
							await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, b, query, prepared) })
							fixture.RowWait(t, p.Ctx, bp, ap)
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
							out := await()
							if out.Err != nil {
								t.Fatal(out.Err)
							}
							fixture.LockAffected(t, out.Result, count)
						}
					})
				}
			})
		}
	}
}

func TestReferenceRowLockViewZeroWidthProvenance(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT)", 0), fixture.Exec("INSERT INTO t VALUES(1),(2)", 2), fixture.Exec("CREATE VIEW zero_width AS SELECT FROM t WHERE id=1", 0)} {
				p.Step(t, step)
			}
			step := fixture.Select("SELECT FROM zero_width FOR KEY SHARE", []string{}, []string{}, [][]any{{}})
			step.Prepared = prepared
			p.Step(t, step)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					holder, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					fixture.RunStep(t, p.Ctx, holder, backend.Recorder, step)
					if !backend.PG {
						fixture.ViewNativeBlocked(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared)
						if err := holder.Rollback(); err != nil {
							t.Fatal(err)
						}
						fixture.RowAffected(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared, 1)
					} else {
						ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
						await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							return fixture.RowExecute(ctx, b, "DELETE FROM t WHERE id=1", prepared)
						})
						fixture.RowWait(t, p.Ctx, bp, ap)
						if err := holder.Rollback(); err != nil {
							t.Fatal(err)
						}
						out := await()
						if out.Err != nil {
							t.Fatal(out.Err)
						}
						fixture.LockAffected(t, out.Result, 1)
					}
				})
			}
		})
	}
}
