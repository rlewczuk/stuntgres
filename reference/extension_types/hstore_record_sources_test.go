package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreRecordScalarSources(t *testing.T) {
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		for _, tc := range []struct {
			base, source string
			rows         [][]any
		}{
			{"'(7,keep)'::hs_record_pair", "'i=>42'::hstore", [][]any{{int64(42), "keep"}}},
			{"NULL::hs_record_pair", "NULL::hstore", [][]any{{nil, nil}}},
			{"NULL::hs_record_pair", "''::hstore", [][]any{{nil, nil}}},
		} {
			for _, empty := range []bool{false, true} {
				q := "SELECT * FROM populate_record(" + tc.base + "," + tc.source + ")"
				want := tc.rows
				if empty {
					q += " WHERE false"
					want = [][]any{}
				}
				s := fixture.Select(q, []string{"i", "n"}, []string{"INT4", "TEXT"}, want)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if len(columns) != 2 {
						t.Fatalf("source columns %d want 2", len(columns))
					}
					for i, column := range columns {
						scan := reflect.TypeFor[string]()
						if i == 0 {
							scan = reflect.TypeFor[int64]()
							if b.PG {
								scan = reflect.TypeFor[int32]()
							}
						}
						if column.ScanType() != scan {
							t.Fatalf("source %d scan=%v want %v", i, column.ScanType(), scan)
						}
						if n, ok := column.Nullable(); n || ok {
							t.Fatalf("source %d nullable=%v/%v want unknown", i, n, ok)
						}
						length, lengthOK := int64(0), false
						if i == 1 {
							length, lengthOK = 9223372036854775807, true
						}
						if n, ok := column.Length(); n != length || ok != lengthOK {
							t.Fatalf("source %d length=%d/%v want %d/%v", i, n, ok, length, lengthOK)
						}
						if precision, scale, ok := column.DecimalSize(); precision != 0 || scale != 0 || ok {
							t.Fatalf("source %d decimal=%d/%d/%v", i, precision, scale, ok)
						}
					}
				}
				hstoreRecordBoth(t, p, b, c, s)
			}
		}
	})
}
