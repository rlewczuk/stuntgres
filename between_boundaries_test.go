package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestBetweenSyntaxDiagnostics(t *testing.T) {
	_, db := betweenSetup(t)
	for _, tc := range []struct{ q, message, pos string }{
		{"SELECT v BETWEEN 1 3 FROM t", `syntax error at or near "3"`, "20"},
		{"SELECT v BETWEEN AND 3 FROM t", `syntax error at or near "AND"`, "18"},
		{"SELECT v BETWEEN 1 AND", `syntax error at end of input`, "23"},
	} {
		rows, err := db.Query(tc.q)
		if rows != nil {
			rows.Close()
			t.Fatal("syntax exposed rows")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: tc.message, Position: tc.pos})
		stmt, err := db.Prepare(tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("syntax prepared")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: tc.message, Position: tc.pos})
	}
}

func TestBetweenMixedParameterErrorsAndTemporalReuse(t *testing.T) {
	_, db := betweenSetup(t)
	execSQL(t, db, "CREATE TABLE m(i INTEGER,s TEXT)")
	execSQL(t, db, "INSERT INTO m VALUES(1,'3')")
	for _, tc := range []struct{ form, op string }{
		{"BETWEEN", "<="}, {"NOT BETWEEN", ">"}, {"BETWEEN SYMMETRIC", "<="}, {"NOT BETWEEN SYMMETRIC", ">"},
	} {
		_, err := db.Query("SELECT $1 "+tc.form+" i AND s FROM m", 2)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42883", Message: "operator does not exist: integer " + tc.op + " text", Position: "11", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
		stmt, err := db.Prepare("SELECT $1 " + tc.form + " i AND s FROM m")
		if stmt != nil {
			stmt.Close()
			t.Fatal("incompatible parameter prepared")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42883", Message: "operator does not exist: integer " + tc.op + " text", Position: "11", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
	}
	for _, typ := range []string{"TIME(3)", "TIMESTAMP(3)"} {
		input := "12:00:00.123"
		lower := "11:00:00.123"
		upper := "13:00:00.123"
		if typ == "TIMESTAMP(3)" {
			input = "2000-01-02 12:00:00.123"
			lower = "2000-01-01 12:00:00.123"
			upper = "2000-01-03 12:00:00.123"
		}
		for _, form := range betweenForms {
			stmt, err := db.Prepare("SELECT '" + input + "' " + form + " " + typ + " '" + lower + "' AND " + typ + " '" + upper + "' FROM t WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			for range 3 {
				rows, err := stmt.Query()
				compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{form == "BETWEEN" || form == "BETWEEN SYMMETRIC"}})
			}
			stmt.Close()
		}
	}
}
