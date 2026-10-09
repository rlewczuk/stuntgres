package reference_test

import (
	"context"
	"testing"
)

func TestReferenceRowLockOrderedCacheFreshOrder(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"t", "w", "stored"} {
			t.Run(source+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceViewFixture(t)
				p.step(t, referenceExec("CREATE VIEW stored AS SELECT id,v FROM t WHERE id<3 ORDER BY v+1,id FOR UPDATE", 0))
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						a, b := referenceLockConnections(t, p.ctx, backend.db)
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
						holder, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { holder.Rollback() })
						step := referenceSelect(query, []string{"k"}, []string{"INT4"}, [][]any{{first}, {second}})
						if prepared {
							step = referencePrepared(step)
						}
						referenceRunStep(t, p.ctx, holder, backend.recorder, step)
						if backend.pg {
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
							referenceLockExec(t, p.ctx, b, "INSERT INTO t VALUES(1,10)", 1)
						} else {
							referenceViewNativeBlocked(t, p.ctx, b, "DELETE FROM t WHERE id=1", prepared)
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
						}
						referenceLockExec(t, p.ctx, b, "UPDATE t SET v=30 WHERE id=1", 1)
						step = referenceSelect(query, []string{"k"}, []string{"INT4"}, [][]any{{second}, {first + 20}})
						if prepared {
							step = referencePrepared(step)
						}
						referenceRunStep(t, p.ctx, a, backend.recorder, step)
						referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE s", 0)
						referenceLockExec(t, p.ctx, a, "UPDATE t SET v=10 WHERE id=1", 1)
						query = "SELECT nextval('s') AS n FROM " + source + " WHERE id=1 ORDER BY n,n"
						if source != "stored" {
							query += " FOR UPDATE"
						}
						step = referenceSelect(query, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
						if prepared {
							step = referencePrepared(step)
						}
						referenceRunStep(t, p.ctx, a, backend.recorder, step)
						referenceRunStep(t, p.ctx, a, backend.recorder, referenceSelect("SELECT currval('s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
						referenceLockExec(t, p.ctx, a, "CREATE TABLE target(v INT)", 0)
						step = referenceImplicitError("INSERT INTO target SELECT 1/(v-10) AS k FROM t WHERE id=1 ORDER BY k FOR UPDATE", referenceDiagnostic{Code: "22012", Message: "division by zero"})
						step.prepared, step.errorStage = prepared, "execute"
						referenceRunStep(t, p.ctx, a, backend.recorder, step)
						referenceRunStep(t, p.ctx, b, backend.recorder, referenceSelect("SELECT v FROM target", []string{"v"}, []string{"INT4"}, nil))
						referenceLockExec(t, p.ctx, b, "UPDATE t SET v=v WHERE id=1", 1)
					})
				}
			})
		}
	}
}
