package stuntgres_test

import (
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestQueryOrderValuesNameHint(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE d(i INT)")
	q := "INSERT INTO d VALUES(1) ORDER BY column1,colum1+0"
	want := stuntgres.Error{Code: "42703", Message: `column "colum1" does not exist`, Position: strconv.Itoa(strings.Index(q, "colum1") + 1), Hint: `Perhaps you meant to reference the column "*VALUES*.column1".`}
	_, err := db.Exec(q)
	typeCoreDiagnostic(t, err, want)
	stmt, err := db.Prepare(q)
	if stmt != nil {
		stmt.Close()
		t.Fatal("analysis failure exposed handle")
	}
	typeCoreDiagnostic(t, err, want)
	rows, err := db.Query("SELECT i FROM d")
	queryCoreRows(t, rows, err, []string{"i"}, []string{"INT4"}, [][]any{}, true)
}
