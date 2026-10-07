//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func referenceViewFixture(t *testing.T) *referencePair {
	t.Helper()
	p := newReferencePair(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT,v INT)", 0), referenceExec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3),
		referenceExec("CREATE VIEW v AS SELECT id,v FROM t WHERE id<3", 0), referenceExec("CREATE VIEW w AS SELECT id,v+1 AS val FROM v", 0),
	} {
		p.step(t, s)
	}
	return p
}

// Native cancellation is its own public control, not proof of a PostgreSQL queue.
// The PostgreSQL branch below instead observes both backend PIDs and the real wait.
func referenceViewNativeBlocked(t *testing.T, ctx context.Context, x referenceExecutor, q string, prepared bool) {
	t.Helper()
	bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	out := referenceRowExecute(bounded, x, q, prepared)
	if !errors.Is(out.err, context.DeadlineExceeded) || out.result != nil {
		t.Fatal("native view conflict", out)
	}
}

func TestReferenceRowLockViews(t *testing.T) {
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceViewFixture(t)
				expected := referenceSelect("SELECT val FROM w WHERE id=1 "+clause, []string{"val"}, []string{"INT4"}, [][]any{{int64(11)}})
				expected.prepared = prepared
				// Literal schema/values are asserted on the oracle before the native result.
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, expected)
				referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, expected)
				for _, backend := range []struct {
					name   string
					db     *sql.DB
					record *referenceRecorder
				}{
					{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord},
				} {
					t.Run(backend.name, func(t *testing.T) {
						a, b := referenceLockConnections(t, p.ctx, backend.db)
						holder, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						referenceRunStep(t, p.ctx, holder, backend.record, expected)
						referenceRunStep(t, p.ctx, b, backend.record, referenceSelect("SELECT val FROM w WHERE id=1", []string{"val"}, []string{"INT4"}, [][]any{{int64(11)}}))
						referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=2", prepared, 1)
						referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=3", prepared, 1)
						if clause == "FOR KEY SHARE" {
							referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=40 WHERE id=1", prepared, 1)
						}
						if backend.name == "native" {
							referenceViewNativeBlocked(t, p.ctx, b, "DELETE FROM t WHERE id=1", prepared)
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
							referenceRowAffected(t, p.ctx, b, "DELETE FROM t WHERE id=1", prepared, 1)
						} else {
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
						}
					})
				}
			})
		}
	}
}

func TestReferenceRowLockViewProvenance(t *testing.T) {
	for _, body := range []string{
		"SELECT a.id,a.v+1 AS val FROM t a JOIN t b USING(id)",
		"SELECT a.id,a.v+1 AS val FROM t a NATURAL JOIN t b",
		"SELECT a.id,a.v+1 AS val FROM t a JOIN t b ON a.id=b.id",
		"SELECT t.id,t.v+1 AS val FROM t CROSS JOIN constant_view",
		"SELECT t.id,t.v+1 AS val FROM t JOIN u ON t.id=u.id",
	} {
		t.Run(body, func(t *testing.T) {
			p := newReferencePair(t)
			for _, s := range []referenceStep{referenceExec("CREATE TABLE t(id INT,v INT)", 0), referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2), referenceExec("CREATE TABLE u(id INT)", 0), referenceExec("INSERT INTO u VALUES(1),(1),(3)", 3), referenceExec("CREATE VIEW constant_view AS SELECT 1 AS n", 0), referenceExec("CREATE VIEW joined AS "+body, 0)} {
				p.step(t, s)
			}
			want := [][]any{{int64(11)}}
			if body == "SELECT t.id,t.v+1 AS val FROM t JOIN u ON t.id=u.id" {
				want = append(want, []any{int64(11)})
			}
			for _, prepared := range []bool{false, true} {
				step := referenceSelect("SELECT val FROM joined WHERE id=1 FOR UPDATE", []string{"val"}, []string{"INT4"}, want)
				step.prepared, step.bag = prepared, true
				p.step(t, step)
			}
			for _, backend := range []struct {
				name string
				db   *sql.DB
			}{{"postgres", p.postgres}, {"native", p.memory}} {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					holder, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					expected := make([]int64, len(want))
					for i := range expected {
						expected[i] = 11
					}
					referenceViewValues(t, referenceRowQuery(p.ctx, holder, "SELECT val FROM joined WHERE id=1 FOR UPDATE", true), "val", "INT4", expected...)
					referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=2", true, 1)
					referenceRowAffected(t, p.ctx, b, "DELETE FROM u WHERE id=3", true, 1)
					query := "DELETE FROM t WHERE id=1"
					count := int64(1)
					if len(want) == 2 {
						query = "DELETE FROM u WHERE id=1"
						count = 2
					}
					if backend.name == "native" {
						referenceViewNativeBlocked(t, p.ctx, b, query, true)
						holder.Rollback()
						referenceRowAffected(t, p.ctx, b, query, true, count)
					} else {
						ap, bp := referenceRowPIDs(t, p.ctx, a, b)
						done := referenceRowAsync(t, p.ctx, b, query, true, false)
						referenceRowWait(t, p.ctx, bp, ap)
						holder.Rollback()
						out := referenceLockResult(t, done)
						if out.err != nil {
							t.Fatal(out.err)
						}
						referenceLockAffected(t, out.result, count)
					}
				})
			}
		})
	}
	t.Run("zero_width", func(t *testing.T) {
		p := newReferencePair(t)
		for _, s := range []referenceStep{referenceExec("CREATE TABLE t(id INT)", 0), referenceExec("INSERT INTO t VALUES(1),(2)", 2), referenceExec("CREATE VIEW zero_width AS SELECT FROM t WHERE id=1", 0)} {
			p.step(t, s)
		}
		step := referenceSelect("SELECT FROM zero_width FOR KEY SHARE", []string{}, []string{}, [][]any{{}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	})
}

func TestReferenceRowLockViewStored(t *testing.T) {
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceViewFixture(t)
				p.step(t, referenceExec("CREATE VIEW stored AS SELECT id,v FROM t ORDER BY id "+clause, 0))
				for _, db := range []*sql.DB{p.postgres, p.memory} {
					var definition string
					if err := db.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='stored'").Scan(&definition); err != nil {
						t.Fatal(err)
					}
					order, mark := strings.Index(definition, "ORDER BY"), strings.Index(definition, clause)
					if order < 0 || mark < order {
						t.Fatal("stored mark must follow ORDER BY", definition)
					}
				}
				for _, backend := range []struct {
					name   string
					db     *sql.DB
					record *referenceRecorder
				}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
					t.Run(backend.name, func(t *testing.T) {
						a, b := referenceLockConnections(t, p.ctx, backend.db)
						holder, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						step := referenceSelect("SELECT v FROM stored WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
						step.prepared = prepared
						referenceRunStep(t, p.ctx, holder, backend.record, step)
						referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=2", prepared, 1)
						referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=3", prepared, 1)
						if backend.name == "native" {
							referenceViewNativeBlocked(t, p.ctx, b, "DELETE FROM t WHERE id=1", prepared)
							holder.Rollback()
						} else {
							ap, bp := referenceRowPIDs(t, p.ctx, a, b)
							done := referenceRowAsync(t, p.ctx, b, "DELETE FROM t WHERE id=1", prepared, false)
							referenceRowWait(t, p.ctx, bp, ap)
							holder.Rollback()
							out := referenceLockResult(t, done)
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
	t.Run("strength_and_unmarked_sibling", func(t *testing.T) {
		p := referenceViewFixture(t)
		for _, s := range []referenceStep{referenceExec("CREATE TABLE u(id INT)", 0), referenceExec("INSERT INTO u VALUES(1)", 1), referenceExec("CREATE VIEW stored AS SELECT id,v FROM t FOR SHARE", 0)} {
			p.step(t, s)
		}
		for _, backend := range []struct {
			db     *sql.DB
			record *referenceRecorder
		}{{p.postgres, p.postgresRecord}, {p.memory, p.memoryRecord}} {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			holder, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer holder.Rollback()
				referenceRunStep(t, p.ctx, holder, backend.record, referenceSelect("SELECT stored.v FROM stored CROSS JOIN u WHERE stored.id=1 FOR NO KEY UPDATE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
				referenceRunStep(t, p.ctx, b, backend.record, referenceSelect("SELECT v FROM t WHERE id=1 FOR KEY SHARE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
			}()
			holder, err = a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer holder.Rollback()
				referenceRowValues(t, referenceRowQuery(p.ctx, holder, "SELECT stored.v FROM stored CROSS JOIN u WHERE stored.id=1", true), 10)
				referenceRowAffected(t, p.ctx, b, "DELETE FROM u", true, 1)
			}()
		}
	})
}
