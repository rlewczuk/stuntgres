package stored

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRuleCTEOriginalBodyLabels(t *testing.T) {
	read, _, integer, _ := routineCaptureFixture(t)
	value, err := sqltype.Integer(integer, 2)
	if err != nil {
		t.Fatal(err)
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value}
	raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 2}}
	for _, values := range []bool{false, true} {
		name := "select"
		if values {
			name = "values"
		}
		t.Run(name, func(t *testing.T) {
			selection := &parser.Select{Targets: []parser.Expr{raw}}
			child := &plan.ProjectNode{
				QuerySchema: plan.QuerySchema{{Name: "declared", Usage: integer.Usage()}},
				Child:       &plan.SingletonNode{}, Expressions: []expr.Expr{literal}, Syntax: selection, Frame: 2,
			}
			path := analyze.ViewLiteralPath{Query: 2, Clause: analyze.ViewTargetLiteral}
			body := " SELECT 2 AS \"?column?\""
			if values {
				selection.Targets = nil
				selection.Values = &parser.ValuesQuery{Rows: [][]parser.Expr{{raw}}}
				child.Child = &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Name: "column1", Usage: integer.Usage()}}, Rows: [][]expr.Expr{{literal}}}
				child.Expressions = []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: 2}}
				path.Clause = analyze.ViewValuesCellLiteral
				body = " VALUES (2)"
			}
			definition := parser.CommonTableExpression{Name: parser.Identifier{Text: "q"}, Columns: []parser.Identifier{{Text: "declared"}}, Query: selection}
			root := &plan.ProjectNode{Syntax: &parser.Select{With: []parser.CommonTableExpression{definition}}, Frame: 1, Child: &plan.SingletonNode{}}
			view := &View{
				query: &plan.Query{Root: root}, queries: map[*parser.Select]*plan.ProjectNode{selection: child},
				literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{path: literal},
			}
			renderer := NewQueryRenderer(view)
			policy := Presentation{Deparse: true, Rule: true, HideLabels: true, Indent: 8, Columns: []catalog.Column{{Name: "outer"}}}
			renderer.Presentation = policy
			got, err := renderer.WithClauseSQL(context.Background(), read, []parser.CommonTableExpression{definition}, false)
			want := " WITH q(declared) AS (\n                " + body + "\n                )\n        "
			if err != nil || got != want {
				t.Fatalf("visible CTE body = %q, %v; want %q", got, err, want)
			}
			if !reflect.DeepEqual(renderer.Presentation, policy) || child.Schema()[0].Name != "declared" {
				t.Fatal("CTE body changed caller presentation or executable schema")
			}
		})
	}
}
