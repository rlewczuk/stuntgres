package stuntgres_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func timeDiagnostic(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	var got *stuntgres.Error
	if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
		t.Fatalf("diagnostic %#v (%v) want %#v", got, err, want)
	}
}

var timeBadInputs = []struct{ input, code, primary, hint string }{
	{"25:00", "22008", "date/time field value out of range", ""}, {"12:60", "22008", "date/time field value out of range", ""}, {"12:00:61", "22008", "date/time field value out of range", ""},
	{"24:01", "22008", "date/time field value out of range", ""}, {"24:00:00.1", "22008", "date/time field value out of range", ""}, {"23:59:60.01", "22008", "date/time field value out of range", ""},
	{"250000", "22008", "date/time field value out of range", ""}, {"049900", "22008", "date/time field value out of range", ""}, {"040599", "22008", "date/time field value out of range", ""},
	{"13:00 PM", "22008", "date/time field value out of range", ""}, {"04:05:06+16", "22009", "time zone displacement out of range", ""}, {"bad", "22007", "invalid input syntax for type time", ""},
	{"15:36:39 America/New_York", "22007", "invalid input syntax for type time", ""}, {"04:05 Asia/Kolkata", "22007", "invalid input syntax for type time", ""}, {"04:05 X0Y", "22007", "invalid input syntax for type time", ""},
	{"04:05 AM PM", "22007", "invalid input syntax for type time", ""}, {"04:05 UTC UTC", "22007", "invalid input syntax for type time", ""}, {"04:05 EDT DST", "22007", "invalid input syntax for type time", ""},
	{"04:05 MSK DST", "22007", "invalid input syntax for type time", ""}, {"04:05 Etc/UTC DST", "22007", "invalid input syntax for type time", ""}, {"allballs UTC", "22007", "invalid input syntax for type time", ""},
	{"epoch", "22007", "invalid input syntax for type time", ""}, {"today", "22007", "invalid input syntax for type time", ""}, {"infinity", "22007", "invalid input syntax for type time", ""},
	{"04:05 on Monday", "22007", "invalid input syntax for type time", ""}, {"Monday 04:05", "22007", "invalid input syntax for type time", ""},
	{"2003-07-07 04:05 Monday", "22007", "invalid input syntax for type time", ""},
	{"2003-02-29 04:05", "22008", "date/time field value out of range", ""}, {"2003-13-07 04:05", "22008", "date/time field value out of range", "Perhaps you need a different \"DateStyle\" setting."},
}

func TestTimeDiagnosticsPhasesAndAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v TIME)")
	execSQL(t, db, "INSERT INTO t VALUES('00:00')")
	param, err := db.Prepare("INSERT INTO t VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer param.Close()
	for _, tc := range timeBadInputs {
		t.Run(tc.input, func(t *testing.T) {
			want := stuntgres.Error{Code: tc.code, Message: tc.primary + ": \"" + tc.input + "\"", Hint: tc.hint, Position: "22"}
			q := "INSERT INTO t VALUES('" + tc.input + "')"
			_, err := db.Exec(q)
			timeDiagnostic(t, err, want)
			stmt, err := db.Prepare(q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed constant prepared")
			}
			timeDiagnostic(t, err, want)
			want.Position = ""
			want.Context = "unnamed portal parameter $1 = '...'"
			_, err = param.Exec(tc.input)
			timeDiagnostic(t, err, want)
			_, err = db.Exec("INSERT INTO t VALUES($1)", tc.input)
			timeDiagnostic(t, err, want)
			timeQuery(t, db, "SELECT v FROM t", []string{"v"}, []string{"TIME"}, [][]any{{"00:00:00"}})
		})
	}
	for _, input := range []string{"15:36:39 America/Bogus", "15:36:39 AMERICA/BOGUS"} {
		q := "INSERT INTO t VALUES('" + input + "')"
		want := stuntgres.Error{Code: "22023", Message: "time zone \"america/bogus\" not recognized", Position: "22"}
		_, err = db.Exec(q)
		timeDiagnostic(t, err, want)
		_, err = db.Prepare(q)
		timeDiagnostic(t, err, want)
		want.Position = ""
		want.Context = "unnamed portal parameter $1 = '...'"
		_, err = param.Exec(input)
		timeDiagnostic(t, err, want)
		_, err = db.Exec("INSERT INTO t VALUES($1)", input)
		timeDiagnostic(t, err, want)
	}
	_, err = db.Prepare("SELECT TIME 'bad' FROM t")
	timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: "invalid input syntax for type time: \"bad\"", Position: "13"})
	_, err = db.Prepare("SELECT TIME(0) '24:00:00.1' FROM t")
	timeDiagnostic(t, err, stuntgres.Error{Code: "22008", Message: "date/time field value out of range: \"24:00:00.1\"", Position: "16"})
	_, err = db.Exec(`CREATE TABLE later(a TIME,v "time"(-1))`)
	timeDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "TIME(-1) precision must not be negative", Position: "29"})
	execSQL(t, db, "CREATE TABLE later(a TIME,v TIME)")
	q := "/*é*/ INSERT INTO t VALUES('bad')"
	_, err = db.Exec(q)
	timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: "invalid input syntax for type time: \"bad\"", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'bad'")]) + 1)})
	_, err = db.Exec("INSERT INTO t VALUES('24:00'),('bad')")
	timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: "invalid input syntax for type time: \"bad\"", Position: "32"})
	_, err = db.Exec("INSERT INTO t VALUES($1),($2)", "24:00", "bad")
	timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: "invalid input syntax for type time: \"bad\"", Context: "unnamed portal parameter $2 = '...'"})
	timeQuery(t, db, "SELECT v FROM t", []string{"v"}, []string{"TIME"}, [][]any{{"00:00:00"}})
	_, err = db.Prepare("SELECT $1 FROM t WHERE v=$1")
	timeDiagnostic(t, err, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "time without time zone versus text", Position: "8"})
	_, err = db.Exec("SELECT v FROM t WHERE v=TIMESTAMP '2003-07-07'")
	timeDiagnostic(t, err, stuntgres.Error{Code: "42883", Message: "operator does not exist: time without time zone = timestamp without time zone", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "24"})
	execSQL(t, db, "CREATE TABLE nn(v TIME NOT NULL)")
	_, err = db.Exec("INSERT INTO nn VALUES(NULL)")
	timeDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: "null value in column \"v\" of relation \"nn\" violates not-null constraint", Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec("INSERT INTO t VALUES('bad')")
	timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: "invalid input syntax for type time: \"bad\"", Position: "22"})
	_, err = tx.Exec("INSERT INTO t VALUES('24:00')")
	timeDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestTimePrecisionDiagnosticsAndNotices(t *testing.T) {
	raw, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	_, conn := noticePool(t, raw.(*stuntgres.Connector), func(n stuntgres.Error) { notices = append(notices, n) })
	ctx := context.Background()
	for _, tc := range []struct{ q, code, message, position string }{
		{"CREATE TABLE bad(v TIME(-1))", "42601", "syntax error at or near \"-\"", "25"},
		{`CREATE TABLE bad(v "time"(-1))`, "22023", "TIME(-1) precision must not be negative", "20"},
		{`CREATE TABLE bad(v "time"(+1))`, "42601", "type modifiers must be simple constants or identifiers", "20"},
		{`CREATE TABLE bad(v "time"(1.5))`, "22P02", "invalid input syntax for type integer: \"1.5\"", "20"},
		{`CREATE TABLE bad(v "time"(2147483648))`, "22003", "value \"2147483648\" is out of range for type integer", "20"},
	} {
		_, err := conn.ExecContext(ctx, tc.q)
		timeDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.position})
		if tc.code != "0A000" && tc.code != "42601" || strings.Contains(tc.q, `"time"(+1)`) {
			stmt, err := conn.PrepareContext(ctx, tc.q)
			if err != nil {
				t.Fatal("utility Prepare must be semantic-effect free", err)
			}
			_, err = stmt.ExecContext(ctx)
			stmt.Close()
			timeDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.position})
		}
		noticeExec(t, conn, "CREATE TABLE bad(v TIME)")
		noticeExec(t, conn, "DROP TABLE bad")
	}
	noticeExec(t, conn, "CREATE TABLE zoned(v TIME WITH TIME ZONE)")
	timezoneExec(t, conn, "INSERT INTO zoned VALUES('04:05+02')", 1)
	timezoneRows(t, conn, "SELECT v FROM zoned", []string{"TIMETZ"}, [][]any{{"04:05:00+02"}})
	stmt, err := conn.PrepareContext(ctx, "CREATE TABLE capped(v TIME(7))")
	if err != nil || len(notices) != 0 {
		t.Fatal(err, notices)
	}
	_, err = stmt.ExecContext(ctx)
	stmt.Close()
	if err != nil {
		t.Fatal(err)
	}
	want := stuntgres.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIME(7) precision reduced to maximum allowed, 6", Position: "23"}
	if len(notices) != 2 || !reflect.DeepEqual(notices[0], want) {
		t.Fatal(notices)
	}
	want.Position = ""
	if !reflect.DeepEqual(notices[1], want) {
		t.Fatal(notices)
	}
	result, err := conn.ExecContext(ctx, "INSERT INTO capped VALUES('00:00')")
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	notices = nil
	literal, err := conn.PrepareContext(ctx, "SELECT TIME(7) 'now' FROM capped")
	if err != nil || len(notices) != 1 {
		t.Fatal(err, notices)
	}
	defer literal.Close()
	want.Position = "8"
	if !reflect.DeepEqual(notices[0], want) {
		t.Fatal(notices)
	}
	notices = nil
	for range 2 {
		var clock string
		if err = literal.QueryRowContext(ctx).Scan(&clock); err != nil {
			t.Fatal(err)
		}
	}
	if len(notices) != 0 {
		t.Fatal("warning repeated", notices)
	}
}
