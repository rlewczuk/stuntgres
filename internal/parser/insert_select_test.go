package parser

import "testing"

func TestInsertSelectSources(t *testing.T) {
	for _, tc := range []struct {
		sql           string
		query, values bool
		targets       int
	}{
		{"INSERT INTO t SELECT i FROM s WHERE i=1 ORDER BY i", true, false, 0},
		{"INSERT INTO t(i) ((SELECT $1))", true, false, 1},
		{"INSERT INTO t (SELECT)", true, false, 0},
		{"INSERT INTO t SELECT TRUE AND FALSE", true, false, 0},
		{"INSERT INTO t VALUES ('1')", false, false, 0},
		{"INSERT INTO t ((VALUES ('1')))", false, false, 0},
		{"INSERT INTO t(i) (VALUES (1),(2) ORDER BY column1 DESC)", true, true, 1},
		{"INSERT INTO t VALUES (1),(2) ORDER BY 1", true, true, 0},
		{"INSERT INTO t SELECT x.i FROM s AS x", true, false, 0},
		{"INSERT INTO t SELECT x.i FROM s AS x JOIN r USING(i)", true, false, 0},
	} {
		stmts, err := Parse(tc.sql)
		if err != nil || len(stmts) != 1 {
			t.Fatal(tc.sql, stmts, err)
		}
		s := stmts[0].(*Insert)
		if (s.Source != nil) != tc.query || len(s.Targets) != tc.targets {
			t.Fatal(tc.sql, s)
		}
		if tc.query {
			if len(s.Rows) != 0 || (s.Source.Select == nil) != tc.values {
				t.Fatal(tc.sql, s)
			}
		} else if len(s.Rows) != 1 {
			t.Fatal(tc.sql, s)
		}
	}
}
func TestInsertSelectBoundaries(t *testing.T) {
	for _, q := range []string{
		"INSERT INTO t SELECT 1 RETURNING i", "INSERT INTO t VALUES(1) ORDER BY 1 LIMIT 1 LIMIT 2",
		"INSERT INTO t WITH x AS (SELECT 1) SELECT 1",
		"INSERT INTO t VALUES()", "SELECT 1",
		"INSERT INTO t VALUES(1); INSERT INTO t (SELECT 1) VALUES(2)",
	} {
		stmts, err := Parse(q)
		if err == nil || stmts != nil {
			t.Fatal("boundary escaped", q, stmts, err)
		}
	}
}

func TestOrderInsertSyntax(t *testing.T) {
	for _, key := range []string{"1", "2147483647", "2147483648", "-2147483647", "-2147483648", "+2", "+1.5 DESC", "+1e0 ASC", "+1e131072", "1.5", "1e0", "'x'", "TRUE", "NULL", "(0)", "column1+0 DESC NULLS LAST,column1"} {
		q := "INSERT INTO t VALUES(1) ORDER BY " + key + "; INSERT INTO t VALUES(9)"
		statements, err := Parse(q)
		if err != nil || len(statements) != 2 {
			t.Fatal(q, statements, err)
		}
		order := statements[0].(*Insert).Source.Values.Order
		if order == nil || len(order.Keys) == 0 || order.Keys[0].Expr == nil {
			t.Fatal(order)
		}
	}
	keywordDiagnostic(t, "INSERT INTO t VALUES(1) ORDER BY 'x'; CREATE TABLE select(x INT)", "42601", `syntax error at or near "select"`, "52")
}
