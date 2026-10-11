package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Expectations come from Table 9.32 and REL_18_0 date.c/timestamp.c, not native output.
type datetimeArithmeticCase struct {
	name, left, op, right, leftType, rightType, result, text string
}

var datetimeArithmeticCases = []datetimeArithmeticCase{
	{"date_days", "DATE '2024-02-28'", "+", "1::INTEGER", "DATE", "INTEGER", "DATE", "2024-02-29"},
	{"days_date", "1::INTEGER", "+", "DATE '2024-02-28'", "INTEGER", "DATE", "DATE", "2024-02-29"},
	{"date_minus_days", "DATE '2024-03-01'", "-", "1::INTEGER", "DATE", "INTEGER", "DATE", "2024-02-29"},
	{"date_difference", "DATE '2024-03-01'", "-", "DATE '2024-02-28'", "DATE", "DATE", "INT4", "2"},
	{"date_interval", "DATE '2024-01-31'", "+", "INTERVAL '1 month'", "DATE", "INTERVAL", "TIMESTAMP", "2024-02-29 00:00:00"},
	{"interval_date", "INTERVAL '1 month'", "+", "DATE '2024-01-31'", "INTERVAL", "DATE", "TIMESTAMP", "2024-02-29 00:00:00"},
	{"date_minus_interval", "DATE '2024-03-31'", "-", "INTERVAL '1 month'", "DATE", "INTERVAL", "TIMESTAMP", "2024-02-29 00:00:00"},
	{"date_time", "DATE '2024-02-29'", "+", "TIME '24:00'", "DATE", "TIME", "TIMESTAMP", "2024-03-01 00:00:00"},
	{"time_date", "TIME '24:00'", "+", "DATE '2024-02-29'", "TIME", "DATE", "TIMESTAMP", "2024-03-01 00:00:00"},
	{"date_timetz", "DATE '2024-01-01'", "+", "TIMETZ '12:00+02'", "DATE", "TIMETZ", "TIMESTAMPTZ", "2024-01-01 10:00:00+00"},
	{"timetz_date", "TIMETZ '12:00+02'", "+", "DATE '2024-01-01'", "TIMETZ", "DATE", "TIMESTAMPTZ", "2024-01-01 10:00:00+00"},
	{"timestamp_interval", "TIMESTAMP '2024-01-31 12:00'", "+", "INTERVAL '1 month'", "TIMESTAMP", "INTERVAL", "TIMESTAMP", "2024-02-29 12:00:00"},
	{"interval_timestamp", "INTERVAL '1 month'", "+", "TIMESTAMP '2024-01-31 12:00'", "INTERVAL", "TIMESTAMP", "TIMESTAMP", "2024-02-29 12:00:00"},
	{"timestamp_minus_interval", "TIMESTAMP '2024-03-31 12:00'", "-", "INTERVAL '1 month'", "TIMESTAMP", "INTERVAL", "TIMESTAMP", "2024-02-29 12:00:00"},
	{"timestamp_difference", "TIMESTAMP '2024-01-03 03:00'", "-", "TIMESTAMP '2024-01-01 01:00'", "TIMESTAMP", "TIMESTAMP", "INTERVAL", "2 days 02:00:00"},
	{"zoned_interval", "TIMESTAMPTZ '2024-01-01 12:00+00'", "+", "INTERVAL '1 day'", "TIMESTAMPTZ", "INTERVAL", "TIMESTAMPTZ", "2024-01-02 12:00:00+00"},
	{"interval_zoned", "INTERVAL '1 day'", "+", "TIMESTAMPTZ '2024-01-01 12:00+00'", "INTERVAL", "TIMESTAMPTZ", "TIMESTAMPTZ", "2024-01-02 12:00:00+00"},
	{"zoned_minus_interval", "TIMESTAMPTZ '2024-01-02 12:00+00'", "-", "INTERVAL '1 day'", "TIMESTAMPTZ", "INTERVAL", "TIMESTAMPTZ", "2024-01-01 12:00:00+00"},
	{"zoned_difference", "TIMESTAMPTZ '2024-01-03 03:00+00'", "-", "TIMESTAMPTZ '2024-01-01 01:00+00'", "TIMESTAMPTZ", "TIMESTAMPTZ", "INTERVAL", "2 days 02:00:00"},
	{"time_interval", "TIME '23:00'", "+", "INTERVAL '3 hours'", "TIME", "INTERVAL", "TIME", "02:00:00"},
	{"interval_time", "INTERVAL '3 hours'", "+", "TIME '23:00'", "INTERVAL", "TIME", "TIME", "02:00:00"},
	{"time_minus_interval", "TIME '01:00'", "-", "INTERVAL '3 hours'", "TIME", "INTERVAL", "TIME", "22:00:00"},
	{"time_difference", "TIME '01:00'", "-", "TIME '23:00'", "TIME", "TIME", "INTERVAL", "-22:00:00"},
	{"timetz_interval", "TIMETZ '23:00+02'", "+", "INTERVAL '3 hours'", "TIMETZ", "INTERVAL", "TIMETZ", "02:00:00+02"},
	{"interval_timetz", "INTERVAL '3 hours'", "+", "TIMETZ '23:00+02'", "INTERVAL", "TIMETZ", "TIMETZ", "02:00:00+02"},
	{"timetz_minus_interval", "TIMETZ '01:00+02'", "-", "INTERVAL '3 hours'", "TIMETZ", "INTERVAL", "TIMETZ", "22:00:00+02"},
	{"interval_sum", "INTERVAL '1 month'", "+", "INTERVAL '30 days'", "INTERVAL", "INTERVAL", "INTERVAL", "1 mon 30 days"},
	{"interval_difference", "INTERVAL '1 day'", "-", "INTERVAL '1 hour'", "INTERVAL", "INTERVAL", "INTERVAL", "1 day -01:00:00"},
	{"interval_negate", "", "-", "INTERVAL '1 month 2 days 3 hours'", "", "INTERVAL", "INTERVAL", "-1 mons -2 days -03:00:00"},
	{"interval_factor", "INTERVAL '1 hour'", "*", "3.5::DOUBLE PRECISION", "INTERVAL", "DOUBLE PRECISION", "INTERVAL", "03:30:00"},
	{"factor_interval", "3.5::DOUBLE PRECISION", "*", "INTERVAL '1 hour'", "DOUBLE PRECISION", "INTERVAL", "INTERVAL", "03:30:00"},
	{"interval_divisor", "INTERVAL '1 hour'", "/", "1.5::DOUBLE PRECISION", "INTERVAL", "DOUBLE PRECISION", "INTERVAL", "00:40:00"},
}

func datetimeArithmeticScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle='ISO,MDY'", "SET IntervalStyle='postgres'"} {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, backend, conn)
		})
	}
}

func datetimeArithmeticBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Prepared(s))
}

func datetimeArithmeticMetadata(t *testing.T, pg bool, meta []*sql.ColumnType) {
	t.Helper()
	for _, m := range meta {
		want := reflect.TypeFor[string]()
		switch m.DatabaseTypeName() {
		case "DATE", "TIME", "TIMETZ":
			if pg {
				want = reflect.TypeFor[time.Time]()
			}
		case "TIMESTAMP", "TIMESTAMPTZ":
			want = reflect.TypeFor[time.Time]()
		case "INTERVAL":
			if pg {
				want = reflect.TypeFor[any]()
			}
		case "INT4":
			want = reflect.TypeFor[int64]()
			if pg {
				want = reflect.TypeFor[int32]()
			}
		case "BOOL":
			want = reflect.TypeFor[bool]()
		}
		if m.ScanType() != want {
			t.Fatalf("%s ScanType=%v want=%v", m.Name(), m.ScanType(), want)
		}
		length, lengthKnown := m.Length()
		wantLength, wantLengthKnown := int64(0), m.DatabaseTypeName() == "TEXT"
		if wantLengthKnown {
			wantLength = 9223372036854775807
		}
		if length != wantLength || lengthKnown != wantLengthKnown {
			t.Fatal("computed length", m.Name(), length, lengthKnown)
		}
		if precision, scale, ok := m.DecimalSize(); precision != 0 || scale != 0 || ok {
			t.Fatal("computed decimal precision", m.Name(), precision, scale, ok)
		}
		if nullable, ok := m.Nullable(); nullable || ok {
			t.Fatal("computed nullability must be unknown", m.Name(), nullable, ok)
		}
	}
}

func datetimeArithmeticRaw(t *testing.T, pg bool, typ, text string) any {
	t.Helper()
	if typ == "INT4" {
		if text == "2" {
			return int64(2)
		}
		t.Fatal("missing independently specified integer", text)
	}
	if typ == "INTERVAL" {
		return text
	}
	if typ == "DATE" && !pg || (typ == "TIME" || typ == "TIMETZ") && !pg {
		return text
	}
	layout := "2006-01-02 15:04:05.999999"
	switch typ {
	case "DATE":
		layout = "2006-01-02"
	case "TIME":
		layout = "15:04:05.999999"
	case "TIMETZ":
		layout = "15:04:05.999999Z07"
		switch strings.Count(text, ":") {
		case 3:
			layout = "15:04:05.999999Z07:00"
		case 4:
			layout = "15:04:05.999999Z07:00:00"
		}
	case "TIMESTAMPTZ":
		layout = "2006-01-02 15:04:05.999999Z07"
	}
	v, err := time.Parse(layout, text)
	if err != nil {
		t.Fatal(typ, text, err)
	}
	return v.UTC()
}

func datetimeArithmeticValue(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, expression, typ, text string) {
	t.Helper()
	q := "SELECT (" + expression + ") AS result,(" + expression + ")::TEXT AS rendered"
	want := datetimeArithmeticRaw(t, b.PG, typ, text)
	for _, prepared := range []bool{false, true} {
		s := fixture.Select(q, []string{"result", "rendered"}, []string{typ, "TEXT"}, [][]any{{want, text}})
		s.Prepared = prepared
		var meta []*sql.ColumnType
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			var err error
			meta, err = rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			datetimeArithmeticMetadata(t, b.PG, meta)
		}
		s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
			var rendered string
			switch {
			case typ == "INT4":
				var v int64
				if err := rows.Scan(&v, &rendered); err != nil || v != want {
					t.Fatal(v, err)
				}
			case typ == "TIMESTAMP" || typ == "TIMESTAMPTZ" || b.PG && (typ == "DATE" || typ == "TIME" || typ == "TIMETZ"):
				var v time.Time
				if err := rows.Scan(&v, &rendered); err != nil || !v.Equal(want.(time.Time)) {
					t.Fatal(v, err)
				}
			default:
				var v string
				if err := rows.Scan(&v, &rendered); err != nil || v != text {
					t.Fatal(v, err)
				}
			}
			if rendered != text {
				t.Fatal(rendered, text)
			}
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		datetimeArithmeticMetadata(t, b.PG, meta)
		if len(meta) != 2 || meta[0].Name() != "result" || meta[0].DatabaseTypeName() != typ {
			t.Fatal("retained metadata", meta)
		}
	}
}

func TestReferenceDatetimeArithmeticOverloads(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range datetimeArithmeticCases {
			t.Run(tc.name, func(t *testing.T) {
				datetimeArithmeticValue(t, p, b, c, tc.left+" "+tc.op+" "+tc.right, tc.result, tc.text)
			})
		}
	})
}

func TestReferenceDatetimeArithmeticMetadataAndNull(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range datetimeArithmeticCases {
			t.Run(tc.name, func(t *testing.T) {
				variants := []string{tc.left + " " + tc.op + " NULL::" + tc.rightType}
				if tc.leftType != "" {
					variants = append(variants, "NULL::"+tc.leftType+" "+tc.op+" "+tc.right, "NULL::"+tc.leftType+" "+tc.op+" NULL::"+tc.rightType)
				}
				for i, expr := range variants {
					for _, empty := range []bool{false, true} {
						q := "SELECT (" + expr + ") AS result"
						want := [][]any{{nil}}
						if empty {
							q += " WHERE FALSE"
							want = nil
						}
						s := fixture.Select(q, []string{"result"}, []string{tc.result}, want)
						var meta []*sql.ColumnType
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
							var err error
							meta, err = rows.ColumnTypes()
							if err != nil {
								t.Fatal(err)
							}
							datetimeArithmeticMetadata(t, b.PG, meta)
						}
						s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
							var value sql.NullString
							if err := rows.Scan(&value); err != nil || value.Valid {
								t.Fatal("typed NULL scan", value, err)
							}
						}
						t.Run(fmt.Sprintf("null%d_empty%v", i, empty), func(t *testing.T) {
							datetimeArithmeticBoth(t, p, b, c, s)
							datetimeArithmeticMetadata(t, b.PG, meta)
							if len(meta) != 1 || meta[0].Name() != "result" || meta[0].DatabaseTypeName() != tc.result {
								t.Fatal("retained NULL/empty descriptor", meta)
							}
						})
					}
				}
			})
		}
		datetimeArithmeticBoth(t, p, b, c, fixture.Select("SELECT NULL::INTERVAL/0 AS result", []string{"result"}, []string{"INTERVAL"}, [][]any{{nil}}))
	})
}
