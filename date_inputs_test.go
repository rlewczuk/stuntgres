package stuntgres_test

import (
	"context"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

func TestDateBoundValuesPreparedQueriesAndComparisons(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE d(v DATE,w DATE)")
	ins, err := db.Prepare("INSERT INTO d VALUES($1,$1)")
	if err != nil {
		t.Fatal(err)
	}
	defer ins.Close()
	bytes := []byte("2000-02-29")
	for _, value := range []any{"epoch", bytes, time.Date(2000, 1, 1, 0, 1, 0, 0, time.FixedZone("east", 14*3600)), time.Date(1999, 12, 31, 23, 59, 0, 0, time.FixedZone("west", -12*3600)), time.Date(0, 2, 29, 23, 0, 0, 0, time.UTC), nil} {
		r, err := ins.Exec(value)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	bytes[0] = '9'
	want := []any{"0001-02-29 BC", "1970-01-01", "1999-12-31", "2000-01-01", "2000-02-29", nil}
	rows, err := db.Query("SELECT v FROM d ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, rows, want)
	held, err := db.Prepare("SELECT v FROM d WHERE v=$1 ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, value := range []any{"2000-02-29", "epoch", nil, "2000-02-29"} {
		rows, err := held.Query(value)
		if err != nil {
			t.Fatal(err)
		}
		var want []any
		if value != nil {
			text := value.(string)
			if text == "epoch" {
				text = "1970-01-01"
			}
			want = []any{text}
		}
		dateCheckRows(t, rows, want)
	}
	for _, tc := range []struct {
		op   string
		want bool
	}{{"=", false}, {"<>", true}, {"!=", true}, {"<", true}, {"<=", true}, {">", false}, {">=", false}} {
		for _, prepared := range []bool{false, true} {
			q := "SELECT v" + tc.op + "DATE '2000-03-01',DATE '2000-02-28'" + tc.op + "v,v=w FROM d WHERE v=$1"
			var a, b, c any
			if prepared {
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				err = stmt.QueryRow("2000-02-29").Scan(&a, &b, &c)
				stmt.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else if err := db.QueryRow(q, "2000-02-29").Scan(&a, &b, &c); err != nil {
				t.Fatal(err)
			}
			if a != tc.want || b != tc.want || c != true {
				t.Fatal(tc.op, a, b, c)
			}
		}
	}
	var unknown any
	if err := db.QueryRow("SELECT v=NULL FROM d WHERE v='2000-02-29'").Scan(&unknown); err != nil || unknown != nil {
		t.Fatal(unknown, err)
	}
	_, err = db.Prepare("SELECT $1 FROM d WHERE v=$1")
	timeDiagnostic(t, err, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "date versus text", Position: "8"})
	for _, tc := range []struct{ literal, typ string }{{"TIME '00:00'", "time without time zone"}} {
		_, err := db.Query("SELECT v FROM d WHERE v=" + tc.literal)
		timeDiagnostic(t, err, stuntgres.Error{Code: "42883", Message: "operator does not exist: date = " + tc.typ, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "24"})
	}
	for _, prepared := range []bool{false, true} {
		q := "SELECT v=TIMESTAMP '2000-02-29 00:00',TIMESTAMP '2000-02-29 00:00'=v FROM d WHERE v=DATE '2000-02-29'"
		var left, right bool
		if prepared {
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			err = stmt.QueryRow().Scan(&left, &right)
			stmt.Close()
			if err != nil {
				t.Fatal(err)
			}
		} else if err := db.QueryRow(q).Scan(&left, &right); err != nil {
			t.Fatal(err)
		}
		if !left || !right {
			t.Fatal("DATE/TIMESTAMP midnight comparison", left, right)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ins.ExecContext(ctx, "2000-01-01"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := held.QueryContext(ctx, "2000-01-01"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDateGregorianOrdering(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE d(v DATE)")
	want := []any{"-infinity", "4714-11-24 BC", "0002-12-31 BC", "0001-01-01 BC", "0001-12-31 BC", "0001-01-01", "1900-02-28", "1900-03-01", "1999-12-31", "2000-01-01", "2000-02-28", "2000-02-29", "2000-03-01", "5874897-12-31", "infinity", nil}
	for i := len(want) - 1; i >= 0; i-- {
		if _, err := db.Exec("INSERT INTO d VALUES($1)", want[i]); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Query("SELECT v FROM d ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, rows, want)
	reverse := make([]any, len(want))
	for i := range want {
		reverse[i] = want[len(want)-1-i]
	}
	rows, err = db.Query("SELECT v FROM d ORDER BY v DESC")
	if err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, rows, reverse)
}

func TestDateCivilInputOracleRegressions(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE d(v DATE)")
	execSQL(t, db, "INSERT INTO d VALUES('2000-01-01')")
	for _, tc := range []struct{ input, want string }{
		{"2001--01-01", "2001-01-01"}, {"1999-01-08 24:00", "1999-01-08"}, {"1999-01-08 250000", "1999-01-08"}, {"1999-01-08 049900", "1999-01-08"}, {"1999-01-08 040599", "1999-01-08"}, {"1999-01-08 23:59:60", "1999-01-08"}, {"1999-01-08 23:59:59.9999999", "1999-01-08"}, {"1999-01-08 23:59:59-08", "1999-01-08"}, {"J2451187.5", "1999-01-08"}, {"1999.366", "2000-01-01"}, {"2000.366", "2000-12-31"}, {"J0", "4714-11-24 BC"},
	} {
		for _, prepared := range []bool{false, true} {
			q := "SELECT DATE '" + tc.input + "' FROM d"
			if prepared {
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := stmt.Query()
				if err != nil {
					t.Fatal(err)
				}
				dateCheckRows(t, rows, []any{tc.want})
				stmt.Close()
			} else {
				rows, err := db.Query(q)
				if err != nil {
					t.Fatal(err)
				}
				dateCheckRows(t, rows, []any{tc.want})
			}
		}
	}
	rows, err := db.Query(`SELECT "date" '1999-01-08' FROM d`)
	if err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, rows, []any{"1999-01-08"})
	_, err = db.Query("SELECT DATE '1999-01-08 04:05 America/Bogus' FROM d")
	timeDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: `time zone "america/bogus" not recognized`, Position: "13"})
	for _, prepared := range []bool{false, true} {
		if prepared {
			stmt, err := db.Prepare("INSERT INTO d VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			_, err = stmt.Exec("1999-01-08 04:05 America/Bogus")
			stmt.Close()
			timeDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: `time zone "america/bogus" not recognized`, Context: "unnamed portal parameter $1 = '...'"})
		} else {
			_, err := db.Exec("INSERT INTO d VALUES($1)", "1999-01-08 04:05 America/Bogus")
			timeDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: `time zone "america/bogus" not recognized`, Context: "unnamed portal parameter $1 = '...'"})
		}
	}
}

func TestDateRepeatedSeparatorsContextualAndBound(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE d(v DATE)")
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"INSERT INTO d VALUES('2001--01-01')", "INSERT INTO d VALUES($1)"} {
			var args []any
			if q == "INSERT INTO d VALUES($1)" {
				args = []any{"2001--01-01"}
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
		}
	}
	rows, err := db.Query("SELECT v FROM d WHERE v='2001--01-01'")
	if err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, rows, []any{"2001-01-01", "2001-01-01", "2001-01-01", "2001-01-01"})
}
