package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQuerySubqueryLocking(t *testing.T) {
	for _, q := range []string{
		"SELECT (SELECT v FROM r WHERE v=1 FOR UPDATE) AS locked",
		"SELECT EXISTS(SELECT v FROM r WHERE v=1 FOR UPDATE) AS locked",
		"SELECT v AS locked FROM (SELECT v FROM r WHERE v=1 FOR UPDATE) d",
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, a *sql.Conn) {
					run := subqueryRun(t, p, b, a)
					run(referenceExec("CREATE TABLE r(v INT)", 0))
					run(referenceExec("INSERT INTO r VALUES(1),(2)", 2))
					other, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer other.Close()
					tx, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					value, typ := any(int64(1)), "INT4"
					if q == "SELECT EXISTS(SELECT v FROM r WHERE v=1 FOR UPDATE) AS locked" {
						value, typ = true, "BOOL"
					}
					s := referenceSelect(q, []string{"locked"}, []string{typ}, [][]any{{value}})
					s.prepared = prepared
					referenceRunStep(t, p.ctx, tx, b.recorder, s)
					referenceRowAffected(t, p.ctx, other, "UPDATE r SET v=2 WHERE v=2", prepared, 1)
					if b.pg {
						ap, bp := referenceRowPIDs(t, p.ctx, a, other)
						done := referenceRowAsync(t, p.ctx, other, "UPDATE r SET v=3 WHERE v=1", prepared, false)
						referenceRowWait(t, p.ctx, bp, ap)
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						out := referenceLockResult(t, done)
						if out.err != nil {
							t.Fatal(out.err)
						}
						referenceLockAffected(t, out.result, 1)
					} else {
						referenceViewNativeBlocked(t, p.ctx, other, "UPDATE r SET v=3 WHERE v=1", prepared)
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						referenceRowAffected(t, p.ctx, other, "UPDATE r SET v=3 WHERE v=1", prepared, 1)
					}
					run(referenceSelect("SELECT v FROM r ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
				})
			})
		}
	}
}

func TestReferenceQuerySubqueryConstantViewReadOnlyLocking(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(referenceExec("CREATE VIEW constant_view AS SELECT 1 AS x", 0))
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				tx, err := c.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
						t.Error(err)
					}
				})
				q := "SELECT * FROM constant_view FOR UPDATE"
				var rows *sql.Rows
				if prepared {
					stmt, prepareErr := tx.PrepareContext(p.ctx, q)
					if prepareErr != nil {
						t.Fatal("read-only restriction occurred during Prepare", prepareErr)
					}
					t.Cleanup(func() { stmt.Close() })
					rows, err = stmt.QueryContext(p.ctx)
				} else {
					rows, err = tx.QueryContext(p.ctx, q)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("read-only failure exposed a cursor")
				}
				want := controlsReferenceDiagnostic("25006", "cannot execute SELECT in a read-only transaction")
				referenceAssertDiagnostic(t, err, &want)
			})
		}
	})
}
