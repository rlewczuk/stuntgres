package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestUpdateReturningParameterPhase(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,f BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,TRUE)")
	for _, tc := range []struct {
		q string
		d stuntgres.Error
	}{
		{"UPDATE t SET a=$1,f=$1 RETURNING 1", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: "21"}},
		{"UPDATE t SET a=$1,f=$1 RETURNING $1::int4", stuntgres.Error{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression.", Position: "21"}},
	} {
		_, err := db.Exec(tc.q, 2)
		typeCoreDiagnostic(t, err, tc.d)
		held, err := db.Prepare(tc.q)
		if held != nil {
			held.Close()
			t.Fatal("invalid handle")
		}
		typeCoreDiagnostic(t, err, tc.d)
	}
	rows, err := db.Query("SELECT a,f FROM t")
	queryCoreRows(t, rows, err, []string{"a", "f"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}, false)
}
