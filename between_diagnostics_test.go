package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestBetweenDiagnostics(t *testing.T) {
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "populated", true: "empty"}[empty], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), "between-diagnostics")
			execSQL(t, db, "CREATE TABLE t(i INTEGER)")
			if !empty {
				execSQL(t, db, "INSERT INTO t VALUES(NULL),(0)")
			}
			for _, tc := range []struct {
				q    string
				want stuntgres.Error
				args []any
			}{
				{"SELECT i BETWEEN TRUE AND 3 FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer >= boolean", Position: "10", Hint: hint}, nil},
				{"SELECT i BETWEEN 1 AND TRUE FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer <= boolean", Position: "10", Hint: hint}, nil},
				{"SELECT i NOT BETWEEN TRUE AND 3 FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer < boolean", Position: "10", Hint: hint}, nil},
				{"SELECT i NOT BETWEEN 1 AND TRUE FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer > boolean", Position: "10", Hint: hint}, nil},
				{"SELECT i BETWEEN SYMMETRIC 1 AND TRUE FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer <= boolean", Position: "10", Hint: hint}, nil},
				{"SELECT i NOT BETWEEN SYMMETRIC 1 AND TRUE FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer > boolean", Position: "10", Hint: hint}, nil},
				{"SELECT /*é*/ i BETWEEN TRUE AND 3 FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer >= boolean", Position: "16", Hint: hint}, nil},
				{"SELECT i BETWEEN 'oops' AND 3 FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "18"}, nil},
				{"SELECT i BETWEEN '2147483648' AND 3 FROM t", stuntgres.Error{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "18"}, nil},
				{"SELECT i BETWEEN 1 AND 'oops' FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "24"}, nil},
				{"SELECT $1,i BETWEEN $1 AND 3 FROM t", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}, []any{2}},
				{"SELECT i FROM t WHERE i BETWEEN 1 AND TRUE", stuntgres.Error{Code: "42883", Message: "operator does not exist: integer <= boolean", Hint: hint, Position: "25"}, nil},
			} {
				rows, err := db.Query(tc.q, tc.args...)
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
			stmt, err := db.Prepare("SELECT i BETWEEN $1 AND $2 FROM t ORDER BY i")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err := stmt.Query("oops", 3)
			if rows != nil {
				rows.Close()
				t.Fatal("bad input exposed cursor")
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
			want := [][]any{}
			if !empty {
				want = [][]any{{true}, {nil}}
			}
			rows, err = stmt.Query(0, 3)
			compareOpsRows(t, rows, err, []string{"BOOL"}, want)
		})
	}
}
