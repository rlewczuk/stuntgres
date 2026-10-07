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
				const q = "INSERT INTO iv VALUES($1),($2)"
				var err error
				if prepared {
					stmt, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = stmt.Exec("1 day", tc.input)
					stmt.Close()
				} else {
					_, err = db.Exec(q, "1 day", tc.input)
				}
				intervalPublicBoundaryError(t, err, base)
				sentinel()
			}
			if tc.code == "22008" {
				q := "SELECT INTERVAL '" + tc.input + "' SECOND(2) FROM iv"
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
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE iv(v INTERVAL SECOND(2))")
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
			_, err = stmt.ExecContext(ctx, "1 day", "2562047788:00:54.775807")
			stmt.Close()
		} else {
			_, err = a.ExecContext(ctx, q, "1 day", "2562047788:00:54.775807")
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
}
