package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestQueryLimitAnalysis(t *testing.T) {
	db := queryLimitFixture(t)
	execSQL(t, db, "CREATE TABLE b(id INT,y BOOL)")
	execSQL(t, db, "CREATE TABLE empty(id INT)")
	for _, tc := range []struct {
		q, code, message, position string
		runtime                    bool
	}{
		{"SELECT id FROM b LIMIT y", "42804", "argument of LIMIT must be type bigint, not type boolean", "24", false},
		{"SELECT id FROM t LIMIT CAST(NULL AS TEXT)", "42804", "argument of LIMIT must be type bigint, not type text", "29", false},
		{"SELECT id FROM t LIMIT CAST(NULL AS BOOLEAN)", "42804", "argument of LIMIT must be type bigint, not type boolean", "29", false},
		{"SELECT id FROM t WHERE FALSE LIMIT -1", "2201W", "LIMIT must not be negative", "", true},
		{"SELECT id FROM empty OFFSET -1", "2201X", "OFFSET must not be negative", "", true},
		{"SELECT id FROM empty LIMIT -1", "2201W", "LIMIT must not be negative", "", true},
	} {
		t.Run(tc.q, func(t *testing.T) {
			want := stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.position}
			rows, err := db.Query(tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("analysis exposed cursor")
			}
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(tc.q)
			if tc.runtime {
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err = stmt.Query()
				if rows != nil {
					rows.Close()
					t.Fatal("runtime bound exposed cursor")
				}
			} else if stmt != nil {
				stmt.Close()
				t.Fatal("analysis exposed statement")
			}
			typeCoreDiagnostic(t, err, want)
		})
	}
}
