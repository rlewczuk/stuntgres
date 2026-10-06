package stuntgres_test

import (
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestInsertSelectValuesOrderSuggestions(t *testing.T) {
	for _, tc := range []struct{ values, key, name, hint string }{
		{"(1)", "column2", "column2", `Perhaps you meant to reference the column "*VALUES*.column1".`},
		{"(1,2)", "column3", "column3", `Perhaps you meant to reference the column "*VALUES*.column1" or the column "*VALUES*.column2".`},
		{"(1,2,3)", "column4", "column4", ""},
		{"(1)", "zzzzzzz", "zzzzzzz", ""},
		{"(1,2,3,4,5,6,7,8,9,10)", "column99", "column99", `Perhaps you meant to reference the column "*VALUES*.column9".`},
		{"(1)", `"cólumn2"`, "cólumn2", `Perhaps you meant to reference the column "*VALUES*.column1".`},
		{"(1)", `"Column2"`, "Column2", `Perhaps you meant to reference the column "*VALUES*.column1".`},
	} {
		q := "INSERT INTO t VALUES" + tc.values + " ORDER BY " + tc.key
		t.Run(q, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), "insert-select-order-hint-"+q)
			execSQL(t, db, "CREATE TABLE t(i INT)")
			execSQL(t, db, "INSERT INTO t VALUES(9)")
			want := stuntgres.Error{Code: "42703", Message: `column "` + tc.name + `" does not exist`, Hint: tc.hint, Position: strconv.Itoa(strings.Index(q, tc.key) + 1)}
			_, err := db.Exec(q)
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed prepare exposed handle")
			}
			typeCoreDiagnostic(t, err, want)
			wantTuples(t, db, "SELECT i FROM t", [][]any{{int64(9)}})
		})
	}
}
