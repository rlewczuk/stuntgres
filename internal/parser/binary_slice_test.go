package parser

import "testing"

func TestBinarySliceSyntax(t *testing.T) {
	for _, q := range []string{
		"SELECT overlay(v PLACING s FROM i FOR n),position(s IN v),substring(v FROM i FOR n),substring(v FOR n FROM i),substring(v FOR n),trim(s FROM v) FROM t",
		"SELECT overlay(v,s,i,n),substring(v,i,n),substr(v,i,n),pg_catalog.position(v,s),\"position\"(v,s) FROM t",
		"SELECT trim(BOTH s FROM v),trim(LEADING s FROM v),trim(TRAILING s FROM v),trim(v,s),trim(FROM v,s),trim(LEADING FROM v,s),trim(TRAILING v,s) FROM t",
		"CREATE TABLE t(v BYTEA,s BYTEA,i INTEGER,n INTEGER,CHECK(overlay(v PLACING s FROM i FOR n)=v))",
		"UPDATE t SET v=substring(v FROM i FOR n) WHERE position(s IN v)>0 RETURNING trim(s FROM v)",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
	for _, q := range []string{"SELECT position(v,s) FROM t", "SELECT pg_catalog.overlay(v PLACING s FROM i) FROM t", "SELECT pg_catalog.substring(v FROM i) FROM t"} {
		if _, err := Parse(q); err == nil {
			t.Fatal("accepted invalid function spelling", q)
		}
	}
}
