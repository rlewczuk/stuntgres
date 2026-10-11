package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceViewFixture(t *testing.T) *fixture.Pair {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(id INT,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3),
		fixture.Exec("CREATE VIEW v AS SELECT id,v FROM t WHERE id<3", 0), fixture.Exec("CREATE VIEW w AS SELECT id,v+1 AS val FROM v", 0),
	} {
		p.Step(t, s)
	}
	return p
}

func TestReferenceRowLockViews(t *testing.T) {
	for _, clause := range fixture.RowClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceViewFixture(t)
				expected := fixture.Select("SELECT val FROM w WHERE id=1 "+clause, []string{"val"}, []string{"INT4"}, [][]any{{int64(11)}})
				expected.Prepared = prepared
				p.Step(t, expected)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
						holder, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						fixture.RunStep(t, p.Ctx, holder, backend.Recorder, expected)
						ordinary := fixture.Select("SELECT val FROM w WHERE id=1", []string{"val"}, []string{"INT4"}, [][]any{{int64(11)}})
						ordinary.Prepared = prepared
						fixture.RunStep(t, p.Ctx, b, backend.Recorder, ordinary)
						fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=2", prepared, 1)
						fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=3", prepared, 1)
						fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v+1 WHERE id=2", prepared, 1)
						fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v+1 WHERE id=3", prepared, 1)
						if clause == "FOR KEY SHARE" {
							fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=40 WHERE id=1", prepared, 1)
						} else if !backend.PG {
							fixture.ViewNativeBlocked(t, p.Ctx, b, "UPDATE t SET v=40 WHERE id=1", prepared)
						} else {
							fixture.LockExec(t, p.Ctx, b, "SET lock_timeout='100ms'", 0)
							ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
							done := fixture.RowAsync(t, p.Ctx, b, "UPDATE t SET v=40 WHERE id=1", prepared, false)
							fixture.RowWait(t, p.Ctx, bp, ap)
							out := fixture.LockResult(t, done)
							if out.Result != nil {
								t.Fatal("failed view update returned result")
							}
							referenceRowTimeoutDiagnostic(t, out.Err, true, "55P03", "canceling statement due to lock timeout", "updating")
							fixture.LockExec(t, p.Ctx, b, "RESET lock_timeout", 0)
						}
						if !backend.PG {
							fixture.ViewNativeBlocked(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared)
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
							fixture.RowAffected(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared, 1)
						} else {
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
						}
					})
				}
			})
		}
	}
	if *fixture.Mode {
		referenceViewReadCommitted(t)
	}
}

func referenceViewReferenceProvenance(t *testing.T) {
	for _, body := range []string{
		"SELECT a.id,a.v+1 AS val FROM t a JOIN t b USING(id)",
		"SELECT a.id,a.v+1 AS val FROM t a NATURAL JOIN t b",
		"SELECT a.id,a.v+1 AS val FROM t a JOIN t b ON a.id=b.id",
		"SELECT t.id,t.v+1 AS val FROM t CROSS JOIN constant_view",
		"SELECT t.id,t.v+1 AS val FROM t JOIN u ON t.id=u.id",
	} {
		t.Run(body, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2), fixture.Exec("CREATE TABLE u(id INT)", 0), fixture.Exec("INSERT INTO u VALUES(1),(1),(3)", 3), fixture.Exec("CREATE VIEW constant_view AS SELECT num_nulls(NULL) AS n", 0), fixture.Exec("CREATE VIEW joined AS "+body, 0)} {
				p.Step(t, s)
			}
			want := [][]any{{int64(11)}}
			if body == "SELECT t.id,t.v+1 AS val FROM t JOIN u ON t.id=u.id" {
				want = append(want, []any{int64(11)})
			}
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT val FROM joined WHERE id=1 FOR UPDATE", []string{"val"}, []string{"INT4"}, want)
				step.Prepared, step.Bag = prepared, true
				p.Step(t, step)
			}
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					holder, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					expected := make([]int64, len(want))
					for i := range expected {
						expected[i] = 11
					}
					fixture.ViewValues(t, fixture.RowQuery(p.Ctx, holder, "SELECT val FROM joined WHERE id=1 FOR UPDATE", true, backend.Recorder.RawBackend), "val", "INT4", expected...)
					fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=2", true, 1)
					fixture.RowAffected(t, p.Ctx, b, "DELETE FROM u WHERE id=3", true, 1)
					query := "DELETE FROM t WHERE id=1"
					count := int64(1)
					if len(want) == 2 {
						query = "DELETE FROM u WHERE id=1"
						count = 2
					}
					if !backend.PG {
						fixture.ViewNativeBlocked(t, p.Ctx, b, query, true)
						holder.Rollback()
						fixture.RowAffected(t, p.Ctx, b, query, true, count)
					} else {
						ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
						done := fixture.RowAsync(t, p.Ctx, b, query, true, false)
						fixture.RowWait(t, p.Ctx, bp, ap)
						holder.Rollback()
						out := fixture.LockResult(t, done)
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

func TestReferenceRowLockViewStored(t *testing.T) {
	for _, clause := range fixture.RowClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceViewFixture(t)
				p.Step(t, fixture.Exec("CREATE VIEW stored AS SELECT id,v FROM t ORDER BY id "+clause, 0))
				implicit := fixture.Select("SELECT v FROM stored WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
				implicit.Prepared = prepared
				p.Step(t, implicit)
				for _, backend := range p.Backends() {
					var definition string
					if err := backend.DB.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='stored'").Scan(&definition); err != nil {
						t.Fatal(err)
					}
					order, mark := strings.Index(definition, "ORDER BY"), strings.Index(definition, clause)
					if order < 0 || mark < order {
						t.Fatal("stored mark must follow ORDER BY", definition)
					}
				}
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
						holder, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						step := fixture.Select("SELECT v FROM stored WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, holder, backend.Recorder, step)
						fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=2", prepared, 1)
						fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=3", prepared, 1)
						if !backend.PG {
							fixture.ViewNativeBlocked(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared)
							holder.Rollback()
							fixture.RowAffected(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared, 1)
						} else {
							ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
							done := fixture.RowAsync(t, p.Ctx, b, "DELETE FROM t WHERE id=1", prepared, false)
							fixture.RowWait(t, p.Ctx, bp, ap)
							holder.Rollback()
							out := fixture.LockResult(t, done)
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
}
