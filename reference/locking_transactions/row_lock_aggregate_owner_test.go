package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockAggregateStatementOwner(t *testing.T) {
	for _, joined := range []bool{false, true} {
		for _, insert := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "scalar", true: "joined"}[joined]+map[bool]string{false: "/select", true: "/insert"}[insert]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := referenceViewFixture(t)
					for _, step := range []fixture.Step{fixture.Exec("CREATE SEQUENCE s", 0), fixture.Exec("CREATE VIEW stored AS SELECT id FROM t WHERE id<3 FOR UPDATE", 0), fixture.Exec("CREATE TABLE u(id INT)", 0), fixture.Exec("INSERT INTO u VALUES(1),(1),(2)", 3), fixture.Exec("CREATE TABLE destination(n BIGINT)", 0)} {
						p.Step(t, step)
					}
					from := "stored"
					want := []int64{1, 1}
					if joined {
						from += " JOIN u USING(id)"
						want = append(want, 1)
					}
					query := "SELECT count(*) AS n FROM " + from + " GROUP BY nextval('s') ORDER BY n"
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
							values := func(out fixture.RelationOutcome, want ...int64) {
								out.RawBackend = backend.Recorder.RawBackend
								fixture.ViewValues(t, out, "n", "INT8", want...)
							}
							ap, bp := 0, 0
							if backend.PG {
								ap, bp = fixture.RowPIDs(t, p.Ctx, a, b)
							}
							holder, err := a.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer holder.Rollback()
							if insert {
								fixture.RowAffected(t, p.Ctx, holder, "INSERT INTO destination "+query, prepared, int64(len(want)))
								values(fixture.RowQuery(p.Ctx, holder, "SELECT n FROM destination ORDER BY n", false), want...)
							} else {
								values(fixture.RowQuery(p.Ctx, holder, query, prepared), want...)
							}
							values(fixture.RowQuery(p.Ctx, holder, "SELECT currval('s') AS n", false), int64(len(want)))
							fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=3", false, 1)
							if joined {
								fixture.RowAffected(t, p.Ctx, b, "DELETE FROM u WHERE id=2", false, 1)
							}
							if backend.PG {
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
							values(fixture.RowQuery(p.Ctx, b, "SELECT n FROM destination", false))
							values(fixture.RowQuery(p.Ctx, a, "SELECT currval('s') AS n", false), int64(len(want)))
						})
					}
				})
			}
		}
	}
}
