package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestArithmeticWidthsAndAssignments(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1,2147483647,9223372036854775807),(2,-2147483648,-9223372036854775808)", 2)
	for _, tc := range []struct {
		expr, where, typ string
		want             int64
	}{{"i+0", "id=1", "INT4", 2147483647}, {"b+0", "id=1", "INT8", 9223372036854775807}, {"i-0", "id=2", "INT4", -2147483648}, {"b-0", "id=2", "INT8", -9223372036854775808}, {"i*1", "id=2", "INT4", -2147483648}, {"b*1", "id=2", "INT8", -9223372036854775808}, {"i/1", "id=1", "INT4", 2147483647}, {"b/1", "id=1", "INT8", 9223372036854775807}, {"i/(b/b*-1)", "id=2", "INT8", 2147483648}, {"9007199254740993*1", "id=1", "INT8", 9007199254740993}, {"9007199254740993/1", "id=1", "INT8", 9007199254740993}} {
		arithmeticRows(t, db, "SELECT "+tc.expr+" FROM t WHERE "+tc.where, []string{tc.typ}, [][]any{{tc.want}})
	}
	arithmeticExec(t, db, "CREATE TABLE dest(i INT,b BIGINT)", 0)
	stmt, err := db.Prepare("INSERT INTO dest VALUES($1+7,$2+2147483648)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	r, err := stmt.Exec(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	arithmeticRows(t, db, "SELECT i,b FROM dest", []string{"INT4", "INT8"}, [][]any{{int64(8), int64(2147483649)}})
	for _, tc := range []struct{ q, primary string }{{"INSERT INTO dest VALUES(1,2147483647+1)", "integer out of range"}, {"INSERT INTO dest VALUES(2147483647+2147483648,1)", "integer out of range"}} {
		_, err := db.Exec(tc.q)
		arithmeticError(t, err, stuntgres.Error{Code: "22003", Message: tc.primary})
	}
	_, err = stmt.Exec("oops", 1)
	arithmeticError(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
	arithmeticRows(t, db, "SELECT i,b FROM dest", []string{"INT4", "INT8"}, [][]any{{int64(8), int64(2147483649)}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rows, err := db.QueryContext(ctx, "SELECT i+1 FROM t"); err == nil || rows != nil {
		t.Fatal(rows, err)
	}
}
