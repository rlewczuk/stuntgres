package parser

import "testing"

func TestBitwiseGrammarAndLexicalBoundaries(t *testing.T) {
	for _, tc := range []struct {
		q, outer, inner string
		side            int
	}{
		{"SELECT 1 | 2 & 4 FROM t", "&", "|", 0},
		{"SELECT 1 << 2 + 1 FROM t", "<<", "+", 1},
		{"SELECT ~1 + 2 FROM t", "~", "+", 0},
		{"SELECT (~1)+2 FROM t", "+", "~", 0},
	} {
		ss, err := Parse(tc.q)
		if err != nil {
			t.Fatal(tc.q, err)
		}
		outer, ok := ss[0].(*Select).Targets[0].(OperatorExpr)
		if !ok || outer.Name.Parts[0].Text != tc.outer {
			t.Fatal(tc, outer)
		}
		inner, ok := outer.Args[tc.side].(OperatorExpr)
		if !ok || inner.Name.Parts[0].Text != tc.inner {
			t.Fatal(tc, inner)
		}
	}
	for _, tc := range []struct{ expr, op string }{{"1&-1", "&-"}, {"1|~1", "|~"}, {"1<<~1", "<<~"}, {"1<<-1", "<<"}, {"1&/* comment */1", "&"}, {"1<=2", "<="}, {"1>=2", ">="}, {"1<>2", "<>"}, {"1!=2", "<>"}} {
		ss, err := Parse("SELECT " + tc.expr + " FROM t")
		if err != nil {
			t.Fatal(tc, err)
		}
		op, ok := ss[0].(*Select).Targets[0].(OperatorExpr)
		if !ok || op.Name.Parts[0].Text != tc.op {
			t.Fatal(tc, op)
		}
	}
	ss, err := Parse("SELECT /*é*/ 1&1 FROM t")
	if err != nil {
		t.Fatal(err)
	}
	op := ss[0].(*Select).Targets[0].(OperatorExpr)
	if op.Pos.Character != 15 {
		t.Fatal(op.Pos)
	}
	ss, err = Parse("SELECT num_nulls(1&1)")
	if err != nil || len(ss) != 1 || ss[0].(*Select).From != nil {
		t.Fatal(ss, err)
	}
	for _, q := range []string{"SELECT 1& FROM t", "SELECT ~ FROM t", "SELECT 1&1 FROM t trailing garbage"} {
		if _, err := Parse(q); err == nil {
			t.Fatal("malformed/excluded expression admitted", q)
		}
	}
}

func TestBitwiseUnicodeQuotingPreserved(t *testing.T) {
	ss, err := Parse("SELECT U&'1' & 1 FROM t")
	if err != nil {
		t.Fatal(err)
	}
	op, ok := ss[0].(*Select).Targets[0].(OperatorExpr)
	if !ok || op.Name.Parts[0].Text != "&" {
		t.Fatal(op)
	}
	literal, ok := op.Args[0].(LiteralExpr)
	if !ok || literal.Value.Kind != StringValue || literal.Value.Text != "1" {
		t.Fatal(literal)
	}
}

func TestBitwiseNamedArgumentTokenIsNotOperator(t *testing.T) {
	for _, q := range []string{
		"SELECT 1 => 2 FROM t", "SELECT 1=>2 FROM t",
		"SELECT =>1 FROM t", "INSERT INTO t VALUES(1=>2)",
		"UPDATE t SET a=1=>2", "CREATE TABLE t(a INTEGER DEFAULT (1=>2))",
	} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatal("named-argument token admitted as generic operator", q, statements, err)
		}
	}
}
