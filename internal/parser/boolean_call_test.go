package parser

import "testing"

func TestBooleanCallPredicateGrammar(t *testing.T) {
	for _, name := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
		for _, prefix := range []string{"", "pg_catalog."} {
			parts := 1
			if prefix != "" {
				parts = 2
			}
			for _, arg := range []string{"s.oid", "1", "$1"} {
				for _, tail := range []string{"", ";", " ORDER BY 1"} {
					q := "SELECT s.oid FROM source s WHERE (((" + prefix + name + "(" + arg + "))))" + tail
					statements, err := Parse(q)
					if err != nil || len(statements) != 1 {
						t.Fatal(q, statements, err)
					}
					call, ok := statements[0].(*Select).Where.(FunctionExpr)
					if !ok || len(call.Args) != 1 || call.Name.Parts[len(call.Name.Parts)-1].Text != name || len(call.Name.Parts) != parts || ExpressionStart(call) != call.Pos {
						t.Fatal(q, call)
					}
				}
			}
		}
	}
	statements, err := Parse("SELECT NOT ((pg_catalog.pg_table_is_visible($1))) AND id=1 OR pg_type_is_visible(23) FROM source")
	if err != nil {
		t.Fatal(err)
	}
	or := statements[0].(*Select).Targets[0].(LogicalExpr)
	and := or.Left.(LogicalExpr)
	not := and.Left.(LogicalExpr)
	call := not.Left.(FunctionExpr)
	if or.Op != LogicalOr || and.Op != LogicalAnd || not.Op != LogicalNot || call.Pos.Character != 14 || call.Args[0].SourcePosition().Character != 45 {
		t.Fatal(or, and, not, call)
	}
	if _, ok := and.Right.(OperatorExpr); !ok {
		t.Fatal("comparison must bind above AND", and.Right)
	}
	for _, q := range []string{
		"SELECT num_nulls(v) IS NULL FROM t", "SELECT (num_nulls(v)) IS NULL FROM t",
		"SELECT num_nulls(v)=1 FROM t", "SELECT 1=num_nulls(v) FROM t",
		"SELECT num_nulls(v) AND TRUE FROM t", "SELECT TRUE OR num_nulls(v) FROM t",
		"SELECT id FROM t WHERE num_nulls(v)", "SELECT id FROM t WHERE num_nulls(v) IS NULL",
		"SELECT CASE WHEN TRUE THEN num_nulls(NULL) ELSE 1 END",
		"SELECT 1 BETWEEN num_nulls(NULL) AND 2",
		"CREATE TABLE bounded(v TEXT DEFAULT to_char(num_nulls(uuidv4()),'FM9'))",
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
	for _, q := range []string{"SELECT num_nulls(1)+1", "SELECT num_nulls((v IS NULL)) FROM t", "SELECT 1<2<3"} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatal("expanded scalar/argument language", q, statements, err)
		}
	}
}

func TestBooleanCallOrdinaryArgumentGrammar(t *testing.T) {
	// A selected first argument uses the same ordinary-call production as
	// other target expressions, including ordered and multi-argument forms.
	for _, prefix := range []string{"", "pg_catalog."} {
		for _, tc := range []struct {
			tail string
			args int
			keys int
		}{
			{")", 1, 0},
			{", 1)", 2, 0},
			{" ORDER BY v)", 1, 1},
			{", 1 ORDER BY v)", 2, 1},
		} {
			q := "SELECT " + prefix + "sum(abs(v)" + tc.tail + " FROM t"
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
			outer, ok := statements[0].(*Select).Targets[0].(AggregateExpr)
			if !ok || outer.Name != "sum" || len(outer.Args) != tc.args || len(outer.Order) != tc.keys {
				t.Fatal("lost ordinary call arguments or order", q, outer)
			}
			inner, ok := outer.Args[0].(FunctionExpr)
			if !ok || inner.Name.Parts[0].Text != "abs" || len(inner.Args) != 1 {
				t.Fatal("lost selected argument", q, outer.Args[0])
			}
			if _, ok := inner.Args[0].(ColumnExpr); !ok {
				t.Fatal("lost nested column", q, inner.Args[0])
			}
		}
	}
}
