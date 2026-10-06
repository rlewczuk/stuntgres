package stuntgres_test

import (
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestArithmeticContextualDiagnostics(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(i INT)", 0)
	arithmeticExec(t, db, "CREATE TABLE d(f BOOL)", 0)
	for _, populated := range []bool{false, true} {
		if populated {
			arithmeticExec(t, db, "INSERT INTO t VALUES(7)", 1)
		}
		for _, tc := range []struct {
			q string
			d stuntgres.Error
		}{
			{"INSERT INTO d VALUES(1+2)", stuntgres.Error{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Position: "22", Hint: "You will need to rewrite or cast the expression."}},
			{"INSERT INTO d VALUES(2147483648+2)", stuntgres.Error{Code: "42804", Message: `column "f" is of type boolean but expression is of type bigint`, Position: "22", Hint: "You will need to rewrite or cast the expression."}},
			{"INSERT INTO d VALUES(/*é*/(1+2))", stuntgres.Error{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Position: "28", Hint: "You will need to rewrite or cast the expression."}},
			{"INSERT INTO d VALUES(- (2147483648+2))", stuntgres.Error{Code: "42804", Message: `column "f" is of type boolean but expression is of type bigint`, Position: "22", Hint: "You will need to rewrite or cast the expression."}},
			{"SELECT i+1 FROM t WHERE i+1", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "25"}},
			{"SELECT i+1 FROM t WHERE (i+1)", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "26"}},
			{"SELECT /*é*/ i+1 FROM t WHERE i+1", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "31"}},
			{"SELECT i FROM t WHERE +i", stuntgres.Error{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "23"}},
		} {
			_, err := db.Prepare(tc.q)
			arithmeticError(t, err, tc.d)
			if strings.HasPrefix(tc.q, "INSERT") {
				_, err = db.Exec(tc.q)
			} else {
				rows, e := db.Query(tc.q)
				err = e
				if rows != nil {
					rows.Close()
					t.Fatal("failed contextual query exposed cursor")
				}
			}
			arithmeticError(t, err, tc.d)
		}
	}
	arithmeticRows(t, db, "SELECT f FROM d", []string{"BOOL"}, [][]any{})
	err := stuntgres.Restore(context.Background(), db, strings.NewReader("CREATE TABLE d(f BOOL); INSERT INTO d VALUES(1+2)"))
	if err == nil {
		t.Fatal("incompatible arithmetic Restore accepted")
	}
	arithmeticRows(t, db, "SELECT f FROM d", []string{"BOOL"}, [][]any{})
}
