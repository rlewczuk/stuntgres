package parser

import "testing"

func TestMathConstantsSignatures(t *testing.T) {
	for _, query := range []string{
		"SELECT cbrt(-8),degrees('0.5'),radians(45),pi(),erf(1),erfc(1),gamma(6),exp('1'::float8)",
		"/*é*/ SELECT pg_catalog.pi(),PI(),\"pi\"(),gamma($1)",
		`SELECT "PI"(1),"CBRT"(1),"DEGREES"(1),"RADIANS"(1),"ERF"(1),"ERFC"(1),"GAMMA"(1),"EXP"(1)`,
		"SELECT pi(),cbrt(d),pi() FROM t",
		"CREATE TABLE t(d DOUBLE PRECISION,p DOUBLE PRECISION GENERATED ALWAYS AS (pi()) STORED)",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
	}
	for _, query := range []string{"CREATE TABLE t(d DOUBLE PRECISION DEFAULT pi())", "CREATE TABLE t(d DOUBLE PRECISION DEFAULT gamma(1))"} {
		statements, err := Parse(query)
		if err == nil || len(statements) != 0 {
			t.Fatal("widened default boundary", query, statements, err)
		}
	}
}
