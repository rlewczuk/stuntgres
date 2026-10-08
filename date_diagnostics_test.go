package stuntgres_test

import (
	"stuntgres"
	"testing"
	"time"
)

var dateBadInputs = []struct{ input, code, primary, hint string }{
	{"bad", "22007", "invalid input syntax for type date", ""},
	{"2001-02-29", "22008", "date/time field value out of range", ""},
	{"1900-02-29", "22008", "date/time field value out of range", ""},
	{"1500-02-29", "22008", "date/time field value out of range", ""},
	{"2100-02-29", "22008", "date/time field value out of range", ""},
	{"0002-02-29 BC", "22008", "date/time field value out of range", ""},
	{"0000-01-01", "22008", "date/time field value out of range", ""},
	{"2001-13-01", "22008", "date/time field value out of range", "Perhaps you need a different \"DateStyle\" setting."},
	{"2001-01-32", "22008", "date/time field value out of range", "Perhaps you need a different \"DateStyle\" setting."},
	{"4714-11-23 BC", "22008", "date out of range", ""},
	{"5874898-01-01", "22008", "date out of range", ""},
	{"1999-01-08 25:00", "22008", "date/time field value out of range", ""},
	{"1999-01-08 24:00:00.1", "22008", "date/time field value out of range", ""},
	{"J2451187.5 J2451187.5", "22007", "invalid input syntax for type date", ""},
	{"J2451187.5 1999-01-08", "22007", "invalid input syntax for type date", ""},
	{"J-1", "22007", "invalid input syntax for type date", ""},
	{"J2147483647", "22008", "date out of range", ""},
	{"999999999999999999-01-01", "22008", "date/time field value out of range", ""},
	{"1999-01-08 04:05 UTC UTC", "22007", "invalid input syntax for type date", ""},
	{"1999-01-08 04:05 EDT DST", "22007", "invalid input syntax for type date", ""},
	{"1999-01-08 04:05+16", "22009", "time zone displacement out of range", ""},
	{"1999-01-08 04:05+01:99", "22009", "time zone displacement out of range", ""},
	{"2001-02-29 25:00", "22008", "date/time field value out of range", ""},
	{"5874898-01-01 25:00", "22008", "date/time field value out of range", ""},
	{"2001-13-01 04:05+16", "22009", "time zone displacement out of range", ""},
}

func TestDateDiagnosticsPhasesAndAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE d(v DATE)")
	execSQL(t, db, "INSERT INTO d VALUES('2000-01-01')")
	param, err := db.Prepare("INSERT INTO d VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer param.Close()
	for _, tc := range dateBadInputs {
		t.Run(tc.input, func(t *testing.T) {
			want := stuntgres.Error{Code: tc.code, Message: tc.primary + ": \"" + tc.input + "\"", Hint: tc.hint, Position: "22"}
			q := "INSERT INTO d VALUES('" + tc.input + "')"
			_, err := db.Exec(q)
			timeDiagnostic(t, err, want)
			stmt, err := db.Prepare(q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid literal prepared successfully")
			}
			timeDiagnostic(t, err, want)
			want.Position = ""
			want.Context = "unnamed portal parameter $1 = '...'"
			_, err = param.Exec(tc.input)
			timeDiagnostic(t, err, want)
			_, err = db.Exec("INSERT INTO d VALUES($1)", tc.input)
			timeDiagnostic(t, err, want)
			rows, err := db.Query("SELECT v FROM d")
			if err != nil {
				t.Fatal(err)
			}
			dateCheckRows(t, rows, []any{"2000-01-01"})
		})
	}
	execSQL(t, db, "CREATE TABLE empty_date(v DATE)")
	_, err = db.Prepare("SELECT DATE 'bad' FROM empty_date")
	timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "13"})
	_, err = db.Exec("/*é*/ INSERT INTO d VALUES('bad')")
	timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "28"})
	_, err = db.Exec("INSERT INTO d VALUES('2000-02-29'),('bad')")
	timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "37"})
	_, err = db.Exec("INSERT INTO d VALUES($1),($2)", "2000-02-29", "bad")
	timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Context: "unnamed portal parameter $2 = '...'"})
	rows, err := db.Query("SELECT v FROM d")
	if err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, rows, []any{"2000-01-01"})
}

func TestDateModifierDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, typ := range []string{"date(0)", "date(3)", `"date"(3)`} {
		_, err := db.Exec("CREATE TABLE bad(v " + typ + ")")
		timeDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "20"})
		stmt, err := db.Prepare("CREATE TABLE bad(v " + typ + ")")
		if err != nil {
			t.Fatal("utility Prepare rejected modifier", err)
		}
		_, err = stmt.Exec()
		stmt.Close()
		timeDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "20"})
		execSQL(t, db, "CREATE TABLE bad(v DATE)")
		execSQL(t, db, "DROP TABLE bad")
	}
	execSQL(t, db, "CREATE TABLE d(v DATE)")
	_, err := db.Prepare("SELECT DATE(3) '1999-01-08' FROM d")
	timeDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "8"})
	_, err = db.Query("SELECT DATE(3) '1999-01-08' FROM d")
	timeDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "8"})
}

func TestDateTimeParameterRangeDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE d(v DATE)")
	execSQL(t, db, "INSERT INTO d VALUES('2000-01-01')")
	for _, tc := range []struct {
		name, message string
		input         time.Time
	}{
		{"upperUTC", `date out of range: "5874898-01-01 00:00:00Z"`, time.Date(5874898, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"belowMinimumUTC", `date out of range: "4714-11-23 00:00:00Z BC"`, time.Date(-4713, 11, 23, 0, 0, 0, 0, time.UTC)},
		{"yearFieldOverflow", `date/time field value out of range: "2147483648-01-01 00:00:00Z"`, time.Date(2147483648, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"bcYearFieldOverflow", `date/time field value out of range: "2147483648-01-01 00:00:00Z BC"`, time.Date(-2147483647, 1, 1, 0, 0, 0, 0, time.UTC)},
		// DATE ignores these valid clock/offset fields, but a failed import's
		// diagnostic must still preserve the original temporal argument text.
		{"upperOffset", `date out of range: "5874898-01-01 04:05:06.123456789+02:03:04"`, time.Date(5874898, 1, 1, 4, 5, 6, 123456789, time.FixedZone("east", 7384))},
		{"belowMinimumOffset", `date out of range: "4714-11-23 04:05:06.123456789-02:03:04 BC"`, time.Date(-4713, 11, 23, 4, 5, 6, 123456789, time.FixedZone("west", -7384))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"direct", "prepared"} {
				t.Run(mode, func(t *testing.T) {
					for _, statement := range []struct{ name, query string }{
						{"singleRow", "INSERT INTO d VALUES($1)"},
						{"laterRow", "INSERT INTO d VALUES('2000-02-29'),($1)"},
					} {
						t.Run(statement.name, func(t *testing.T) {
							var err error
							if mode == "prepared" {
								stmt, prepareErr := db.Prepare(statement.query)
								if prepareErr != nil {
									t.Fatal("range parameter failed during Prepare", prepareErr)
								}
								defer stmt.Close()
								_, err = stmt.Exec(tc.input)
							} else {
								_, err = db.Exec(statement.query, tc.input)
							}
							timeDiagnostic(t, err, stuntgres.Error{Code: "22008", Message: tc.message, Context: "unnamed portal parameter $1 = '...'"})
							rows, err := db.Query("SELECT v FROM d ORDER BY v")
							if err != nil {
								t.Fatal(err)
							}
							dateCheckRows(t, rows, []any{"2000-01-01"})
						})
					}
				})
			}
		})
	}
}
