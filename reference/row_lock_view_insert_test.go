package reference_test

import (
	"testing"
)

func TestReferenceRowLockViewInsert(t *testing.T) {
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			for _, source := range []string{"w", "marked"} {
				t.Run(clause+"/"+source+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := referenceViewFixture(t)
					p.step(t, referenceExec("CREATE TABLE destination(v INT)", 0))
					p.step(t, referenceExec("CREATE VIEW marked AS SELECT id,v+1 AS val FROM t WHERE id<3 "+clause, 0))
					suffix := ""
					if source == "w" {
						suffix = " " + clause
					}
					query := "INSERT INTO destination SELECT val FROM " + source + " WHERE id=1" + suffix
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							a, b := referenceLockConnections(t, p.ctx, backend.db)
							holder, err := a.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer holder.Rollback()
							referenceRowAffected(t, p.ctx, holder, query, prepared, 1)
							referenceLimitLockValues(t, referenceRowQuery(p.ctx, holder, "SELECT v FROM destination", prepared), backend.pg, 11)
							if backend.pg {
								ap, bp := referenceRowPIDs(t, p.ctx, a, b)
								done := referenceRowAsync(t, p.ctx, b, "DELETE FROM t WHERE id=1", prepared, false)
								referenceRowWait(t, p.ctx, bp, ap)
								if err := holder.Rollback(); err != nil {
									t.Fatal(err)
								}
								out := referenceLockResult(t, done)
								if out.err != nil {
									t.Fatal(out.err)
								}
								referenceLockAffected(t, out.result, 1)
							} else {
								referenceViewNativeBlocked(t, p.ctx, b, "DELETE FROM t WHERE id=1", prepared)
								if err := holder.Rollback(); err != nil {
									t.Fatal(err)
								}
								referenceRowAffected(t, p.ctx, b, "DELETE FROM t WHERE id=1", prepared, 1)
							}
							referenceLimitLockValues(t, referenceRowQuery(p.ctx, a, "SELECT v FROM destination", false), backend.pg)
							referenceRowAffected(t, p.ctx, a, "INSERT INTO t VALUES(1,10)", false, 1)
							failure := referenceImplicitError("INSERT INTO destination SELECT 1/(val-11) FROM "+source+" WHERE id=1"+suffix, referenceDiagnostic{Code: "22012", Message: "division by zero"})
							failure.prepared, failure.errorStage = prepared, "execute"
							referenceRunStep(t, p.ctx, a, backend.recorder, failure)
							referenceLimitLockValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM destination", false), backend.pg)
							referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=1", prepared, 1)
						})
					}
				})
			}
		}
	}
}
