package stuntgres_test

import (
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestArithmeticUnsupportedBoundaries(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(s SMALLINT,n NUMERIC,r REAL,d DOUBLE PRECISION)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(7,+1.5,7,7)", 1)
	arithmeticRows(t, db, "SELECT n FROM t", []string{"NUMERIC"}, [][]any{{"1.5"}})
	for _, tc := range []struct{ expr, primary, pos string }{{"s+1", "arithmetic for smallint operands is unsupported", "9"}, {"+n", "arithmetic for numeric operands is unsupported", "8"}, {"r/2", "arithmetic for real operands is unsupported", "9"}, {"d*2", "arithmetic for double precision operands is unsupported", "9"}, {"+$1", "integer arithmetic requires known integer or bigint operands", "8"}, {"-$1", "integer arithmetic requires known integer or bigint operands", "8"}, {"$1+$2", "integer arithmetic requires known integer or bigint operands", "10"}, {"NULL+NULL", "integer arithmetic requires known integer or bigint operands", "12"}, {"$1%$2", "integer arithmetic requires known integer or bigint operands", "10"}} {
		_, err := db.Prepare("SELECT " + tc.expr + " FROM t")
		arithmeticError(t, err, stuntgres.Error{Code: "0A000", Message: tc.primary, Position: tc.pos})
	}
	for _, tc := range []struct {
		expr, pos string
		args      []any
	}{{"+$1", "8", []any{7}}, {"-$1", "8", []any{7}}, {"$1+$2", "10", []any{7, 2}}, {"$1%$2", "10", []any{7, 2}}} {
		rows, err := db.Query("SELECT "+tc.expr+" FROM t", tc.args...)
		if rows != nil {
			rows.Close()
			t.Fatal("unknown-only arithmetic exposed cursor")
		}
		arithmeticError(t, err, stuntgres.Error{Code: "0A000", Message: "integer arithmetic requires known integer or bigint operands", Position: tc.pos})
	}
}

func TestArithmeticSignedNumericLiteralCompatibility(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE n(v NUMERIC)", 0)
	arithmeticExec(t, db, "INSERT INTO n VALUES(+1.5)", 1)
	stmt, err := db.Prepare("INSERT INTO n VALUES(+9223372036854775808)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err := stmt.Exec(); err != nil {
		t.Fatal(err)
	}
	arithmeticRows(t, db, "SELECT v FROM n ORDER BY v", []string{"NUMERIC"}, [][]any{{"1.5"}, {"9223372036854775808"}})
	if err := stuntgres.Restore(context.Background(), db, strings.NewReader("CREATE TABLE n(v NUMERIC); INSERT INTO n VALUES(+1.5),(+9223372036854775808)")); err != nil {
		t.Fatal(err)
	}
	arithmeticRows(t, db, "SELECT v FROM n ORDER BY v", []string{"NUMERIC"}, [][]any{{"1.5"}, {"9223372036854775808"}})
}
