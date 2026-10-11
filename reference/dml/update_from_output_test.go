package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateFromOutputAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				updateFromSeed(run)
				q := "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k RETURNING x.id,1/(s.n-303)"
				var rows *sql.Rows
				var err error
				var stmt *sql.Stmt
				if prepared {
					stmt, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = c.QueryContext(p.Ctx, q)
				}
				emitted := 0
				if rows != nil {
					defer rows.Close()
					for rows.Next() {
						var id, output int64
						if scanErr := rows.Scan(&id, &output); scanErr != nil {
							t.Fatal(scanErr)
						}
						if id != 1 || output != 0 {
							t.Fatal(id, output)
						}
						emitted++
					}
					err = rows.Err()
					rows.Close()
				}
				if err == nil {
					t.Fatal("source-driven output failure was not reported")
				}
				want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				fixture.AssertDiagnostic(t, err, &want)
				if emitted > 1 {
					t.Fatal("unexpected streamed output count", emitted)
				}
				if !b.PG && emitted != 0 {
					t.Fatal("native eager Query exposed partial output", emitted)
				}
				run(updateFromTargets(updateFromOriginal()))
			})
		})
	}
}
