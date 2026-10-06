package stuntgres_test

import (
	"context"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestArithmeticCombinedOperatorBoundary(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(i INT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(7)", 1)
	want := stuntgres.Error{Code: "42883", Message: "operator does not exist: integer %- integer", Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}
	_, err := db.Prepare("SELECT i%-1 FROM t")
	arithmeticError(t, err, want)
	rows, err := db.Query("SELECT i%-1 FROM t")
	if rows != nil {
		rows.Close()
		t.Fatal("combined operator exposed cursor")
	}
	arithmeticError(t, err, want)
	q := "INSERT INTO t VALUES(1),(7%-1)"
	want.Position = strconv.Itoa(strings.Index(q, "%") + 1)
	_, err = db.Prepare(q)
	arithmeticError(t, err, want)
	_, err = db.Exec(q)
	arithmeticError(t, err, want)
	arithmeticRows(t, db, "SELECT i FROM t", []string{"INT4"}, [][]any{{int64(7)}})
	script := "CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(1); INSERT INTO replacement VALUES(7%-1)"
	err = stuntgres.Restore(context.Background(), db, strings.NewReader(script))
	want.Position = strconv.Itoa(strings.Index(script, "%") + 1)
	arithmeticError(t, err, want)
	arithmeticRows(t, db, "SELECT i FROM t", []string{"INT4"}, [][]any{{int64(7)}})
	arithmeticRows(t, db, "SELECT i% -1 FROM t", []string{"INT4"}, [][]any{{int64(0)}})
}
