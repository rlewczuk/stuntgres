package stuntgres_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestIntervalISOUnderflowPublic(t *testing.T) {
	for _, input := range []string{"PT1e-400S", "P1DT1e-400S", "PT5e-324S", "P1DT5e-324S", "PT-1e-400S", "P1DT-5e-324S"} {
		t.Run(input, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
			execSQL(t, db, "INSERT INTO iv VALUES('1 second')")
			base := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: "invalid input syntax for type interval: \"" + input + "\""}
			sentinel := func() {
				rows, err := db.Query("SELECT v FROM iv")
				if err != nil {
					t.Fatal(err)
				}
				intervalRows(t, rows, [][]any{{"00:00:01"}})
			}
			for _, prepared := range []bool{false, true} {
				for _, literal := range []struct{ q, pos string }{{"SELECT INTERVAL '" + input + "' FROM iv", "17"}, {"INSERT INTO iv VALUES('" + input + "')", "23"}, {"INSERT INTO iv VALUES('1 day'),('" + input + "')", "33"}} {
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
					{"INSERT INTO iv VALUES($1)", []any{input}},
					{"INSERT INTO iv VALUES($1),($2)", []any{"1 day", input}},
				} {
					var err error
					if prepared {
						stmt, e := db.Prepare(parameter.q)
						if e != nil {
							t.Fatal(e)
						}
						_, err = stmt.Exec(parameter.args...)
						stmt.Close()
					} else {
						_, err = db.Exec(parameter.q, parameter.args...)
					}
					want:=base;want.Context="unnamed portal parameter $1 = '...'";if len(parameter.args)==2{want.Context="unnamed portal parameter $2 = '...'"};intervalPublicBoundaryError(t, err, want)
					sentinel()
				}
			}
		})
	}
}

func TestIntervalISOUnderflowZeroControls(t *testing.T) {
	for _, tc := range []struct{ input, out string }{{"PT0e-400S", "00:00:00"}, {"PT-0e-400S", "00:00:00"}, {"PT1e-300S", "00:00:00"}, {"P1DT0e-400S", "1 day"}, {"P1DT1e-300S", "1 day"}} {
		t.Run(tc.input, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
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
						r, err := stmt.Exec(args...)
						stmt.Close()
						if err != nil {
							t.Fatal(err)
						}
						if n, err := r.RowsAffected(); err != nil || n != 1 {
							t.Fatal(n, err)
						}
					} else {
						r, err := db.Exec(q, args...)
						if err != nil {
							t.Fatal(err)
						}
						if n, err := r.RowsAffected(); err != nil || n != 1 {
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
		})
	}
}

func TestIntervalISOUnderflowSessionIsolation(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
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
	for _, tc := range []struct{ input, code, primary string }{
		{"P1DT1e-400S", "22007", "invalid input syntax for type interval"},
		{"P1DT0x1pS", "22007", "invalid input syntax for type interval"},
		{"P1DT0x1p50S", "22015", "interval field value out of range"},
		{"P1DT0x1p-1075S", "22007", "invalid input syntax for type interval"},
		{"P1DT-NaNS", "22015", "interval field value out of range"},
		{"P1DT-InfS", "22015", "interval field value out of range"},
		{" PT1S", "22007", "invalid input syntax for type interval"},
		{"PT1S ", "22007", "invalid input syntax for type interval"},
		{"\u00a01 second", "22007", "invalid input syntax for type interval"},
	} {
		for _, prepared := range []bool{false, true} {
			const q = "INSERT INTO iv VALUES($1),($2)"
			if prepared {
				stmt, e := a.PrepareContext(ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				_, err = stmt.ExecContext(ctx, "PT0x1p0S", tc.input)
				stmt.Close()
			} else {
				_, err = a.ExecContext(ctx, q, "PT0x1p0S", tc.input)
			}
			intervalPublicBoundaryError(t, err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.primary + ": \"" + tc.input + "\"",Context:"unnamed portal parameter $2 = '...'"})
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
}

func TestIntervalISOHexadecimalPublic(t *testing.T) {
	for _, tc := range []struct{ input, out string }{
		{"PT0x1p0S", "00:00:01"}, {"P1DT0x1p0S", "1 day 00:00:01"},
		{"PT-0x1.8p1S", "-00:00:03"}, {"P1DT-0x1.8p1S", "1 day -00:00:03"},
		{"PT0x1.8S", "00:00:01.5"}, {"P1DT0x1.8S", "1 day 00:00:01.5"},
		{"PT0x.8p0S", "00:00:00.5"}, {"P1DT0x.8p0S", "1 day 00:00:00.5"},
		{"PT0X1P0S", "00:00:01"}, {"P1DT0X1P0S", "1 day 00:00:01"},
		{"PT0X1.8P+1S", "00:00:03"}, {"P1DT0X1.8P+1S", "1 day 00:00:03"},
		{"PT0x1S", "00:00:01"}, {"P1DT0x1S", "1 day 00:00:01"},
		{"PT0xep0S", "00:00:14"}, {"P1DT0xep0S", "1 day 00:00:14"},
		{"PT0x.8S", "00:00:00.5"}, {"P1DT0x.8S", "1 day 00:00:00.5"},
		{"PT-0x.8p0S", "-00:00:00.5"}, {"P1DT-0x.8p0S", "1 day -00:00:00.5"},
		{"PT0x1.p0S", "00:00:01"}, {"P1DT0x1.p0S", "1 day 00:00:01"},
		{"PT0x1.S", "00:00:01"}, {"P1DT0x1.S", "1 day 00:00:01"},
		{"PT0x1p-1074S", "00:00:00"}, {"P1DT0x1p-1074S", "1 day"},
		{"PT0x0p-4000S", "00:00:00"}, {"P1DT0x0p-4000S", "1 day"},
		{"PT0x0p-99999S", "00:00:00"}, {"P1DT0x0p-99999S", "1 day"},
		{"P0x1p0Y", "1 year"}, {"P0x1p0M", "1 mon"}, {"P0x1p0D", "1 day"},
		{"P1Y0x1p0M", "1 year 1 mon"}, {"P0x1p0", "1 year"}, {"PT0x1p0", "01:00:00"},
		{"P1-0x1p0-1", "1 year 1 mon 1 day"}, {"P1-1-0x1p0", "1 year 1 mon 1 day"},
		{"PT1:0x1p0:1", "01:01:01"}, {"PT1:1:0x1p0", "01:01:01"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
			for _, prepared := range []bool{false, true} {
				for _, parameterized := range []bool{false, true} {
					execSQL(t, db, "DELETE FROM iv")
					q := "INSERT INTO iv VALUES('" + tc.input + "')"
					var args []any
					if parameterized {
						q = "INSERT INTO iv VALUES($1)"
						args = []any{tc.input}
					}
					var result sql.Result
					var err error
					if prepared {
						stmt, e := db.Prepare(q)
						if e != nil {
							t.Fatal(e)
						}
						result, err = stmt.Exec(args...)
						stmt.Close()
					} else {
						result, err = db.Exec(q, args...)
					}
					if err != nil {
						t.Fatal(err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
					rows, err := db.Query("SELECT v FROM iv")
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.ColumnTypes()
					if err != nil || len(columns) != 1 {
						rows.Close()
						t.Fatal(columns, err)
					}
					if columns[0].DatabaseTypeName() != "INTERVAL" || columns[0].ScanType().String() != "string" {
						rows.Close()
						t.Fatal(columns[0])
					}
					if _, _, ok := columns[0].DecimalSize(); ok {
						rows.Close()
						t.Fatal("interval has decimal metadata")
					}
					if _, ok := columns[0].Length(); ok {
						rows.Close()
						t.Fatal("interval has length metadata")
					}
					intervalRows(t, rows, [][]any{{tc.out}})
					q = "SELECT INTERVAL '" + tc.input + "' AS v FROM iv"
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

func TestIntervalISONumberDiagnosticsPublic(t *testing.T) {
	for _, tc := range []struct{ number, code, primary string }{
		{"0x1p", "22007", "invalid input syntax for type interval"},
		{"0x1p+", "22007", "invalid input syntax for type interval"},
		{"0x", "22007", "invalid input syntax for type interval"},
		{"0x.p0", "22007", "invalid input syntax for type interval"},
		{"+0x1p0", "22007", "invalid input syntax for type interval"},
		{"0x1p1024", "22007", "invalid input syntax for type interval"},
		{"0x1p-1075", "22007", "invalid input syntax for type interval"},
		{"0x1.8p-1074", "22007", "invalid input syntax for type interval"},
		{"0x1p50", "22015", "interval field value out of range"},
		{"0x1p49", "22015", "interval field value out of range"},
		{"1000000000000001e+", "22015", "interval field value out of range"},
		{"-NaN", "22015", "interval field value out of range"},
		{"-Inf", "22015", "interval field value out of range"},
		{"-Infinity", "22015", "interval field value out of range"},
		{"-NaN(foo)", "22015", "interval field value out of range"},
		{"NaN", "22007", "invalid input syntax for type interval"},
		{"Inf", "22007", "invalid input syntax for type interval"},
	} {
		for _, prefix := range []string{"PT", "P1DT"} {
			input := prefix + tc.number + "S"
			t.Run(input, func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
				execSQL(t, db, "INSERT INTO iv VALUES('1 second')")
				base := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.primary + ": \"" + input + "\""}
				sentinel := func() {
					rows, err := db.Query("SELECT v FROM iv")
					if err != nil {
						t.Fatal(err)
					}
					intervalRows(t, rows, [][]any{{"00:00:01"}})
				}
				for _, prepared := range []bool{false, true} {
					for _, literal := range []struct{ q, pos string }{
						{"SELECT INTERVAL '" + input + "' FROM iv", "17"},
						{"INSERT INTO iv VALUES('" + input + "')", "23"},
						{"INSERT INTO iv VALUES('PT0x1p0S'),('" + input + "')", "36"},
					} {
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
						{"INSERT INTO iv VALUES($1)", []any{input}},
						{"INSERT INTO iv VALUES($1),($2)", []any{"PT0x1p0S", input}},
					} {
						var err error
						if prepared {
							stmt, e := db.Prepare(parameter.q)
							if e != nil {
								t.Fatal(e)
							}
							_, err = stmt.Exec(parameter.args...)
							stmt.Close()
						} else {
							_, err = db.Exec(parameter.q, parameter.args...)
						}
						want:=base;want.Context="unnamed portal parameter $1 = '...'";if len(parameter.args)==2{want.Context="unnamed portal parameter $2 = '...'"};intervalPublicBoundaryError(t, err, want)
						sentinel()
					}
				}
			})
		}
	}
}
