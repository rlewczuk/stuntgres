package parser

import "testing"

func TestCheckSyntaxExistingAPI(t *testing.T) {
	for _, q := range []string{"CREATE TABLE t(a INT CHECK(a=1))", "CREATE TABLE t(CHECK(a<b),a INT,b INT)", "CREATE TABLE t(a INT DEFAULT 1 CONSTRAINT c CHECK(a=1) NO INHERIT NOT NULL)", "CREATE TABLE t(a INT,CONSTRAINT c CHECK(a=1 OR a IS NULL) NO INHERIT)", "CREATE TABLE t(a INT CHECK(CASE WHEN a IS NULL THEN TRUE ELSE a=1 END))"} {
		s, err := Parse(q)
		if err != nil || len(s) != 1 {
			t.Fatal(q, s, err)
		}
		if _, ok := s[0].(*CreateTable); !ok {
			t.Fatal(s)
		}
	}
	for _, q := range []string{"CREATE TABLE t(a INT CHECK())", "CREATE TABLE t(a INT CHECK(TRUE) NO INHERIT NO INHERIT)", "CREATE TABLE t(a INT CHECK(TRUE));INSERT INTO", "CREATE TABLE t(a INT CHECK(TRUE) NOT VALID)", "CREATE TABLE t(a INT CHECK(TRUE) DEFERRABLE)"} {
		s, err := Parse(q)
		if err == nil || s != nil {
			t.Fatal("accepted invalid whole input", q, s, err)
		}
	}
}
