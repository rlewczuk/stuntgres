package parser

import "testing"

func TestArithmeticGrammar(t *testing.T) {
	for _, tc := range []struct {
		q, outer, inner string
		side            int
	}{{"SELECT 2+3*4 FROM t", "+", "*", 1}, {"SELECT (2+3)*4 FROM t", "*", "+", 0}, {"SELECT 20-3-2 FROM t", "-", "-", 0}, {"SELECT - -i FROM t", "-", "-", 0}, {"SELECT s+h*h FROM t", "+", "*", 1}, {"SELECT (s+h)*h FROM t", "*", "+", 0}, {"SELECT s%(-h) FROM t", "%", "-", 1}, {"SELECT s/*a*/+h/*b*/*h FROM t", "+", "*", 1}} {
		statements, err := Parse(tc.q)
		if err != nil {
			t.Fatal(tc.q, err)
		}
		outer, ok := statements[0].(*Select).Targets[0].(OperatorExpr)
		if !ok || outer.Name.Parts[0].Text != tc.outer {
			t.Fatal(tc, outer)
		}
		inner, ok := outer.Args[tc.side].(OperatorExpr)
		if !ok || inner.Name.Parts[0].Text != tc.inner {
			t.Fatal(tc, inner)
		}
	}
	for _, tc := range []struct {
		expr    string
		kind    ValueKind
		integer int64
		text    string
	}{{"-2147483648", IntegerValue, -2147483648, ""}, {"-9223372036854775808", IntegerValue, -9223372036854775808, ""}, {"-(-2147483648)", IntegerValue, 2147483648, ""}, {"-(-9223372036854775808)", NumericValue, 0, "9223372036854775808"}} {
		statements, err := Parse("SELECT " + tc.expr + " FROM t")
		if err != nil {
			t.Fatal(err)
		}
		literal, ok := statements[0].(*Select).Targets[0].(LiteralExpr)
		if !ok || literal.Value.Kind != tc.kind || literal.Value.Integer != tc.integer || literal.Value.Text != tc.text || literal.Value.Pos.Character != 8 {
			t.Fatal(tc, literal)
		}
	}
	statements, err := Parse("SELECT /*é*/ i+1 FROM t")
	if err != nil {
		t.Fatal(err)
	}
	op := statements[0].(*Select).Targets[0].(OperatorExpr)
	if op.Pos.Character != 15 || len(op.Args) != 2 {
		t.Fatal(op)
	}
	for _, q := range []string{"SELECT i+ FROM t", "SELECT (i+1 FROM t", "SELECT i+1 FROM t trailing garbage"} {
		if _, err := Parse(q); err == nil {
			t.Fatal("unsupported/malformed SQL accepted", q)
		}
	}
}

func TestArithmeticSignedCastNoFromSyntax(t *testing.T) {
	for _, q := range []string{
		"SELECT num_nulls(CAST(+1 AS int4),NULL),num_nonnulls(+1::int4,NULL)",
		"SELECT num_nulls(+(1::int4),NULL)",
		"SELECT num_nulls(+CAST(1 AS bigint),NULL)",
		"SELECT num_nulls(-1::int4,NULL)",
		"SELECT num_nulls(-(CAST(1 AS bigint)::int8),NULL)",
		"SELECT num_nulls((-1)::int4,NULL)",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 || statements[0].(*Select).From != nil || statements[0].(*Select).Values != nil {
			t.Fatal(q, statements, err)
		}
	}
	for _, q := range []string{
		"SELECT num_nulls(+(CAST(1+2 AS int4)),NULL)",
		"SELECT num_nulls(+(i::int4),NULL)",
		"SELECT num_nulls(+CAST($1 AS int4),NULL)",
		"SELECT num_nulls(-(CAST(1+2 AS int4)),NULL)",
		"SELECT num_nulls(-(i::int4),NULL)",
		"SELECT num_nulls(-CAST($1 AS int4),NULL)",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 || statements[0].(*Select).From != nil {
			t.Fatal("lost ordinary signed cast query", q, statements, err)
		}
	}
}

func TestFloatingPowerGrammar(t *testing.T) {
	for _, tc := range []struct {
		query, outer, inner string
		side                int
	}{
		{"SELECT 2^3^2 FROM t", "^", "^", 0},
		{"SELECT 2^(3^2) FROM t", "^", "^", 1},
		{"SELECT 2*3^2 FROM t", "*", "^", 1},
		{"SELECT 2^3*2 FROM t", "*", "^", 0},
		{"SELECT -(i^2) FROM t", "-", "^", 0},
		{"SELECT -i^2 FROM t", "^", "-", 0},
		{"SELECT 2 ^ -i FROM t", "^", "-", 1},
		{"SELECT 2 OPERATOR(pg_catalog.^) 3 * 2 FROM t", "pg_catalog", "*", 1},
	} {
		statements, err := Parse(tc.query)
		if err != nil {
			t.Fatal(tc.query, err)
		}
		outer, ok := statements[0].(*Select).Targets[0].(OperatorExpr)
		if !ok || outer.Name.Parts[0].Text != tc.outer {
			t.Fatal(tc, outer)
		}
		inner, ok := outer.Args[tc.side].(OperatorExpr)
		if !ok || inner.Name.Parts[0].Text != tc.inner {
			t.Fatal(tc, inner)
		}
	}
	statements, err := Parse("SELECT 2^-3 FROM t")
	if err != nil {
		t.Fatal(err)
	}
	operator := statements[0].(*Select).Targets[0].(OperatorExpr)
	if operator.Name.Parts[0].Text != "^-" || len(operator.Args) != 2 || operator.Pos.Character != 9 {
		t.Fatal("maximal munch lost", operator)
	}
}
