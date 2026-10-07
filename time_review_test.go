package stuntgres_test

import (
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

// Expectations follow REL_18_0 DecodeTimeOnly and parse_target.c, not native output.
func TestTimeReviewClockPrecedence(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TIME)")
	execSQL(t, db, "INSERT INTO t VALUES('00:00')")
	for _, tc := range []struct{ input, code, message, hint string }{
		{"25:00 UTC UTC", "22007", "invalid input syntax for type time: \"25:00 UTC UTC\"", ""},
		{"UTC UTC 25:00", "22007", "invalid input syntax for type time: \"UTC UTC 25:00\"", ""},
		{"24:00:00.1 America/Bogus", "22023", "time zone \"america/bogus\" not recognized", ""},
		{"America/Bogus 24:00:00.1", "22007", "invalid input syntax for type time: \"America/Bogus 24:00:00.1\"", ""},
		{"2003-13-07 25:00", "22008", "date/time field value out of range: \"2003-13-07 25:00\"", "Perhaps you need a different \"DateStyle\" setting."},
		{"25:00 2003-13-07", "22007", "invalid input syntax for type time: \"25:00 2003-13-07\"", ""},
		{"25:00 AM PM", "22007", "invalid input syntax for type time: \"25:00 AM PM\"", ""},
		{"AM PM 25:00", "22007", "invalid input syntax for type time: \"AM PM 25:00\"", ""},
		{"12:60 America/Bogus", "22008", "date/time field value out of range: \"12:60 America/Bogus\"", ""},
		{"America/Bogus 12:60", "22007", "invalid input syntax for type time: \"America/Bogus 12:60\"", ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			for _, later := range []bool{false, true} {
				q := "INSERT INTO t VALUES('" + tc.input + "')"
				parameter := "INSERT INTO t VALUES($1)"
				if later {
					q = "INSERT INTO t VALUES('24:00'),('" + tc.input + "')"
					parameter = "INSERT INTO t VALUES('24:00'),($1)"
				}
				want := stuntgres.Error{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: strconv.Itoa(strings.Index(q, "'"+tc.input) + 1)}
				_, err := db.Exec(q)
				timeDiagnostic(t, err, want)
				stmt, err := db.Prepare(q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("invalid constant prepared")
				}
				timeDiagnostic(t, err, want)
				want.Position = ""
				_, err = db.Exec(parameter, tc.input)
				timeDiagnostic(t, err, want)
				stmt, err = db.Prepare(parameter)
				if err != nil {
					t.Fatal(err)
				}
				_, err = stmt.Exec(tc.input)
				stmt.Close()
				timeDiagnostic(t, err, want)
				timeQuery(t, db, "SELECT v FROM t", []string{"v"}, []string{"TIME"}, [][]any{{"00:00:00"}})
			}
		})
	}
}

func TestTimeReviewAssignmentIdentity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []struct {
		name, declaration, destination, literal, source, sentinel, databaseType string
		value                                                                   any
	}{
		{"ints", "INTEGER", "integer", "TIME '04:05'", "time without time zone", "1", "INT4", int64(1)},
		{"stamps", "TIMESTAMP", "timestamp without time zone", "TIME '04:05'", "time without time zone", "'2003-07-07'", "TIMESTAMP", time.Date(2003, 7, 7, 0, 0, 0, 0, time.UTC)},
		{"stampints", "INTEGER", "integer", "TIMESTAMP '2003-07-07'", "timestamp without time zone", "1", "INT4", int64(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			execSQL(t, db, "CREATE TABLE "+tc.name+"(v "+tc.declaration+")")
			execSQL(t, db, "INSERT INTO "+tc.name+" VALUES("+tc.sentinel+")")
			for _, later := range []bool{false, true} {
				q := "INSERT INTO " + tc.name + " VALUES("
				if later {
					q += tc.sentinel + "),("
				}
				q += tc.literal + ")"
				want := stuntgres.Error{Code: "42804", Message: "column \"v\" is of type " + tc.destination + " but expression is of type " + tc.source, Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(strings.Index(q, tc.literal) + strings.Index(tc.literal, "'") + 1)}
				_, err := db.Exec(q)
				timeDiagnostic(t, err, want)
				stmt, err := db.Prepare(q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("incompatible assignment prepared")
				}
				timeDiagnostic(t, err, want)
				timeQuery(t, db, "SELECT v FROM "+tc.name, []string{"v"}, []string{tc.databaseType}, [][]any{{tc.value}})
			}
		})
	}
}

func TestTimezoneTimestampToTimeAssignment(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE times(v TIME)")
	for _, prepared := range []bool{false, true} {
		q := "INSERT INTO times VALUES(TIMESTAMP '2003-07-07 04:05:06')"
		if prepared {
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			result, err := stmt.Exec()
			stmt.Close()
			if err != nil {
				t.Fatal(err)
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				t.Fatal(n, err)
			}
		} else {
			result, err := db.Exec(q)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				t.Fatal(n, err)
			}
		}
	}
	timeQuery(t, db, "SELECT v FROM times", []string{"v"}, []string{"TIME"}, [][]any{{"04:05:06"}, {"04:05:06"}})
}
