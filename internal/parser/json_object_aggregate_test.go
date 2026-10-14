package parser

import (
	"strings"
	"testing"
)

func TestJSONObjectAggregateOrdinarySyntax(t *testing.T) {
	for _, name := range []string{"json_object_agg", "jsonb_object_agg", "json_object_agg_strict", "jsonb_object_agg_strict"} {
		for _, prefix := range []string{"", "pg_catalog.", "public."} {
			for _, suffix := range []string{"()", "(*)", "(1)", "(1,2)", "(1,2,3)", "(ALL k,v ORDER BY id)", "(DISTINCT k,v ORDER BY k,v) FILTER(WHERE TRUE)", "(k,v) OVER()"} {
				q := "/*é😀*/ SELECT " + prefix + name + suffix + " FROM t"
				statements, err := Parse(q)
				if err != nil || len(statements) != 1 {
					t.Fatal(q, err)
				}
				call, ok := statements[0].(*Select).Targets[0].(AggregateExpr)
				if !ok || call.Name != name || call.JSON != nil || call.Pos.Character != 15 || (prefix != "" && call.Schema == nil) {
					t.Fatal("ordinary aggregate provenance", q, call)
				}
			}
		}
		for _, call := range []string{name + "(missing)", name + "(1)", name + "(1) FILTER(WHERE 2)"} {
			if statements, err := Parse("INSERT INTO t VALUES(" + call + ")"); err != nil || len(statements) != 1 {
				t.Fatal("VALUES child analysis lost", call, err)
			}
		}
		q := "CREATE TABLE t(v int,j json GENERATED ALWAYS AS(" + name + "('a',v)) STORED)"
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, err)
		}
		raw := statements[0].(*CreateTable).Elements[1].Column.Constraints[0].Generation.Expression
		forbidden, ok := raw.(ForbiddenGenerationExpr)
		if !ok || forbidden.Code != "42803" || forbidden.Pos.Character != strings.Index(q, name)+1 {
			t.Fatal("generation aggregate restriction", raw)
		}
	}
}

func TestJSONObjectAggregateQuotedCallIdentity(t *testing.T) {
	for _, name := range []string{"JSON_OBJECT_AGG", "Json_Object_Agg", "JSONB_OBJECT_AGG", "Jsonb_Object_Agg", "JSON_OBJECT_AGG_STRICT", "Json_Object_Agg_Strict", "JSONB_OBJECT_AGG_STRICT", "Jsonb_Object_Agg_Strict"} {
		for _, suffix := range []string{"(7)", "(ROW(7)::jo_projection)", "(k,v)", "(DISTINCT k,v ORDER BY k,v)"} {
			q := `SELECT "` + name + `"` + suffix
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			if !ok || len(call.Name.Parts) != 1 || call.Name.Parts[0].Text != name || call.ResolutionOnly || call.CompositeCandidate {
				t.Fatal("exact quoted ordinary identity lost", q, call)
			}
		}
	}
}
