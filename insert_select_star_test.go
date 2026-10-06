package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestInsertSelectStarDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		source, q string
		want      stuntgres.Error
	}{
		{"CREATE TABLE s(a INT,b INT)", "INSERT INTO t(i) SELECT * FROM s", stuntgres.Error{Code: "42601", Message: "INSERT has more expressions than target columns", Position: "25"}},
		{"CREATE TABLE s(v TEXT)", "INSERT INTO t(i) SELECT * FROM s", stuntgres.Error{Code: "42804", Message: `column "i" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "25"}},
		{"CREATE TABLE s(v TEXT)", "/*é*/ INSERT INTO t(i) SELECT * FROM s", stuntgres.Error{Code: "42804", Message: `column "i" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "31"}},
		{"CREATE TABLE s(v TEXT)", "INSERT INTO t SELECT *", stuntgres.Error{Code: "42601", Message: "SELECT * with no tables specified is not valid", Position: "22"}},
	} {
		t.Run(tc.q+tc.source, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), "insert-select-star")
			execSQL(t, db, "CREATE TABLE t(i INT)")
			execSQL(t, db, "INSERT INTO t VALUES(9)")
			execSQL(t, db, tc.source)
			_, err := db.Exec(tc.q)
			typeCoreDiagnostic(t, err, tc.want)
			stmt, err := db.Prepare(tc.q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("bad source prepared")
			}
			typeCoreDiagnostic(t, err, tc.want)
			wantTuples(t, db, "SELECT i FROM t", [][]any{{int64(9)}})
		})
	}
}
