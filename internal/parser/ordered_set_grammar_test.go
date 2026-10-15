package parser

import "testing"

func TestOrderedSetAggregateGrammarAttachment(t *testing.T) {
	statements, err := Parse("SELECT abs() WITHIN GROUP(ORDER BY 1)")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	ordinary, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
	if !ok || !ordinary.Within || len(ordinary.Args) != 0 || len(ordinary.Order) != 1 || ordinary.WithinPos.Character != 14 {
		t.Fatal("scalar WITHIN attached as alias or lost", ordinary)
	}
	statements, err = Parse("SELECT abs(percentile_cont(.25) WITHIN GROUP(ORDER BY x) FILTER(WHERE x IS NOT NULL)) FROM t")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	outer, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
	if !ok || len(outer.Args) != 1 || outer.Within {
		t.Fatal("wrong scalar attachment", outer)
	}
	inner, ok := outer.Args[0].(AggregateExpr)
	if !ok || !inner.Within || len(inner.Args) != 1 || len(inner.Order) != 1 || inner.Filter == nil || inner.Window != nil {
		t.Fatal("nested aggregate clause attachment", inner)
	}
	query := "CREATE INDEX ix ON t(pg_catalog.overlaps(TIME '00:00',TIME '01:00',TIME '02:00') WITHIN GROUP(ORDER BY TIME '03:00'))"
	statements, err = Parse(query)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	index, ok := statements[0].(*CreateIndex)
	if !ok || len(index.Keys) != 1 {
		t.Fatal("index route", statements[0])
	}
	call, ok := index.Keys[0].Expression.(FunctionExpr)
	if !ok || !call.Within || len(call.Args) != 3 || len(call.Order) != 1 || len(call.Name.Parts) != 2 || call.Name.Parts[1].Text != "overlaps" {
		t.Fatal("direct index-function WITHIN attachment", call)
	}
	// Establish the real lexical route used by that index witness. Selected
	// abs() is rewritten to CHECK_FUNC, whereas qualified OVERLAPS retains
	// the token accepted by function:name '.' OVERLAPS in the state report.
	for _, tc := range []struct {
		query string
		want  int
	}{{"abs()", CHECK_FUNC}, {"pg_catalog.overlaps()", OVERLAPS}} {
		l := &lexer{input: tc.query, indexExpression: true, options: Options{StandardConformingStrings: true}}
		found := false
		for {
			var value sqlSymType
			token := l.Lex(&value)
			if token == 0 {
				break
			}
			if value.tok.text == "abs" || value.tok.text == "overlaps" {
				if token != tc.want {
					t.Fatal("index witness reaches different grammar", tc.query, token, tc.want)
				}
				found = true
			}
		}
		if l.err != nil || !found {
			t.Fatal("lexical witness", tc.query, l.err, found)
		}
	}
}

func TestArrayTransformWithinGrammar(t *testing.T) {
	for _, name := range []string{"array_sort", "pg_catalog.array_sort"} {
		statements, err := Parse("SELECT " + name + "(ARRAY[1,2]) WITHIN GROUP (ORDER BY id) AS a FROM t")
		if err != nil || len(statements) != 1 {
			t.Fatal(name, statements, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || !call.Within || len(call.Args) != 1 || len(call.Order) != 1 {
			t.Fatal("selected decoration lost", call)
		}
	}
	// Sharing the modifier grammar must not admit adjacent array families.
	if _, err := Parse("SELECT array_position(ARRAY[1,2],1) WITHIN GROUP (ORDER BY id) FROM t"); err == nil {
		t.Fatal("adjacent array search decoration admitted")
	}
}
