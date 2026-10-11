package parser

import "testing"

func TestMathGcdLcmParser(t *testing.T) {
	for _, query := range []string{
		"SELECT gcd(12,18),lcm(12,18)",
		"SELECT gcd(lcm(12,18),12)",
		"SELECT gcd($1,18),lcm($1,18)",
		`/*é*/ SELECT "gcd"(a,b),pg_catalog.lcm(a,b),GCD(a,b) FROM t`,
		`SELECT "GCD"(12,18),pg_catalog."LCM"(12,18)`,
		"SELECT gcd(gcd,lcm),lcm AS gcd FROM gcd",
		"SELECT x,gcd(x,x),x,lcm(x,x) FROM t",
		"SELECT gcd(1,2),1",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
	}
	for _, query := range []string{
		"SELECT gcd(1,2) trailing junk",
		"SELECT gcd(1,2); trailing junk",
		"SELECT lcm(1,2) FROM",
		"INSERT INTO t VALUES(gcd(1,2))",
		"CREATE TABLE t(n INTEGER DEFAULT gcd(1,2))",
	} {
		statements, err := Parse(query)
		if err == nil || len(statements) != 0 {
			t.Fatal("widened grammar/full-input boundary", query, statements, err)
		}
	}
}
