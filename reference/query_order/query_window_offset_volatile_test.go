package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowOffsetVolatile(t *testing.T) {
	queryWindowOffsetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowOffsetSeed(t, p, b)
		queryWindowRun(t, p, b, fixture.Exec("CREATE SEQUENCE wo_independent START WITH 1", 0))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ name, a, c, order string }{
				{"postponed-identical", "lead(nextval('wo_independent'),0,-1)", "lead(nextval('wo_independent'),0,-1)", "id"},
				{"different-offset", "lead(nextval('wo_independent'),0,-1)", "lead(nextval('wo_independent'),id-id,-1)", "a"},
				{"different-default", "lead(1,100,nextval('wo_independent'))", "lead(1,100,nextval('wo_independent')+0)", "a"},
				{"both-required", "lag(1,100,nextval('wo_independent'))", "lag(1,100,nextval('wo_independent'))", "a,b"},
			} {
				t.Run(fmt.Sprintf("%s/%t", tc.name, prepared), func(t *testing.T) {
					queryWindowRun(t, p, b, fixture.Exec("ALTER SEQUENCE wo_independent RESTART WITH 1", 0))
					q := "SELECT " + tc.a + " OVER w AS a," + tc.c + " OVER w AS b FROM wo WINDOW w AS(ORDER BY id) ORDER BY " + tc.order
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, e := b.DB.PrepareContext(p.Ctx, q)
						if e != nil {
							t.Fatal(e)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(p.Ctx)
					} else {
						rows, err = b.DB.QueryContext(p.Ctx, q)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.ColumnTypes()
					if err != nil || len(columns) != 2 {
						t.Fatal(columns, err)
					}
					for i, c := range columns {
						if c.Name() != []string{"a", "b"}[i] || c.DatabaseTypeName() != "INT8" {
							t.Fatal(i, c)
						}
					}
					var seen [13]bool
					count := 0
					for rows.Next() {
						var a, c int64
						if err := rows.Scan(&a, &c); err != nil {
							t.Fatal(err)
						}
						for _, v := range []int64{a, c} {
							if v < 1 || v > 12 || seen[v] {
								t.Fatalf("independent calls reused/lost value: %d,%d", a, c)
							}
							seen[v] = true
						}
						count++
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					if count != 6 {
						t.Fatal("row count", count)
					}
					for v := 1; v <= 12; v++ {
						if !seen[v] {
							t.Fatal("missing sequence value", v)
						}
					}
					queryWindowRun(t, p, b, fixture.Select("SELECT last_value FROM wo_independent", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(12)}}))
				})
			}
		}
	})
}
