package stuntgres_test

import (
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

// Arithmetic must not expand the independently selected null-count arguments
// or make arithmetic executable through their singleton no-FROM exception.
func TestArithmeticSelectedFunctionArgumentBoundary(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(i INT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(7)", 1)
	for _, tc := range []struct {
		query, operator string
		args            []any
	}{
		{"SELECT num_nulls(1+2)", "+", nil},
		{"SELECT num_nulls(1+2) FROM t", "+", nil},
		{"SELECT num_nulls(num_nonnulls(i+1)) FROM t", "+", nil},
		{"SELECT num_nulls(-i) FROM t", "-", nil},
		{"SELECT num_nulls(+$1)", "+", []any{7}},
		{"SELECT num_nulls(+$1) FROM t", "+", []any{7}},
		{"SELECT num_nulls(num_nonnulls(1+2))", "+", nil},
		{"SELECT num_nulls(+ +1)", "+", nil},
	} {
		want := stuntgres.Error{Code: "0A000", Message: "arithmetic in null-count arguments is unsupported", Position: strconv.Itoa(strings.Index(tc.query, tc.operator) + 1)}
		if !strings.Contains(tc.query, " FROM ") {
			want = stuntgres.Error{Code: "0A000", Message: "SELECT without FROM is unsupported", Position: "1"}
		}
		stmt, err := db.Prepare(tc.query)
		if stmt != nil {
			stmt.Close()
			t.Fatal("unsupported argument exposed statement")
		}
		arithmeticError(t, err, want)
		rows, err := db.Query(tc.query, tc.args...)
		if rows != nil {
			rows.Close()
			t.Fatal("unsupported argument exposed rows")
		}
		arithmeticError(t, err, want)
	}
	// These signed literal and nested call forms belong to the master capability;
	// retaining them does not register arithmetic inside selected function args.
	arithmeticRows(t, db, "SELECT num_nulls(+1,-2,+1.5,+0x8000000000000000,NULL)", []string{"INT4"}, [][]any{{int64(1)}})
	arithmeticRows(t, db, "SELECT num_nulls(num_nonnulls(i)) FROM t", []string{"INT4"}, [][]any{{int64(0)}})
	arithmeticRows(t, db, "SELECT i=$1,num_nulls($1) FROM t", []string{"BOOL", "INT4"}, [][]any{{false, int64(0)}}, 1)
}

func TestArithmeticSelectedFunctionComposition(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(id INT,i INT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(1,7),(2,NULL)", 2)
	arithmeticRows(t, db, "SELECT num_nulls(+1,NULL)", []string{"INT4"}, [][]any{{int64(1)}})
	arithmeticRows(t, db, "SELECT num_nonnulls(+1,NULL)", []string{"INT4"}, [][]any{{int64(1)}})
	arithmeticRows(t, db, "SELECT num_nulls(+1,NULL),num_nonnulls(+1,NULL)", []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}})
	arithmeticRows(t, db, "SELECT num_nulls(i),i+1 FROM t ORDER BY id", []string{"INT4", "INT4"}, [][]any{{int64(0), int64(8)}, {int64(1), nil}})
}
