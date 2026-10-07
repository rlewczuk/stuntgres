package parser

import (
	"testing"
)

func TestLogicalPrecedenceAndLocations(t *testing.T) {
	parse := func(q string) *Select {
		t.Helper()
		stmts, err := Parse(q)
		if err != nil || len(stmts) != 1 {
			t.Fatal(q, stmts, err)
		}
		return stmts[0].(*Select)
	}
	logical := func(raw Expr, op LogicalOperator) LogicalExpr {
		t.Helper()
		n, ok := raw.(LogicalExpr)
		if !ok || n.Op != op {
			t.Fatalf("got %#v, want logical %v", raw, op)
		}
		return n
	}
	s := parse("SELECT TRUE OR FALSE AND FALSE FROM t")
	n := logical(s.Targets[0], LogicalOr)
	logical(n.Right, LogicalAnd)
	if n.Pos.Character != 13 || n.SourcePosition().Character != 8 {
		t.Fatal(n)
	}
	s = parse("SELECT (TRUE OR FALSE) AND FALSE FROM t")
	n = logical(s.Targets[0], LogicalAnd)
	logical(n.Left, LogicalOr)
	s = parse("SELECT NOT NOT NULL FROM t")
	n = logical(s.Targets[0], LogicalNot)
	logical(n.Left, LogicalNot)
	s = parse("SELECT TRUE AND FALSE AND NULL FROM t")
	n = logical(s.Targets[0], LogicalAnd)
	logical(n.Left, LogicalAnd)
	s = parse("SELECT TRUE OR FALSE OR NULL FROM t")
	n = logical(s.Targets[0], LogicalOr)
	logical(n.Left, LogicalOr)
	s = parse("SELECT id FROM t WHERE NOT id=1 AND (a OR b)")
	n = logical(s.Where, LogicalAnd)
	not := logical(n.Left, LogicalNot)
	if _, ok := not.Left.(OperatorExpr); !ok {
		t.Fatal(not)
	}
	logical(n.Right, LogicalOr)
	s = parse("SELECT TRUE AND (1) FROM t")
	n = logical(s.Targets[0], LogicalAnd)
	if n.Right.SourcePosition().Character != 18 {
		t.Fatal(n.Right.SourcePosition())
	}
	parse(`SELECT "or" AND "and" FROM "not"`)
	parse("SELECT a::BOOL AND b FROM t")
	s = parse("SELECT a+1 AND b FROM t")
	n = logical(s.Targets[0], LogicalAnd)
	if arithmetic, ok := n.Left.(OperatorExpr); !ok || arithmetic.Name.Parts[0].Text != "+" {
		t.Fatal(n.Left)
	}
	s = parse("SELECT id FROM t WHERE (id)=1")
	if comparison, ok := s.Where.(OperatorExpr); !ok || comparison.Name.Parts[0].Text != "=" {
		t.Fatal(s.Where)
	}
}

func TestLogicalSyntaxAndScopeBoundaries(t *testing.T) {
	for _, tc := range []struct{ q, message, position string }{
		{"SELECT TRUE AND FROM t", `syntax error at or near "FROM"`, "17"},
		{"SELECT NOT FROM t", `syntax error at or near "FROM"`, "12"},
		{"SELECT id FROM t WHERE TRUE AND", "syntax error at end of input", "32"},
		{"SELECT (TRUE OR FALSE FROM t", `syntax error at or near "FROM"`, "23"},
		{"SELECT () FROM t", `syntax error at or near ")"`, "9"},
		{"SELECT TRUE AND AND FALSE FROM t", `syntax error at or near "AND"`, "17"},
		{"SELECT TRUE OR FROM t", `syntax error at or near "FROM"`, "16"},
	} {
		keywordDiagnostic(t, tc.q, "42601", tc.message, tc.position)
	}
	for _, q := range []string{
		"SELECT TRUE AND FALSE",
		"SELECT id=1=TRUE FROM t", "SELECT (id=1)=TRUE FROM t",
		"INSERT INTO t VALUES(TRUE AND FALSE)", "INSERT INTO t VALUES(NOT TRUE)",
		"INSERT INTO t SELECT TRUE AND FALSE FROM t RETURNING a", "SELECT current_setting(TRUE AND FALSE)", "SET application_name TO TRUE AND FALSE",
		"SELECT a AND b result FROM t",
		"SELECT f(a) AND b FROM t", "SELECT a FROM t ORDER BY a AND b",
		"CREATE TABLE or(a BOOL)", "SELECT or FROM t", "INSERT INTO t VALUES(1); SELECT TRUE AND FROM t",
	} {
		stmts, err := Parse(q)
		if err == nil || stmts != nil {
			t.Fatal("scope expansion or executable prefix", q, stmts, err)
		}
	}
	for _, q := range []string{"SELECT a AND b AS result FROM t", "SELECT t.a AND b FROM t"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal("supported target label or qualification", q, statements, err)
		}
		logical, ok := statements[0].(*Select).Targets[0].(LogicalExpr)
		if !ok || logical.Op != LogicalAnd {
			t.Fatal("lost logical target", q, statements)
		}
	}
}
