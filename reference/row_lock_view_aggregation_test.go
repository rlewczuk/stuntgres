package reference_test

import (
	"context"
	"testing"
)

func TestReferenceRowLockViewAggregation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := referenceViewFixture(t)
			for _, step := range []referenceStep{referenceExec("CREATE VIEW stored AS SELECT id,v FROM t FOR UPDATE", 0), referenceExec("CREATE VIEW ordinary_group AS SELECT count(*) AS n FROM t", 0)} {
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
					step := referenceSelect("SELECT ordinary_group.n FROM stored CROSS JOIN ordinary_group WHERE stored.id=1", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}})
					step.prepared = prepared
					referenceRunStep(t, p.ctx, holder, backend.recorder, step)
					referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=2", true, 1)
					referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=3", true, 1)
					referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=22 WHERE id=2", prepared, 1)
					if !backend.pg {
						referenceViewNativeBlocked(t, p.ctx, b, "DELETE FROM t WHERE id=1", prepared)
						if err := holder.Rollback(); err != nil {
							t.Fatal(err)
						}
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
