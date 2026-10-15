package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowRankMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%t", empty), func(t *testing.T) {
			q := "SELECT rank() OVER(),dense_rank() OVER() AS d,percent_rank() OVER(),cume_dist() OVER() AS cd"
			var expected [][]any
			if empty {
				q += " WHERE FALSE"
			} else {
				expected = [][]any{{int64(1), int64(1), float64(0), float64(1)}}
			}
			s := fixture.Select(q, []string{"rank", "d", "percent_rank", "cd"}, []string{"INT8", "INT8", "FLOAT8", "FLOAT8"}, expected)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 4 {
					t.Fatal(columns, err)
				}
				for i, c := range columns {
					want := reflect.TypeFor[int64]()
					if i >= 2 {
						want = reflect.TypeFor[float64]()
					}
					nullable, known := c.Nullable()
					length, lengthOK := c.Length()
					precision, scale, decimalOK := c.DecimalSize()
					if c.Name() != s.Columns[i] || c.DatabaseTypeName() != s.Types[i] || c.ScanType() != want {
						t.Errorf("metadata column=%d name=%q dbtype=%q scan=%v; expected %q %q %v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), s.Columns[i], s.Types[i], want)
					}
					if nullable || known != !b.PG {
						t.Errorf("nullable column=%d (%t,%t)", i, nullable, known)
					}
					if length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK {
						t.Errorf("size column=%d length=(%d,%t) decimal=(%d,%d,%t)", i, length, lengthOK, precision, scale, decimalOK)
					}
				}
			}
			s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
				if reflect.TypeOf(value) != reflect.TypeOf(expected[row][column]) || value != expected[row][column] {
					t.Fatalf("raw column=%d got %T %v expected %T %v", column, value, value, expected[row][column], expected[row][column])
				}
				return true
			}
			s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
				var r int64
				var d sql.NullInt64
				var pr float64
				var cd sql.NullFloat64
				if err := rows.Scan(&r, &d, &pr, &cd); err != nil || r != 1 || !d.Valid || d.Int64 != 1 || pr != 0 || !cd.Valid || cd.Float64 != 1 {
					t.Fatal("typed scans", r, d, pr, cd, err)
				}
			}
			queryWindowRoutes(t, p, b, s)
		})
	}
}

func TestReferenceQueryWindowRankMetadata(t *testing.T) {
	queryWindowRankScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { queryWindowRankMetadata(t, p, b) })
}
