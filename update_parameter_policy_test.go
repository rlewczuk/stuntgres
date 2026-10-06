package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestUpdateCoreMixedParameterPolicyIsolation(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,f BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,TRUE)")
	_, err := db.Exec("UPDATE t SET a=$1,f=$1", 1)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: "21"})
	// UPDATE's contextual diagnostic must not widen other assignment consumers.
	for _, prepared := range []bool{false, true} {
		var err error
		if prepared {
			stmt, e := db.Prepare("INSERT INTO t VALUES($1,$1)")
			err = e
			if stmt != nil {
				stmt.Close()
				t.Fatal("mixed INSERT parameter prepared")
			}
		} else {
			_, err = db.Exec("INSERT INTO t VALUES($1,$1)", 1)
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "parameter reused with different destination types"})
	}
	wantTuples(t, db, "SELECT a,f FROM t", [][]any{{int64(1), true}})
}
