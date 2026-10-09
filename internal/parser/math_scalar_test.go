package parser

import "testing"

func TestMathScalarAdmission(t *testing.T) {
	for _, query := range []string{
		"SELECT abs(1),ceil(1.0),ceiling(1.0),div(1,2),exp(1.0),factorial(5),floor(1.0)",
		"SELECT x,abs(x),x,abs(x) FROM t",
		"/*é*/ SELECT x,abs(x) FROM t",
		`SELECT "abs"(x),pg_catalog.abs(x),ABS(x) FROM t`,
		"SELECT abs(abs),ceil AS ceiling,floor FROM factorial",
		"SELECT exp(1.0),1",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
	}
	for _, query := range []string{"SELECT n FROM t WHERE abs(n)", "INSERT INTO t VALUES(abs(1))", "CREATE TABLE t(n NUMERIC DEFAULT exp(1.0))"} {
		statements, err := Parse(query)
		if err == nil || len(statements) != 0 {
			t.Fatal("widened grammar boundary", query, statements, err)
		}
	}
}
