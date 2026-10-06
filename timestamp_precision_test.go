package stuntgres_test

import (
	"strconv"
	"stuntgres"
	"testing"
	"time"
)

func TestTimestampPrecisionScannerDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE anchor(v TIMESTAMP)`)
	execSQL(t, db, `INSERT INTO anchor VALUES('epoch')`)
	for _, head := range []struct {
		sql      string
		position int
	}{
		{"TIMESTAMP", 30}, {`"timestamp"`, 32}, {`U&"t\0069mestamp"`, 38},
	} {
		for _, tc := range []struct {
			spelling, token, primary string
			delta                    int
		}{
			{"1.5foo", "1.5foo", "trailing junk after numeric literal", 0},
			{".5foo", ".5foo", "trailing junk after numeric literal", 0},
			{"1.5e+", "1.5e+", "trailing junk after numeric literal", 0},
			{"1..2", "..", "syntax error", 1},
			{".5e-", ".5e-", "trailing junk after numeric literal", 0},
			{"1.5e", "1.5e", "trailing junk after numeric literal", 0},
			{"1.5e+3foo", "1.5e+3foo", "trailing junk after numeric literal", 0},
			{"1.5_", "1.5_", "trailing junk after numeric literal", 0},
			{"1.5e+3_", "1.5e+3_", "trailing junk after numeric literal", 0},
			{".5", ".5", "syntax error", 0},
			{"1.5e+3", "1.5e+3", "syntax error", 0},
			{"1e+2", "1e+2", "syntax error", 0},
			{"1e+", "1e+", "trailing junk after numeric literal", 0},
			{"1efoo", "1efoo", "trailing junk after numeric literal", 0},
			{"1.5e3foo", "1.5e3foo", "trailing junk after numeric literal", 0},
			{"1.", "1.", "syntax error", 0},
		} {
			// Complete generic numeric tokens are tested below at semantic execution.
			if head.sql != "TIMESTAMP" && tc.primary == "syntax error" && tc.delta == 0 {
				continue
			}
			for _, table := range []string{"bad", `"é"`} {
				q := "CREATE TABLE " + table + "(v " + head.sql + "(" + tc.spelling + "))"
				want := stuntgres.Error{Code: "42601", Message: tc.primary + ` at or near "` + tc.token + `"`, Position: strconv.Itoa(head.position + tc.delta)}
				_, err := db.Exec(q)
				typeCoreDiagnostic(t, err, want)
				stmt, err := db.Prepare(q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed prepare published handle", q)
				}
				typeCoreDiagnostic(t, err, want)
				// Creating the same name proves the failed declaration published no relation.
				execSQL(t, db, "CREATE TABLE "+table+"(v TIMESTAMP)")
				execSQL(t, db, "DROP TABLE "+table)
			}
			if head.sql == "TIMESTAMP" {
				q := "INSERT INTO anchor VALUES('2000-01-01'),(TIMESTAMP(" + tc.spelling + ") 'epoch')"
				want := stuntgres.Error{Code: "42601", Message: tc.primary + ` at or near "` + tc.token + `"`, Position: strconv.Itoa(52 + tc.delta)}
				_, err := db.Exec(q)
				typeCoreDiagnostic(t, err, want)
				stmt, err := db.Prepare(q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed literal prepare published handle")
				}
				typeCoreDiagnostic(t, err, want)
				timestampQuery(t, db, "SELECT v FROM anchor", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
			}
		}
	}
	for _, sign := range []string{"+", "-"} {
		q := "CREATE TABLE bad(v TIMESTAMP(" + sign + "1))"
		want := stuntgres.Error{Code: "42601", Message: `syntax error at or near "` + sign + `"`, Position: "30"}
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("signed keyword precision published handle")
		}
		typeCoreDiagnostic(t, err, want)
	}
	for _, tc := range []struct {
		head     string
		position int
	}{{`"timestamp"`, 33}, {`U&"t\0069mestamp"`, 39}} {
		for _, number := range []string{"+1.5foo", "-1..2"} {
			want := stuntgres.Error{Code: "42601", Message: `trailing junk after numeric literal at or near "1.5foo"`, Position: strconv.Itoa(tc.position)}
			if number == "-1..2" {
				want.Message = `syntax error at or near ".."`
				want.Position = strconv.Itoa(tc.position + 1)
			}
			q := "CREATE TABLE bad(v " + tc.head + "(" + number + "))"
			_, err := db.Exec(q)
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("signed junk precision published handle")
			}
			typeCoreDiagnostic(t, err, want)
		}
	}
	for _, q := range []string{`CREATE TABLE signed(v "timestamp"(3))`, `CREATE TABLE unicode_signed(v U&"t\0069mestamp"(3))`} {
		execSQL(t, db, q)
	}
	execSQL(t, db, "INSERT INTO anchor VALUES(TIMESTAMP(3) '2000-01-01 00:00:00.1236')")
	timestampQuery(t, db, "SELECT v FROM anchor ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 124000000, time.UTC)}})
}
