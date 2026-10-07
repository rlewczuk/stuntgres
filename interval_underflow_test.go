package stuntgres_test

import (
	"context"
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
					intervalPublicBoundaryError(t, err, base)
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
	for _, prepared := range []bool{false, true} {
		const q = "INSERT INTO iv VALUES($1),($2)"
		if prepared {
			stmt, e := a.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			_, err = stmt.ExecContext(ctx, "1 day", "P1DT1e-400S")
			stmt.Close()
		} else {
			_, err = a.ExecContext(ctx, q, "1 day", "P1DT1e-400S")
		}
		intervalPublicBoundaryError(t, err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: "invalid input syntax for type interval: \"P1DT1e-400S\""})
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
