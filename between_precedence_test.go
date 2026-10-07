package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestBetweenComparisonPrecedence(t *testing.T) {
	_, db := betweenSetup(t)
	for _, tc := range []struct{ query, position string }{
		{"SELECT TRUE BETWEEN 1<2 AND 2<3 FROM t WHERE id=1", "13"},
		{"SELECT id FROM t WHERE (TRUE BETWEEN 1<2 AND 2<3) AND id=1", "30"},
	} {
		want := stuntgres.Error{Code: "42883", Message: "operator does not exist: boolean <= integer", Position: tc.position, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}
		rows, err := db.Query(tc.query)
		if rows != nil {
			rows.Close()
			t.Fatal("invalid comparison exposed rows")
		}
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(tc.query)
		if stmt != nil {
			stmt.Close()
			t.Fatal("invalid comparison prepared")
		}
		typeCoreDiagnostic(t, err, want)
	}
}
