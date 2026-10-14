package parser

import "testing"

func TestQueryWindowValueGrammar(t *testing.T) {
	for _, name := range []string{"first_value", "last_value", "nth_value"} {
		arguments := "v"
		if name == "nth_value" {
			arguments += ",$1"
		}
		for _, callee := range []string{name, "pg_catalog." + name, `"` + name + `"`} {
			q := "SELECT /*é*/ " + callee + "(" + arguments + ") OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES) FROM t"
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(AggregateExpr)
			arity := 1
			if name == "nth_value" {
				arity = 2
			}
			if !ok || call.Name != name || len(call.Args) != arity || call.Pos.Character != 14 || call.Window == nil || call.Window.Frame == nil || call.Window.Frame.Exclusion != ExcludeTies {
				t.Fatal("window AST/position/children", q, call)
			}
			if callee == "pg_catalog."+name && (call.Schema == nil || call.Schema.Text != "pg_catalog") {
				t.Fatal("qualified callable identity", call)
			}
		}
		statements, err := Parse("SELECT " + name + "(" + arguments + ") OVER w FROM t WINDOW w AS(ORDER BY id)")
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		call := statements[0].(*Select).Targets[0].(AggregateExpr)
		if call.Window.Name == nil || call.Window.Name.Text != "w" {
			t.Fatal("named OVER", call)
		}
		statements, err = Parse("SELECT " + name + "(" + arguments + ")")
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		if call := statements[0].(*Select).Targets[0].(AggregateExpr); call.Window != nil || call.Name != name {
			t.Fatal("missing OVER classification", call)
		}
	}
}
