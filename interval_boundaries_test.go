package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"stuntgres"
	"testing"
)

func TestIntervalGenericTypmodNativeRoundTrip(t *testing.T) {
	ctx := context.Background()
	drv := stuntgres.NewDriver()
	db := openLocal(t, drv, t.Name())
	dest := openLocal(t, drv, t.Name()+"/restore")
	cases := []struct {
		mask int
		out  string
	}{{4, "1 year"}, {2, "1 year 1 mon"}, {8, "1 year 1 mon 2 days"}, {1024, "1 year 1 mon 2 days 03:00:00"}, {2048, "1 year 1 mon 2 days 03:04:00"}, {6, "1 year 1 mon"}, {1032, "1 year 1 mon 2 days 03:00:00"}, {3080, "1 year 1 mon 2 days 03:04:00"}, {3072, "1 year 1 mon 2 days 03:04:00"}}
	for i, tc := range cases {
		execSQL(t, db, fmt.Sprintf(`CREATE TABLE g%d(v "interval"(%d,2))`, i, tc.mask))
		execSQL(t, db, fmt.Sprintf("INSERT INTO g%d VALUES('13 months 2 days 3:04:05.6789')", i))
	}
	execSQL(t, db, `CREATE TABLE ivdefault(id INTEGER,v "interval"(4,2) DEFAULT '13 months')`)
	execSQL(t, db, "INSERT INTO ivdefault(id) VALUES(1)")
	execSQL(t, db, `CREATE VIEW ivgenericview AS SELECT v FROM g0`)
	var canonical bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &canonical); err != nil {
		t.Fatal(err)
	}
	for _, style := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
		execSQL(t, db, "SET IntervalStyle="+style)
		var dump bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &dump); err != nil || dump.String() != canonical.String() {
			t.Fatal(style, err, "style-dependent dump")
		}
		execSQL(t, dest, "SET IntervalStyle="+style)
		if err := stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(style, err, dump.String())
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(ctx, dest, &redump); err != nil || redump.String() != canonical.String() {
			t.Fatal(style, err, "typmod/redump changed")
		}
		execSQL(t, dest, "SET IntervalStyle=postgres")
		for i, tc := range cases {
			rows, err := dest.Query(fmt.Sprintf("SELECT v FROM g%d", i))
			if err != nil {
				t.Fatal(err)
			}
			intervalRows(t, rows, [][]any{{tc.out}})
		}
		for _, q := range []string{"SELECT v FROM ivdefault", "SELECT v FROM ivgenericview"} {
			rows, err := dest.Query(q)
			if err != nil {
				t.Fatal(err)
			}
			intervalRows(t, rows, [][]any{{"1 year"}})
		}
	}
}

func intervalPublicBoundaryError(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	var got *stuntgres.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, want)
	}
}

func TestIntervalBoundaryDiagnosticsAndAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, input, usage, code, primary, sentinel string }{
		{"iso_first", "PT1000000000000001S", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_later", "P1YT1000000000000001S", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"year_month", "1 1-2", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"year_month_year", "1 1-2", "INTERVAL YEAR", "22007", "invalid input syntax for type interval", "00:00:00"},
		{"year_month_minute", "1 1-2", "INTERVAL MINUTE TO SECOND(2)", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"precision", "2562047788:00:54.775807", "INTERVAL SECOND(2)", "22008", "interval out of range", "00:00:01"},
		{"original_iso_leading", " PT1S", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_iso_trailing", "PT1S ", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_iso_hex_leading", " P1DT0x1p0S", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_iso_tab", "\tPT1S", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_iso_newline", "PT1S\n", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_iso_nbsp", "\u00a0PT1S", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_verbose_nbsp", "\u00a01 second", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_infinity_nbsp", "\u00a0infinity", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_iso_nbsp_trailing", "PT1S\u00a0", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_verbose_nbsp_trailing", "1 second\u00a0", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_infinity_nbsp_trailing", "infinity\u00a0", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"precision_offset_positive", "9223372036854775804 microseconds", "INTERVAL SECOND(5)", "22008", "interval out of range", "00:00:01"},
		{"precision_offset_negative", "-9223372036854775804 microseconds", "INTERVAL SECOND(5)", "22008", "interval out of range", "00:00:01"},
		{"precision_offset_closest_positive", "9223372036854775803 microseconds", "INTERVAL SECOND(5)", "22008", "interval out of range", "00:00:01"},
		{"clock_signed_minimum", "-2562047788:00:54.775808", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"clock_signed_positive", "+2562047788:00:54.775808", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"clock_unsigned_overflow", "2562047788:00:54.775808", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE iv(v "+tc.usage+")")
			execSQL(t, db, "INSERT INTO iv VALUES('1 second')")
			message := tc.primary
			if tc.code != "22008" {
				message += ": \"" + tc.input + "\""
			}
			base := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: message}
			sentinel := func() {
				rows, err := db.Query("SELECT v FROM iv")
				if err != nil {
					t.Fatal(err)
				}
				intervalRows(t, rows, [][]any{{tc.sentinel}})
			}
			for _, prepared := range []bool{false, true} {
				for _, literal := range []struct{ q, pos string }{{"INSERT INTO iv VALUES('" + tc.input + "')", "23"}, {"INSERT INTO iv VALUES('1 day'),('" + tc.input + "')", "33"}} {
					var err error
					if prepared {
						stmt, e := db.Prepare(literal.q)
						err = e
						if stmt != nil {
							stmt.Close()
						}
					} else {
						_, err = db.Exec(literal.q)
					}
					want := base
					want.Position = literal.pos
					intervalPublicBoundaryError(t, err, want)
					sentinel()
				}
				for _, parameter := range []struct {
					q    string
					args []any
				}{
					{"INSERT INTO iv VALUES($1)", []any{tc.input}},
					{"INSERT INTO iv VALUES($1),($2)", []any{"1 day", tc.input}},
				} {
					q := parameter.q
					var err error
					if prepared {
						stmt, e := db.Prepare(q)
						if e != nil {
							t.Fatal(e)
						}
						_, err = stmt.Exec(parameter.args...)
						stmt.Close()
					} else {
						_, err = db.Exec(q, parameter.args...)
					}
					want:=base;if tc.code!="22008" {want.Context="unnamed portal parameter $1 = '...'";if len(parameter.args)==2{want.Context="unnamed portal parameter $2 = '...'"}}
					intervalPublicBoundaryError(t, err, want)
					sentinel()
				}
			}
			q := "SELECT INTERVAL '" + tc.input + "'"
			if tc.code == "22008" {
				precision := "2"
				if tc.usage == "INTERVAL SECOND(5)" {
					precision = "5"
				}
				q += " SECOND(" + precision + ")"
			}
			q += " FROM iv"
			for _, prepared := range []bool{false, true} {
				var err error
				if prepared {
					stmt, e := db.Prepare(q)
					err = e
					if stmt != nil {
						stmt.Close()
					}
				} else {
					_, err = db.Exec(q)
				}
				want := base
				want.Position = "17"
				intervalPublicBoundaryError(t, err, want)
				sentinel()
			}
		})
	}
}

func TestIntervalISOAlternativeExponentPublic(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
	for _, tc := range []struct{ input, out string }{{"P00021015e0", "2 years 10 mons 15 days"}, {"P00021015.0e0", "2 years 10 mons 15 days"}, {"PT001020e0", "00:10:20"}, {"PT001020.0e0", "00:10:20"}} {
		for _, prepared := range []bool{false, true} {
			for _, parameterized := range []bool{false, true} {
				execSQL(t, db, "DELETE FROM iv")
				q := "INSERT INTO iv VALUES('" + tc.input + "')"
				var args []any
				if parameterized {
					q = "INSERT INTO iv VALUES($1)"
					args = []any{tc.input}
				}
				if prepared {
					stmt, err := db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					result, err := stmt.Exec(args...)
					stmt.Close()
					if err != nil {
						t.Fatal(err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
				} else {
					result, err := db.Exec(q, args...)
					if err != nil {
						t.Fatal(err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
				}
				rows, err := db.Query("SELECT v FROM iv")
				if err != nil {
					t.Fatal(err)
				}
				intervalRows(t, rows, [][]any{{tc.out}})
			}
		}
	}
}

func TestIntervalPrecisionOverflowSessionIsolation(t *testing.T) {
	for _, tc := range []struct{ name, usage, input string }{
		{"rounded_result", "INTERVAL SECOND(2)", "2562047788:00:54.775807"},
		{"offset_positive", "INTERVAL SECOND(5)", "9223372036854775804 microseconds"},
		{"offset_negative", "INTERVAL SECOND(5)", "-9223372036854775804 microseconds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE iv(v "+tc.usage+")")
			execSQL(t, db, "INSERT INTO iv VALUES('1 second')")
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			if _, err = a.ExecContext(ctx, "SET IntervalStyle=sql_standard"); err != nil {
				t.Fatal(err)
			}
			if _, err = b.ExecContext(ctx, "SET IntervalStyle=iso_8601"); err != nil {
				t.Fatal(err)
			}
			for _, prepared := range []bool{false, true} {
				const q = "INSERT INTO iv VALUES($1),($2)"
				if prepared {
					stmt, e := a.PrepareContext(ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = stmt.ExecContext(ctx, "1 day", tc.input)
					stmt.Close()
				} else {
					_, err = a.ExecContext(ctx, q, "1 day", tc.input)
				}
				intervalPublicBoundaryError(t, err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22008", Message: "interval out of range"})
				var own, peer string
				if err = a.QueryRowContext(ctx, "SHOW IntervalStyle").Scan(&own); err != nil || own != "sql_standard" {
					t.Fatal(own, err)
				}
				if err = b.QueryRowContext(ctx, "SHOW IntervalStyle").Scan(&peer); err != nil || peer != "iso_8601" {
					t.Fatal(peer, err)
				}
				rows, err := a.QueryContext(ctx, "SELECT v FROM iv")
				if err != nil {
					t.Fatal(err)
				}
				intervalRows(t, rows, [][]any{{"0:00:01"}})
				rows, err = b.QueryContext(ctx, "SELECT v FROM iv")
				if err != nil {
					t.Fatal(err)
				}
				intervalRows(t, rows, [][]any{{"PT1S"}})
			}
		})
	}
}

func TestIntervalOriginalBoundaryAndPrecisionControls(t *testing.T) {
	for _, tc := range []struct{ name, input, usage, qualifier, out string }{
		{"verbose_leading", " 1 second", "INTERVAL", "", "00:00:01"},
		{"verbose_trailing", "1 second ", "INTERVAL", "", "00:00:01"},
		{"verbose_c_whitespace", "\t1 second\n", "INTERVAL", "", "00:00:01"},
		{"verbose_all_c_whitespace", " \t1 second\r\n\f\v", "INTERVAL", "", "00:00:01"},
		{"infinity_leading", " infinity", "INTERVAL", "", "infinity"},
		{"infinity_trailing", "infinity ", "INTERVAL", "", "infinity"},
		{"negative_infinity_c_whitespace", "\t-infinity\n", "INTERVAL", "", "-infinity"},
		{"offset_positive_neighbor", "9223372036854775802 microseconds", "INTERVAL SECOND(5)", " SECOND(5)", "2562047788:00:54.7758"},
		{"offset_negative_neighbor", "-9223372036854775803 microseconds", "INTERVAL SECOND(5)", " SECOND(5)", "-2562047788:00:54.7758"},
		{"offset_positive_multiple", "9223372036854775800 microseconds", "INTERVAL SECOND(5)", " SECOND(5)", "2562047788:00:54.7758"},
		{"offset_negative_multiple", "-9223372036854775800 microseconds", "INTERVAL SECOND(5)", " SECOND(5)", "-2562047788:00:54.7758"},
		{"unrestricted_positive", "9223372036854775804 microseconds", "INTERVAL", "", "2562047788:00:54.775804"},
		{"unrestricted_negative", "-9223372036854775804 microseconds", "INTERVAL", "", "-2562047788:00:54.775804"},
		{"precision_six_positive", "9223372036854775804 microseconds", "INTERVAL SECOND(6)", " SECOND(6)", "2562047788:00:54.775804"},
		{"precision_six_negative", "-9223372036854775804 microseconds", "INTERVAL SECOND(6)", " SECOND(6)", "-2562047788:00:54.775804"},
		{"restriction_before_offset_positive", "9223372036854775804 microseconds", "\"interval\"(8,5)", " DAY", "00:00:00"},
		{"restriction_before_offset_negative", "-9223372036854775804 microseconds", "\"interval\"(8,5)", " DAY", "00:00:00"},
		{"positive_tie", "5 microseconds", "INTERVAL SECOND(5)", " SECOND(5)", "00:00:00.00001"},
		{"negative_tie", "-5 microseconds", "INTERVAL SECOND(5)", " SECOND(5)", "-00:00:00.00001"},
		{"precision_positive_infinity", "infinity", "INTERVAL SECOND(5)", " SECOND(5)", "infinity"},
		{"precision_negative_infinity", "-infinity", "INTERVAL SECOND(5)", " SECOND(5)", "-infinity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE iv(v "+tc.usage+")")
			for _, prepared := range []bool{false, true} {
				for _, parameterized := range []bool{false, true} {
					execSQL(t, db, "DELETE FROM iv")
					q := "INSERT INTO iv VALUES('" + tc.input + "')"
					var args []any
					if parameterized {
						q = "INSERT INTO iv VALUES($1)"
						args = []any{tc.input}
					}
					if prepared {
						stmt, err := db.Prepare(q)
						if err != nil {
							t.Fatal(err)
						}
						result, err := stmt.Exec(args...)
						stmt.Close()
						if err != nil {
							t.Fatal(err)
						}
						if n, err := result.RowsAffected(); err != nil || n != 1 {
							t.Fatal(n, err)
						}
					} else {
						result, err := db.Exec(q, args...)
						if err != nil {
							t.Fatal(err)
						}
						if n, err := result.RowsAffected(); err != nil || n != 1 {
							t.Fatal(n, err)
						}
					}
					rows, err := db.Query("SELECT v FROM iv")
					if err != nil {
						t.Fatal(err)
					}
					intervalRows(t, rows, [][]any{{tc.out}})
					q = "SELECT INTERVAL '" + tc.input + "'" + tc.qualifier + " AS v FROM iv"
					if prepared {
						stmt, e := db.Prepare(q)
						if e != nil {
							t.Fatal(e)
						}
						rows, err = stmt.Query()
						stmt.Close()
					} else {
						rows, err = db.Query(q)
					}
					if err != nil {
						t.Fatal(err)
					}
					intervalRows(t, rows, [][]any{{tc.out}})
				}
			}
		})
	}
}
