package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockViewInsert(t *testing.T) {
	for _, clause := range fixture.RowClauses {
		for _, prepared := range []bool{false, true} {
			for _, source := range []string{"w", "marked"} {
				t.Run(clause+"/"+source+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := referenceViewFixture(t)
					p.Step(t, fixture.Exec("CREATE TABLE destination(v INT)", 0))
					p.Step(t, fixture.Exec("CREATE VIEW marked AS SELECT id,v+1 AS val FROM t WHERE id<3 "+clause, 0))
					suffix := ""
					if source == "w" {
						suffix = " " + clause
					}
					query := "INSERT INTO destination SELECT val FROM " + source + " WHERE id=1" + suffix
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
							holder, err := a.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer holder.Rollback()
							fixture.RowAffected(t, p.Ctx, holder, query, prepared, 1)
							fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, holder, "SELECT v FROM destination", prepared), backend.PG, 11)
							if backend.PG {
								ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
								done := fixture.RowAsync(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared, false)
								fixture.RowWait(t, p.Ctx, bp, ap)
								if err := holder.Rollback(); err != nil {
									t.Fatal(err)
								}
								out := fixture.LockResult(t, done)
								if out.Err != nil {
									t.Fatal(out.Err)
								}
								fixture.LockAffected(t, out.Result, 1)
							} else {
								fixture.ViewNativeBlocked(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared)
								if err := holder.Rollback(); err != nil {
									t.Fatal(err)
								}
								fixture.RowAffected(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared, 1)
							}
							fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, a, "SELECT v FROM destination", false), backend.PG)
							fixture.RowAffected(t, p.Ctx, a, "INSERT INTO t VALUES(1,10)", false, 1)
							failure := fixture.ImplicitError("INSERT INTO destination SELECT 1/(val-11) FROM "+source+" WHERE id=1"+suffix, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
							failure.Prepared, failure.ErrorStage = prepared, "execute"
							fixture.RunStep(t, p.Ctx, a, backend.Recorder, failure)
							fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM destination", false), backend.PG)
							fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=1", prepared, 1)
						})
					}
				})
			}
		}
	}
}
