package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func datetimeExtractMetadata(t *testing.T, pg bool, columns []*sql.ColumnType) {
	t.Helper()
	if len(columns) != 2 {
		t.Fatalf("column count %d", len(columns))
	}
	for i, column := range columns {
		name, typ, scan := "extract", "NUMERIC", reflect.TypeFor[string]()
		precision, scale, decimal := int64(0), int64(0), false
		if pg {
			scan, precision, scale, decimal = reflect.TypeFor[any](), 65535, 65531, true
		}
		if i == 1 {
			name, typ, scan, precision, scale, decimal = "date_part", "FLOAT8", reflect.TypeFor[float64](), 0, 0, false
		}
		p, s, ok := column.DecimalSize()
		length, hasLength := column.Length()
		nullable, known := column.Nullable()
		if column.Name() != name || column.DatabaseTypeName() != typ || column.ScanType() != scan || p != precision || s != scale || ok != decimal || length != 0 || hasLength || nullable || known {
			t.Fatalf("%s metadata name=%s type=%s scan=%v decimal=(%d,%d,%t) length=(%d,%t) nullable=(%t,%t)", name, column.Name(), column.DatabaseTypeName(), column.ScanType(), p, s, ok, length, hasLength, nullable, known)
		}
	}
}

func TestReferenceDatetimeExtractRawRetainedMetadata(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			field, source, suffix string
			rows                  [][]any
		}{
			{"second", "TIMESTAMP '2000-01-01 00:00:28.123456'", "", [][]any{{"28.123456", float64(28.123456)}}},
			{"second", "NULL::timestamp", "", [][]any{{nil, nil}}},
			{"second", "TIMESTAMP 'infinity'", "", [][]any{{nil, nil}}},
			{"year", "TIMESTAMP 'infinity'", "", [][]any{{"Infinity", math.Inf(1)}}},
			{"year", "TIMESTAMP '-infinity'", "", [][]any{{"-Infinity", math.Inf(-1)}}},
			{"year", "TIMESTAMP '2000-01-01'", " WHERE false", nil},
		} {
			q := "SELECT " + datetimeExtractSQL("extract", tc.field, tc.source) + "," + datetimeExtractSQL("date_part", tc.field, tc.source) + tc.suffix
			for _, prepared := range []bool{false, true} {
				t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					var retained []*sql.ColumnType
					s := fixture.Select(q, []string{"extract", "date_part"}, []string{"NUMERIC", "FLOAT8"}, tc.rows)
					s.Prepared = prepared
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						var err error
						retained, err = rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						datetimeExtractMetadata(t, b.PG, retained)
					}
					s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
						var numeric, floating any
						if err := rows.Scan(&numeric, &floating); err != nil {
							t.Fatal(err)
						}
						fixture.AssertRawValue(t, b.PG, "NUMERIC", numeric)
						fixture.AssertRawValue(t, b.PG, "FLOAT8", floating)
						if !reflect.DeepEqual([]any{fixture.Normalize(numeric), floating}, tc.rows[index]) {
							t.Fatalf("raw row %#v %#v", numeric, floating)
						}
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					// Inspect descriptors retained before Close, never closed Rows.
					datetimeExtractMetadata(t, b.PG, retained)
				})
			}
		}
	})
}
