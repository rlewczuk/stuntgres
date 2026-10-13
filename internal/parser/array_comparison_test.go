package parser

import "testing"

func TestArrayCompareSyntax(t *testing.T) {
	for _, tc := range []struct {
		sql         string
		list        bool
		count       int
		negate, all bool
	}{
		{"SELECT 1 IN(1)", true, 1, false, false},
		{"SELECT x NOT /* gap */ IN(y,$1,CAST(2 AS INT),1+2) FROM t", true, 4, true, false},
		{"SELECT (1=1) IN(TRUE,FALSE)", true, 2, false, false},
		{"SELECT 1=ANY(ARRAY[1,2])", false, 0, false, false},
		{"SELECT x<>ALL(a) FROM t", false, 0, false, true},
		{"SELECT $1!=SOME($2)", false, 0, false, false},
		{"SELECT 1 OPERATOR(pg_catalog.=) ANY('{1}'::INT[])", false, 0, false, false},
	} {
		statements, err := Parse(tc.sql)
		if err != nil {
			t.Fatal(tc.sql, err)
		}
		raw := statements[0].(*Select).Targets[0]
		if tc.list {
			n, ok := raw.(InListExpr)
			if !ok || len(n.Items) != tc.count || n.Negate != tc.negate {
				t.Fatalf("%s: %#v", tc.sql, raw)
			}
		} else {
			n, ok := raw.(ArrayComparisonExpr)
			if !ok || n.All != tc.all || n.Left == nil || n.Right == nil {
				t.Fatalf("%s: %#v", tc.sql, raw)
			}
			if tc.sql == "SELECT $1!=SOME($2)" && n.Name.Parts[0].Text != "<>" {
				t.Fatal("alias not canonical", n)
			}
		}
	}
	for _, sql := range []string{
		"SELECT NOT (1 IN(1,NULL)) AND 2=ANY(ARRAY[2]) OR FALSE",
		"SELECT 1 in", "SELECT 1 IN(SELECT 1)", "SELECT 1=ANY(SELECT 1)",
		"SELECT (1 IN(1)) IS TRUE", "SELECT (1=ANY(ARRAY[1]))=TRUE",
		"SELECT id FROM t WHERE t.x IN(t.y,$1) AND t.x<=ALL(t.a)",
		"SELECT 1 IN((SELECT 1),2)",
	} {
		if _, err := Parse(sql); err != nil {
			t.Fatal(sql, err)
		}
	}
	for _, sql := range []string{
		"SELECT 1 IN()", "SELECT 1 NOT IN()", "SELECT 1 IN(1,)", "SELECT 1 IN(1",
		"SELECT 1=ANY()", "SELECT 1=ALL(ARRAY[1]", "SELECT 1 IN(1)=TRUE",
		"SELECT 1=ANY(ARRAY[1])=TRUE", "SELECT 1 IN(1) trailing garbage",
	} {
		if _, err := Parse(sql); err == nil {
			t.Fatal("accepted malformed comparison", sql)
		}
	}
}

func TestArrayCompareLocations(t *testing.T) {
	for _, tc := range []struct {
		sql             string
		operator, start int
	}{
		{"SELECT /*é*/1 IN(1)", 15, 13},
		{"SELECT /*é*/1 NOT IN(2)", 15, 13},
		{"SELECT /*é*/1 = ANY(ARRAY[1])", 15, 13},
		{"SELECT 1 OPERATOR(pg_catalog.=) SOME(ARRAY[1])", 10, 8},
	} {
		statements, err := Parse(tc.sql)
		if err != nil {
			t.Fatal(err)
		}
		raw := statements[0].(*Select).Targets[0]
		if raw.SourcePosition().Character != tc.operator || ExpressionStart(raw).Character != tc.start {
			t.Fatal(tc.sql, raw.SourcePosition(), ExpressionStart(raw))
		}
	}
}
