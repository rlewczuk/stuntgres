package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestLikeDiagnosticsAndHeldEscapes(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(i INTEGER,v TEXT,b BYTEA)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'a%b','\\x61')")
	hint := "No operator matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT i LIKE 'x' FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer ~~ unknown", Hint: hint, Position: "10"}},
		{"SELECT i NOT ILIKE 'x' FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer !~~* unknown", Hint: hint, Position: "10"}},
		{"SELECT /*é*/ i LIKE 'x' FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer ~~ unknown", Hint: hint, Position: "16"}},
		{"SELECT b ILIKE b FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: bytea ~~* bytea", Hint: hint, Position: "10"}},
		{"SELECT v LIKE 'x' ESCAPE 1 FROM t", stuntgres.Error{Code: "42883", Message: "function pg_catalog.like_escape(unknown, integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "10"}},
	} {
		rows, err := db.Query(tc.q)
		if rows != nil {
			rows.Close()
			t.Fatal("failed query exposed cursor")
		}
		typeCoreDiagnostic(t, err, tc.want)
		stmt, err := db.Prepare(tc.q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("failed prepare exposed handle")
		}
		typeCoreDiagnostic(t, err, tc.want)
	}
	stmt, err := db.Prepare("SELECT v LIKE $1 ESCAPE $2 FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err := stmt.Query("a!%b", "ab")
	if rows != nil {
		rows.Close()
		t.Fatal("failed execute exposed cursor")
	}
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."})
	for _, tc := range []struct {
		escape any
		want   any
	}{{"!", true}, {nil, nil}, {"!", true}} {
		rows, err := stmt.Query("a!%b", tc.escape)
		compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{tc.want}})
	}
}
