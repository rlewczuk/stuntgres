package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowOffsetMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, suffix := range []string{" ORDER BY id", " WHERE FALSE", " LIMIT 0"} {
		var expected [][]any
		if suffix == " ORDER BY id" {
			expected = [][]any{{int64(2), nil}, {int64(3), int64(1)}, {int64(4), int64(2)}, {int64(5), int64(3)}, {int64(6), int64(4)}, {nil, int64(5)}}
		}
		s := fixture.Select("SELECT lead(id) OVER(ORDER BY id),lag(id) OVER(ORDER BY id) FROM wo"+suffix, []string{"lead", "lag"}, []string{"INT4", "INT4"}, expected)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil || len(columns) != 2 {
				t.Fatal(columns, err)
			}
			for i, c := range columns {
				scan := reflect.TypeFor[int64]()
				if b.PG {
					scan = reflect.TypeFor[int32]()
				}
				nullable, known := c.Nullable()
				length, lengthKnown := c.Length()
				precision, scale, decimalKnown := c.DecimalSize()
				if c.Name() != s.Columns[i] || c.DatabaseTypeName() != "INT4" || c.ScanType() != scan || nullable != !b.PG || known != !b.PG || length != 0 || lengthKnown || precision != 0 || scale != 0 || decimalKnown {
					t.Errorf("offset descriptor[%d]: name=%q type=%q scan=%v nullable=%v/%v length=%d/%v decimal=%d,%d/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), nullable, known, length, lengthKnown, precision, scale, decimalKnown)
				}
			}
		}
		s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
			if v != nil {
				if _, ok := v.(int64); !ok {
					t.Fatalf("INT4 raw %T %v", v, v)
				}
			}
			return false
		}
		s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
			var lead, lag sql.NullInt64
			if err := rows.Scan(&lead, &lag); err != nil {
				t.Fatal(err)
			}
			if lead.Valid != (index < 5) || lag.Valid != (index > 0) || (lead.Valid && lead.Int64 != int64(index+2)) || (lag.Valid && lag.Int64 != int64(index)) {
				t.Fatal(index, lead, lag)
			}
		}
		queryWindowRoutes(t, p, b, s)
	}
}

func TestReferenceQueryWindowOffsetMetadata(t *testing.T) {
	queryWindowOffsetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowOffsetSeed(t, p, b)
		queryWindowOffsetMetadata(t, p, b)
		for _, q := range []string{"CREATE DOMAIN wo_meta_d AS INTEGER", "CREATE TYPE wo_meta_m AS ENUM('z','a')"} {
			queryWindowRun(t, p, b, fixture.Exec(q, 0))
		}
		for _, tc := range []struct {
			expr, typ string
			value     any
		}{
			{"lead(1::smallint,0,2::bigint) OVER()", "INT8", int64(1)},
			{"lead(1.25::numeric(4,2),0,2.50::numeric(4,2)) OVER()", "NUMERIC", "1.25"},
			{"lead('abc'::varchar(3),0,'def'::varchar(3)) OVER()", "VARCHAR", "abc"},
			{"lead(1::wo_meta_d,0,2::wo_meta_d) OVER()", "INT4", int64(1)},
			{"lead(1::wo_meta_d) OVER()", "INT4", nil},
			{"lead(NULL,1,NULL) OVER()", "TEXT", nil},
			{"lead('z'::wo_meta_m,1,'a'::wo_meta_m) OVER()", "", "a"},
			{"lag(ARRAY[1,NULL::int],0) OVER()", "_INT4", "{1,NULL}"},
			{"lag('\\x00ff'::bytea,0) OVER()", "BYTEA", string([]byte{0, 255})},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				for _, suffix := range []string{"", " WHERE FALSE", " LIMIT 0"} {
					var expected [][]any
					if suffix == "" {
						expected = [][]any{{tc.value}}
					}
					s := fixture.Select("SELECT "+tc.expr+" AS x"+suffix, []string{"x"}, []string{tc.typ}, expected)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						columns, err := rows.ColumnTypes()
						if err != nil || len(columns) != 1 {
							t.Fatal(columns, err)
						}
						c := columns[0]
						nullable, known := c.Nullable()
						length, lk := c.Length()
						precision, scale, dk := c.DecimalSize()
						wantScan := reflect.TypeFor[string]()
						wantLength, wantLengthKnown := int64(0), false
						wantPrecision, wantScale, wantDecimalKnown := int64(0), int64(0), false
						switch tc.typ {
						case "INT4":
							wantScan = reflect.TypeFor[int64]()
							if b.PG {
								wantScan = reflect.TypeFor[int32]()
							}
						case "INT8":
							wantScan = reflect.TypeFor[int64]()
						case "NUMERIC":
							if b.PG {
								wantScan = reflect.TypeFor[any]()
								wantPrecision, wantScale, wantDecimalKnown = 65535, 65531, true
							}
						case "VARCHAR":
							wantLength, wantLengthKnown = math.MaxInt64, true
							if b.PG {
								wantLength = -5
							}
						case "TEXT":
							wantLength, wantLengthKnown = math.MaxInt64, true
						case "BYTEA":
							wantScan = reflect.TypeFor[[]byte]()
							if b.PG {
								wantLength, wantLengthKnown = math.MaxInt64, true
							}
						case "", "_INT4":
							if b.PG {
								wantScan = reflect.TypeFor[any]()
							}
						}
						if c.Name() != "x" || c.DatabaseTypeName() != tc.typ || c.ScanType() != wantScan || nullable != !b.PG || known != !b.PG || length != wantLength || lk != wantLengthKnown || precision != wantPrecision || scale != wantScale || dk != wantDecimalKnown {
							t.Errorf("offset descriptor %s: name=%q type=%q scan=%v nullable=%v/%v length=%d/%v decimal=%d,%d/%v; want x/%q/%v/%v,%v/%d,%v/%d,%d,%v", tc.expr, c.Name(), c.DatabaseTypeName(), c.ScanType(), nullable, known, length, lk, precision, scale, dk, tc.typ, wantScan, !b.PG, !b.PG, wantLength, wantLengthKnown, wantPrecision, wantScale, wantDecimalKnown)
						}
					}
					if tc.typ == "BYTEA" {
						s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
							bytes, ok := v.([]byte)
							if !ok || !reflect.DeepEqual(bytes, []byte{0, 255}) {
								t.Fatalf("BYTEA raw %T %v", v, v)
							}
							return true
						}
					}
					for _, prepared := range []bool{false, true} {
						s.Prepared = prepared
						if tc.typ == "" {
							fixture.EnumReferenceRunStep(t, p.Ctx, b.DB, b.Recorder, s)
						} else {
							queryWindowRun(t, p, b, s)
						}
					}
				}
			})
		}
	})
}
