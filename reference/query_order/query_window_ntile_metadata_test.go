package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryWindowNtileMetadata(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowNtileSeed(t, p, b)
		for _, empty := range []bool{false, true} {
			q := "SELECT ntile(2) OVER(ORDER BY id),ntile(NULL) OVER(ORDER BY id) AS n FROM nt"
			var expected [][]any
			if empty {
				q += " WHERE FALSE"
			} else {
				q += " WHERE id<=3 ORDER BY id"
				expected = [][]any{{int64(1), nil}, {int64(1), nil}, {int64(2), nil}}
			}
			s := fixture.Select(q, []string{"ntile", "n"}, []string{"INT4", "INT4"}, expected)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 2 {
					t.Fatal(columns, err)
				}
				for i, c := range columns {
					want := reflect.TypeFor[int64]()
					if b.PG {
						want = reflect.TypeFor[int32]()
					}
					if c.Name() != s.Columns[i] || c.DatabaseTypeName() != "INT4" || c.ScanType() != want {
						t.Fatal("ntile metadata", i, c)
					}
					if nullable, known := c.Nullable(); nullable != !b.PG || known != !b.PG {
						t.Fatal("nullable", nullable, known)
					}
					if length, ok := c.Length(); length != 0 || ok {
						t.Fatal("length", length, ok)
					}
					if precision, scale, ok := c.DecimalSize(); precision != 0 || scale != 0 || ok {
						t.Fatal("decimal", precision, scale, ok)
					}
				}
			}
			s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
				if column == 1 {
					if value != nil {
						t.Fatalf("NULL raw %T %v", value, value)
					}
					return true
				}
				n, ok := value.(int64)
				if !ok || n != expected[row][column] {
					t.Fatalf("INT4 raw %T %v; expected %v", value, value, expected[row][column])
				}
				return true
			}
			s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
				var n, null sql.NullInt64
				if err := rows.Scan(&n, &null); err != nil || !n.Valid || n.Int64 != expected[index][0] || null.Valid {
					t.Fatal("nullable scan", n, null, err)
				}
			}
			queryWindowRoutes(t, p, b, s)
		}
	})
}
