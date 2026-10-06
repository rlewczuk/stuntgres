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

func TestArithmeticTruthTestContextualDiagnostics(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(i INT,b BIGINT)", 0)
	for _, populated := range []bool{false, true} {
		if populated {
			arithmeticExec(t, db, "INSERT INTO t VALUES(7,7),(NULL,NULL)", 2)
		}
		for _, predicate := range []string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"} {
			for _, operand := range []struct{ column, diagnostic string }{{"i", "integer"}, {"b", "bigint"}} {
				for _, tc := range []struct{ prefix, suffix, position string }{
					{"SELECT " + operand.column + "+1 ", " FROM t", "8"},
					{"SELECT (" + operand.column + "+1) ", " FROM t", "9"},
					{"SELECT /*é*/ " + operand.column + "+1 ", " FROM t", "14"},
					{"SELECT /*é*/ (" + operand.column + "+1) ", " FROM t", "15"},
					{"SELECT " + operand.column + " FROM t WHERE " + operand.column + "+1 ", "", "23"},
					{"SELECT " + operand.column + " FROM t WHERE (" + operand.column + "+1) ", "", "24"},
					{"SELECT " + operand.column + " FROM t WHERE /*é*/ " + operand.column + "+1 ", "", "29"},
					{"SELECT " + operand.column + " FROM t WHERE /*é*/ (" + operand.column + "+1) ", "", "30"},
					{"SELECT /*é*/ " + operand.column + " FROM t WHERE " + operand.column + "+1 ", "", "29"},
					{"SELECT /*é*/ " + operand.column + " FROM t WHERE (" + operand.column + "+1) ", "", "30"},
				} {
					query := tc.prefix + predicate + tc.suffix
					want := stuntgres.Error{Code: "42804", Message: "argument of " + predicate + " must be type boolean, not type " + operand.diagnostic, Position: tc.position}
					stmt, err := db.Prepare(query)
					if stmt != nil {
						stmt.Close()
						t.Fatal("failed truth test exposed statement", query)
					}
					arithmeticError(t, err, want)
					rows, err := db.Query(query)
					if rows != nil {
						rows.Close()
						t.Fatal("failed truth test exposed cursor", query)
					}
					arithmeticError(t, err, want)
				}
			}
		}
	}
}
