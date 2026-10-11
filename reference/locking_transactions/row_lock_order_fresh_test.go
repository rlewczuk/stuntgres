package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockOrderedCacheFreshOrder(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"t", "w", "stored"} {
			t.Run(source+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceViewFixture(t)
				p.Step(t, fixture.Exec("CREATE VIEW stored AS SELECT id,v FROM t WHERE id<3 ORDER BY v+1,id FOR UPDATE", 0))
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
						expression := "v+1"
						first, second := int64(11), int64(21)
						if source == "w" {
							expression = "val+1"
							first, second = 12, 22
						}
						query := "SELECT " + expression + " AS k FROM " + source + " WHERE id<3 ORDER BY k,id"
						if source != "stored" {
							query += " FOR UPDATE"
						}
						holder, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { holder.Rollback() })
						step := fixture.Select(query, []string{"k"}, []string{"INT4"}, [][]any{{first}, {second}})
						if prepared {
							step = fixture.Prepared(step)
						}
						fixture.RunStep(t, p.Ctx, holder, backend.Recorder, step)
						if backend.PG {
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
							fixture.LockExec(t, p.Ctx, b, "INSERT INTO t VALUES(1,10)", 1)
						} else {
							fixture.ViewNativeBlocked(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared)
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
						}
						fixture.LockExec(t, p.Ctx, b, "UPDATE t SET v=30 WHERE id=1", 1)
						step = fixture.Select(query, []string{"k"}, []string{"INT4"}, [][]any{{second}, {first + 20}})
						if prepared {
							step = fixture.Prepared(step)
						}
						fixture.RunStep(t, p.Ctx, a, backend.Recorder, step)
						fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE s", 0)
						fixture.LockExec(t, p.Ctx, a, "UPDATE t SET v=10 WHERE id=1", 1)
						query = "SELECT nextval('s') AS n FROM " + source + " WHERE id=1 ORDER BY n,n"
						if source != "stored" {
							query += " FOR UPDATE"
						}
						step = fixture.Select(query, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
						if prepared {
							step = fixture.Prepared(step)
						}
						fixture.RunStep(t, p.Ctx, a, backend.Recorder, step)
						fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Select("SELECT currval('s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE target(v INT)", 0)
						step = fixture.ImplicitError("INSERT INTO target SELECT 1/(v-10) AS k FROM t WHERE id=1 ORDER BY k FOR UPDATE", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
						step.Prepared, step.ErrorStage = prepared, "execute"
						fixture.RunStep(t, p.Ctx, a, backend.Recorder, step)
						fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SELECT v FROM target", []string{"v"}, []string{"INT4"}, nil))
						fixture.LockExec(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=1", 1)
					})
				}
			})
		}
	}
}
