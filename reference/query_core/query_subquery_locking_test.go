package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
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
				subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
					run := subqueryRun(t, p, b, a)
					run(fixture.Exec("CREATE TABLE r(v INT)", 0))
					run(fixture.Exec("INSERT INTO r VALUES(1),(2)", 2))
					other, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer other.Close()
					tx, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					value, typ := any(int64(1)), "INT4"
					if q == "SELECT EXISTS(SELECT v FROM r WHERE v=1 FOR UPDATE) AS locked" {
						value, typ = true, "BOOL"
					}
					s := fixture.Select(q, []string{"locked"}, []string{typ}, [][]any{{value}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
					fixture.RowAffected(t, p.Ctx, other, "UPDATE r SET v=2 WHERE v=2", prepared, 1)
					if b.PG {
						ap, bp := fixture.RowPIDs(t, p.Ctx, a, other)
						done := fixture.RowAsync(t, p.Ctx, other, "UPDATE r SET v=3 WHERE v=1", prepared, false)
						fixture.RowWait(t, p.Ctx, bp, ap)
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						out := fixture.LockResult(t, done)
						if out.Err != nil {
							t.Fatal(out.Err)
						}
						fixture.LockAffected(t, out.Result, 1)
					} else {
						fixture.ViewNativeBlocked(t, p.Ctx, other, "UPDATE r SET v=3 WHERE v=1", prepared)
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						fixture.RowAffected(t, p.Ctx, other, "UPDATE r SET v=3 WHERE v=1", prepared, 1)
					}
					run(fixture.Select("SELECT v FROM r ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
				})
			})
		}
	}
}

func TestReferenceQuerySubqueryConstantViewReadOnlyLocking(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE VIEW constant_view AS SELECT 1 AS x", 0))
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
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
					stmt, prepareErr := tx.PrepareContext(p.Ctx, q)
					if prepareErr != nil {
						t.Fatal("read-only restriction occurred during Prepare", prepareErr)
					}
					t.Cleanup(func() { stmt.Close() })
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = tx.QueryContext(p.Ctx, q)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("read-only failure exposed a cursor")
				}
				want := fixture.ControlsReferenceDiagnostic("25006", "cannot execute SELECT in a read-only transaction")
				fixture.AssertDiagnostic(t, err, &want)
			})
		}
	})
}
