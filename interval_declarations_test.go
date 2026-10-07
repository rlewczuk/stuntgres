package stuntgres_test

import (
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestIntervalDeclarationsAndRounding(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			for i, tc := range []struct{ decl, want string }{{"INTERVAL", "1 year 1 mon 2 days 03:04:05.6789"}, {"INTERVAL YEAR", "1 year"}, {"INTERVAL MONTH", "1 year 1 mon"}, {"INTERVAL YEAR TO MONTH", "1 year 1 mon"}, {"INTERVAL DAY", "1 year 1 mon 2 days"}, {"INTERVAL HOUR", "1 year 1 mon 2 days 03:00:00"}, {"INTERVAL DAY TO HOUR", "1 year 1 mon 2 days 03:00:00"}, {"INTERVAL MINUTE", "1 year 1 mon 2 days 03:04:00"}, {"INTERVAL DAY TO MINUTE", "1 year 1 mon 2 days 03:04:00"}, {"INTERVAL HOUR TO MINUTE", "1 year 1 mon 2 days 03:04:00"}, {"INTERVAL SECOND(2)", "1 year 1 mon 2 days 03:04:05.68"}, {"INTERVAL DAY TO SECOND(2)", "1 year 1 mon 2 days 03:04:05.68"}, {"INTERVAL HOUR TO SECOND(2)", "1 year 1 mon 2 days 03:04:05.68"}, {"INTERVAL MINUTE TO SECOND(2)", "1 year 1 mon 2 days 03:04:05.68"}} {
				table := fmt.Sprintf("iv%d", i)
				q := "CREATE TABLE " + table + "(v " + tc.decl + ")"
				if prepared {
					stmt, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					_, e = stmt.Exec()
					stmt.Close()
					if e != nil {
						t.Fatal(e)
					}
				} else {
					execSQL(t, db, q)
				}
				q = "INSERT INTO " + table + " VALUES('13 months 2 days 3:04:05.6789')"
				if prepared {
					stmt, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					_, e = stmt.Exec()
					stmt.Close()
					if e != nil {
						t.Fatal(e)
					}
				} else {
					execSQL(t, db, q)
				}
				rows, e := db.Query("SELECT v FROM " + table)
				if e != nil {
					t.Fatal(e)
				}
				intervalRows(t, rows, [][]any{{tc.want}})
			}
			for p, want := range []string{"00:00:01", "00:00:01.2", "00:00:01.23", "00:00:01.235", "00:00:01.2346", "00:00:01.23457", "00:00:01.234567"} {
				q := fmt.Sprintf("CREATE TABLE p%d(v INTERVAL(%d))", p, p)
				execSQL(t, db, q)
				execSQL(t, db, fmt.Sprintf("INSERT INTO p%d VALUES('1.234567 seconds')", p))
				rows, e := db.Query(fmt.Sprintf("SELECT v FROM p%d", p))
				if e != nil {
					t.Fatal(e)
				}
				intervalRows(t, rows, [][]any{{want}})
			}
		})
	}
}
func TestIntervalPrecisionWarnings(t *testing.T) {
	drv := stuntgres.NewDriver()
	connector, e := drv.OpenConnector(t.Name())
	if e != nil {
		t.Fatal(e)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(connector.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
	stmt, e := db.Prepare("CREATE TABLE ivp(v INTERVAL(7))")
	if e != nil {
		t.Fatal(e)
	}
	defer stmt.Close()
	if len(notices) != 0 {
		t.Fatal("utility Prepare emitted notice", notices)
	}
	if _, e = stmt.Exec(); e != nil {
		t.Fatal(e)
	}
	if len(notices) != 2 {
		t.Fatal(notices)
	}
	for i, n := range notices {
		want := stuntgres.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "INTERVAL(7) precision reduced to maximum allowed, 6"}
		if i == 0 {
			want.Position = "20"
		}
		if n != want {
			t.Fatal(n, want)
		}
	}
}
