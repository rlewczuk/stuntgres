package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockViewAggregation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := referenceViewFixture(t)
			for _, step := range []fixture.Step{fixture.Exec("CREATE VIEW stored AS SELECT id,v FROM t FOR UPDATE", 0), fixture.Exec("CREATE VIEW ordinary_group AS SELECT count(*) AS n FROM t", 0)} {
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
					step := fixture.Select("SELECT ordinary_group.n FROM stored CROSS JOIN ordinary_group WHERE stored.id=1", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}})
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, holder, backend.Recorder, step)
					fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=2", true, 1)
					fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=3", true, 1)
					fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=22 WHERE id=2", prepared, 1)
					if !backend.PG {
						fixture.ViewNativeBlocked(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared)
						if err := holder.Rollback(); err != nil {
							t.Fatal(err)
						}
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
