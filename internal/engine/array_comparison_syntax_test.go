package engine

import (
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
	"testing"
)

func TestArrayCompareVisitorsStoredSyntax(t *testing.T) {
	position := parser.Position{Offset: 17, Character: 15}
	left := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "1"}}
	item := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}
	in := parser.InListExpr{Left: left, Items: []parser.Expr{item}, Pos: position}
	lowered := parser.OperatorExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}},
		Args: []parser.Expr{left, item}, Pos: position,
	}
	b := &analyze.Binder{
		Stored: analyze.StoredSyntaxState{Syntax: map[analyze.StoredExpressionSyntaxKey]parser.Expr{
			{Frame: 1, Position: position}: lowered,
		}},
	}
	childQuery := &parser.Select{Targets: []parser.Expr{in}}
	raw := parser.FunctionExpr{
		Args:  []parser.Expr{parser.SubqueryExpr{Query: childQuery, Left: in}, parser.CastExpr{Child: in}},
		Order: []parser.OrderKey{{Expr: in}}, Filter: in,
		Window: &parser.WindowSpec{Partition: []parser.Expr{in}, Order: []parser.OrderKey{{Expr: in}},
			Frame: &parser.WindowFrame{Start: parser.FrameBound{Offset: in}, End: parser.FrameBound{Offset: in}}},
	}
	next, changed := b.NormalizeInListExpression(raw)
	if !changed {
		t.Fatal("nested IN was not normalized")
	}
	call := next.(parser.FunctionExpr)
	if call.Args[0].(parser.SubqueryExpr).Query != childQuery {
		t.Fatal("child query identity changed")
	}
	if !reflect.DeepEqual(childQuery.Targets[0], in) {
		t.Fatal("normalization crossed child query frame")
	}
	for _, expr := range []parser.Expr{
		call.Args[0].(parser.SubqueryExpr).Left, call.Args[1].(parser.CastExpr).Child,
		call.Order[0].Expr, call.Filter, call.Window.Partition[0], call.Window.Order[0].Expr,
		call.Window.Frame.Start.Offset, call.Window.Frame.End.Offset,
	} {
		if !reflect.DeepEqual(expr, lowered) {
			t.Fatal("lost lowered authority", expr)
		}
	}
	if !reflect.DeepEqual(raw.Args[1].(parser.CastExpr).Child, in) || !reflect.DeepEqual(raw.Window.Partition[0], in) || !reflect.DeepEqual(raw.Window.Frame.Start.Offset, in) {
		t.Fatal("normalization mutated original ancestors")
	}
	if again, changed := b.NormalizeInListExpression(call); changed || !reflect.DeepEqual(again, call) {
		t.Fatal("canonical syntax was not stable", changed)
	}
	if _, changed := (&analyze.Binder{}).NormalizeInListExpression(raw); changed {
		t.Fatal("missing analysis template changed syntax")
	}

	query := &parser.Select{
		Targets: []parser.Expr{in}, Where: in, Having: in,
		From:     &parser.JoinReference{On: in},
		GroupBy:  &parser.GroupClause{Items: []parser.GroupItem{{Items: []parser.GroupItem{{Expr: in}}}}},
		Order:    &parser.Order{Keys: []parser.OrderKey{{Expr: in}}},
		Bounds:   &parser.QueryBounds{Offset: in, Count: in},
		Windows:  []parser.WindowDefinition{{Spec: *raw.Window}},
		Distinct: &parser.DistinctClause{On: []parser.Expr{in}},
		Values:   &parser.ValuesQuery{Rows: [][]parser.Expr{{in}}},
	}
	originalTargets := query.Targets
	originalGroup := query.GroupBy
	originalFrom := query.From.(*parser.JoinReference)
	b.NormalizeStoredQuerySyntax(query)
	for _, expr := range []parser.Expr{
		query.Targets[0], query.Where, query.Having, query.From.(*parser.JoinReference).On,
		query.GroupBy.Items[0].Items[0].Expr, query.Order.Keys[0].Expr,
		query.Bounds.Offset, query.Bounds.Count, query.Distinct.On[0], query.Values.Rows[0][0],
		query.Windows[0].Spec.Partition[0],
	} {
		if !reflect.DeepEqual(expr, lowered) {
			t.Fatal("owning syntax lost normalization", expr)
		}
	}
	if !reflect.DeepEqual(originalTargets[0], in) || !reflect.DeepEqual(originalGroup.Items[0].Items[0].Expr, in) || !reflect.DeepEqual(originalFrom.On, in) {
		t.Fatal("owning syntax normalization mutated shared ancestors")
	}
}
