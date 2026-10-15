package parser

import "testing"

func TestQueryWindowNtileParse(t *testing.T) {
	for _, callee := range []string{"ntile", "pg_catalog.ntile", `"ntile"`} {
		for _, over := range []string{"", " OVER w", " OVER(ORDER BY id ROWS CURRENT ROW EXCLUDE CURRENT ROW)"} {
			q := "SELECT /*é*/ " + callee + "($1)" + over + " FROM t WINDOW w AS(ORDER BY id)"
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(AggregateExpr)
			if !ok || call.Name != "ntile" || len(call.Args) != 1 || call.Pos.Character != 14 {
				t.Fatal("call shape/position", call)
			}
			if _, ok := call.Args[0].(ParameterExpr); !ok {
				t.Fatal("parameter child", call.Args[0])
			}
			if callee == "pg_catalog.ntile" && (call.Schema == nil || call.Schema.Text != "pg_catalog") {
				t.Fatal("schema", call)
			}
			if over == "" {
				if call.Window != nil {
					t.Fatal("missing OVER", call)
				}
			} else if call.Window == nil {
				t.Fatal("lost OVER", call)
			}
			if over == " OVER w" && (call.Window.Name == nil || call.Window.Name.Text != "w") {
				t.Fatal("named window", call)
			}
			if over == " OVER(ORDER BY id ROWS CURRENT ROW EXCLUDE CURRENT ROW)" && (call.Window.Frame == nil || call.Window.Frame.Exclusion != ExcludeCurrentRow) {
				t.Fatal("frame", call)
			}
		}
	}
	for _, q := range []string{"SELECT ntile(DISTINCT 2) OVER()", "SELECT ntile(2 ORDER BY id) OVER() FROM t", "SELECT ntile(2) FILTER(WHERE TRUE) OVER()"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		if call, ok := statements[0].(*Select).Targets[0].(AggregateExpr); !ok || call.Name != "ntile" || call.Window == nil {
			t.Fatal("modified call", call)
		}
	}
}
