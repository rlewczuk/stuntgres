package reference_test

import (
	"database/sql"
	"math"
	"reflect"
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
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
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
					s := referenceSelect(q, []string{"extract", "date_part"}, []string{"NUMERIC", "FLOAT8"}, tc.rows)
					s.prepared = prepared
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						var err error
						retained, err = rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						datetimeExtractMetadata(t, b.pg, retained)
					}
					s.checkScan = func(t *testing.T, rows *sql.Rows, index int) {
						var numeric, floating any
						if err := rows.Scan(&numeric, &floating); err != nil {
							t.Fatal(err)
						}
						referenceAssertRawValue(t, b.pg, "NUMERIC", numeric)
						referenceAssertRawValue(t, b.pg, "FLOAT8", floating)
						if !reflect.DeepEqual([]any{referenceNormalize(numeric), floating}, tc.rows[index]) {
							t.Fatalf("raw row %#v %#v", numeric, floating)
						}
					}
					referenceRunStep(t, p.ctx, c, b.recorder, s)
					// Inspect descriptors retained before Close, never closed Rows.
					datetimeExtractMetadata(t, b.pg, retained)
				})
			}
		}
	})
}
