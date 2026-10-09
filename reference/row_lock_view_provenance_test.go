package reference_test

import (
	"context"
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
				for _, step := range []referenceStep{referenceExec("CREATE TABLE u(id INT)", 0), referenceExec("INSERT INTO u VALUES(1),(1),(4)", 3), referenceExec("CREATE VIEW constant_view AS SELECT num_nulls(NULL) AS n", 0), referenceExec("CREATE VIEW joined AS "+tc.body, 0)} {
					p.step(t, step)
				}
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						a, b := referenceLockConnections(t, p.ctx, backend.db)
						holder, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { holder.Rollback() })
						want := [][]any{{int64(10)}}
						if tc.joined {
							want = append(want, []any{int64(10)})
						}
						step := referenceSelect(tc.query, []string{"v"}, []string{"INT4"}, want)
						step.prepared, step.bag = prepared, true
						referenceRunStep(t, p.ctx, holder, backend.recorder, step)
						referenceRowAffected(t, p.ctx, b, "UPDATE u SET id=5 WHERE id=4", prepared, 1)
						referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=2", prepared, 1)
						query, count := "DELETE FROM t WHERE id=1", int64(1)
						if tc.joined {
							if !backend.pg {
								referenceViewNativeBlocked(t, p.ctx, b, query, prepared)
							} else {
								ap, bp := referenceRowPIDs(t, p.ctx, a, b)
								referenceLockExec(t, p.ctx, b, "SET lock_timeout='100ms'", 0)
								await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome { return referenceRowExecute(ctx, b, query, prepared) })
								referenceRowWait(t, p.ctx, bp, ap)
								out := await()
								if out.result != nil {
									t.Fatal("failed provenance DELETE returned result")
								}
								referenceRowTimeoutDiagnostic(t, out.err, true, "55P03", "canceling statement due to lock timeout", "deleting")
								referenceLockExec(t, p.ctx, b, "RESET lock_timeout", 0)
							}
							query, count = "DELETE FROM u WHERE id=1", 2
						}
						if !backend.pg {
							referenceViewNativeBlocked(t, p.ctx, b, query, prepared)
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
							referenceRowAffected(t, p.ctx, b, query, prepared, count)
						} else {
							ap, bp := referenceRowPIDs(t, p.ctx, a, b)
							await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome { return referenceRowExecute(ctx, b, query, prepared) })
							referenceRowWait(t, p.ctx, bp, ap)
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
							out := await()
							if out.err != nil {
								t.Fatal(out.err)
							}
							referenceLockAffected(t, out.result, count)
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
			p := newReferenceScenario(t)
			for _, step := range []referenceStep{referenceExec("CREATE TABLE t(id INT)", 0), referenceExec("INSERT INTO t VALUES(1),(2)", 2), referenceExec("CREATE VIEW zero_width AS SELECT FROM t WHERE id=1", 0)} {
				p.step(t, step)
			}
			step := referenceSelect("SELECT FROM zero_width FOR KEY SHARE", []string{}, []string{}, [][]any{{}})
			step.prepared = prepared
			p.step(t, step)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					holder, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					referenceRunStep(t, p.ctx, holder, backend.recorder, step)
					if !backend.pg {
						referenceViewNativeBlocked(t, p.ctx, b, "DELETE FROM t WHERE id=1", prepared)
						if err := holder.Rollback(); err != nil {
							t.Fatal(err)
						}
						referenceRowAffected(t, p.ctx, b, "DELETE FROM t WHERE id=1", prepared, 1)
					} else {
						ap, bp := referenceRowPIDs(t, p.ctx, a, b)
						await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
							return referenceRowExecute(ctx, b, "DELETE FROM t WHERE id=1", prepared)
						})
						referenceRowWait(t, p.ctx, bp, ap)
						if err := holder.Rollback(); err != nil {
							t.Fatal(err)
						}
						out := await()
						if out.err != nil {
							t.Fatal(out.err)
						}
						referenceLockAffected(t, out.result, 1)
					}
				})
			}
		})
	}
}
