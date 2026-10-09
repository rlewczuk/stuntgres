package reference_test

import (
	"testing"
)

func TestReferenceRowLockAggregateStatementOwner(t *testing.T) {
	for _, joined := range []bool{false, true} {
		for _, insert := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "scalar", true: "joined"}[joined]+map[bool]string{false: "/select", true: "/insert"}[insert]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := referenceViewFixture(t)
					for _, step := range []referenceStep{referenceExec("CREATE SEQUENCE s", 0), referenceExec("CREATE VIEW stored AS SELECT id FROM t WHERE id<3 FOR UPDATE", 0), referenceExec("CREATE TABLE u(id INT)", 0), referenceExec("INSERT INTO u VALUES(1),(1),(2)", 3), referenceExec("CREATE TABLE destination(n BIGINT)", 0)} {
						p.step(t, step)
					}
					from := "stored"
					want := []int64{1, 1}
					if joined {
						from += " JOIN u USING(id)"
						want = append(want, 1)
					}
					query := "SELECT count(*) AS n FROM " + from + " GROUP BY nextval('s') ORDER BY n"
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							a, b := referenceLockConnections(t, p.ctx, backend.db)
							values := func(out relationOutcome, want ...int64) {
								out.rawBackend = backend.recorder.rawBackend
								referenceViewValues(t, out, "n", "INT8", want...)
							}
							ap, bp := 0, 0
							if backend.pg {
								ap, bp = referenceRowPIDs(t, p.ctx, a, b)
							}
							holder, err := a.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer holder.Rollback()
							if insert {
								referenceRowAffected(t, p.ctx, holder, "INSERT INTO destination "+query, prepared, int64(len(want)))
								values(referenceRowQuery(p.ctx, holder, "SELECT n FROM destination ORDER BY n", false), want...)
							} else {
								values(referenceRowQuery(p.ctx, holder, query, prepared), want...)
							}
							values(referenceRowQuery(p.ctx, holder, "SELECT currval('s') AS n", false), int64(len(want)))
							referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=3", false, 1)
							if joined {
								referenceRowAffected(t, p.ctx, b, "DELETE FROM u WHERE id=2", false, 1)
							}
							if backend.pg {
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
							values(referenceRowQuery(p.ctx, b, "SELECT n FROM destination", false))
							values(referenceRowQuery(p.ctx, a, "SELECT currval('s') AS n", false), int64(len(want)))
						})
					}
				})
			}
		}
	}
}
