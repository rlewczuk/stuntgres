package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestLikeBinaryParameters(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(b BYTEA)")
	execSQL(t, db, "INSERT INTO t VALUES('\\x00ff')")
	stmt, err := db.Prepare("SELECT b LIKE $1::bytea ESCAPE $2::bytea FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	pattern := []byte{0, '%'}
	rows, err := stmt.Query(pattern, []byte{'!'})
	pattern[0] = 1
	compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{true}})
	rows, err = stmt.Query([]byte{'_'}, []byte{})
	compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{false}})
	rows, err = stmt.Query([]byte{'_', '_'}, nil)
	compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{nil}})
	rows, err = stmt.Query([]byte{'%'}, []byte{0xc3, 0xa9})
	if rows != nil {
		rows.Close()
		t.Fatal("invalid binary escape exposed rows")
	}
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."})
}
