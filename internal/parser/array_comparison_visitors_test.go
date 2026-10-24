package parser

import (
	"errors"
	"reflect"
	"testing"
)

func TestArrayCompareVisitors(t *testing.T) {
	t.Run("query sources", func(t *testing.T) {
		for _, sql := range []string{
			"SELECT (SELECT x FROM lhs) IN ((SELECT x FROM first_rhs), (SELECT x FROM last_rhs))",
			"SELECT (SELECT x FROM lhs) = ANY(ARRAY[(SELECT x FROM first_rhs), (SELECT x FROM last_rhs)])",
		} {
			statements, err := Parse(sql)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			want := []string{"lhs", "first_rhs", "last_rhs"}
			var names []string
			err = VisitQuerySources(statements[0], nil, func(table *TableReference, query *Select) error {
				names = append(names, table.Name.Parts[0].Text)
				return nil
			})
			if err != nil || !reflect.DeepEqual(names, want) {
				t.Fatal(names, err)
			}
			stop := errors.New("late RHS source rejected")
			err = VisitQuerySources(statements[0], nil, func(table *TableReference, query *Select) error {
				if table.Name.Parts[0].Text == "last_rhs" {
					return stop
				}
				return nil
			})
			if err != stop {
				t.Fatal(err)
			}
		}
	})

	literal := LiteralExpr{}
	position := Position{Offset: 31, Character: 29}
	qualified := ColumnExpr{
		Name: ObjectName{Parts: []Identifier{{Text: "t"}, {Text: "x"}}},
	}
	property := FunctionExpr{Name: ObjectName{Parts: []Identifier{{Text: "pg_index_has_property"}}}, Pos: position}
	stringCall := FunctionExpr{Name: ObjectName{Parts: []Identifier{{Text: "lower"}}}, Args: []Expr{literal}}
	forms := []struct {
		name string
		make func(Expr) Expr
	}{
		{"list left", func(child Expr) Expr { return InListExpr{Left: child, Items: []Expr{literal}} }},
		{"list late item", func(child Expr) Expr { return InListExpr{Left: literal, Items: []Expr{literal, child}, Negate: true} }},
		{"array left", func(child Expr) Expr { return ArrayComparisonExpr{Left: child, Right: literal} }},
		{"array right", func(child Expr) Expr { return ArrayComparisonExpr{Left: literal, Right: child, All: true} }},
	}
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			raw := form.make(qualified)
			if pos, found := firstDefaultIndexProperty(form.make(property)); !found || pos != position {
				t.Fatal(pos, found)
			}
			if valid, found := stringSingleton(form.make(stringCall)); !valid || !found {
				t.Fatal(valid, found)
			}
			if valid, _ := stringSingleton(raw); valid {
				t.Fatal("qualified child lost")
			}
			lexer := &lexer{}
			if !lexer.scalarInputTree(form.make(literal)) {
				t.Fatal("scalar predicate rejected")
			}
			if scalarCastArgument(raw) {
				t.Fatal("restricted function-cast predicate boundary widened")
			}
		})
	}
}
