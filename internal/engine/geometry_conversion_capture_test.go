package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestGeometryConversionCTypeNameCapture(t *testing.T) {
	e := New()
	callPos := parser.Position{Offset: 7, Character: 8}
	paramPos := parser.Position{Offset: 15, Character: 16}
	raw := parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "polygon", Pos: callPos}}}, Pos: callPos,
		Args: []parser.Expr{parser.CastExpr{
			Child:    parser.ParameterExpr{Number: 1, Pos: paramPos},
			TypeName: parser.TypeName{Name: "text", Pos: parser.Position{Offset: 19, Character: 20}},
			Syntax:   parser.PostfixCast, Pos: parser.Position{Offset: 17, Character: 18},
		}},
	}
	first := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	bound, err := first.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	key := analyze.PreparedTypeKey{Offset: callPos.Offset, Kind: analyze.TypeNameCast}
	captured, ok := first.Prepared.Types[key]
	if !ok || captured.Kind != analyze.TypeNameCast || captured.Base != bound.ResultUsage() {
		t.Fatal("successful typename selection not captured")
	}
	view := e.CatalogRead()
	expected := &StatementDescription{
		Parameters:   analyze.ParameterInfo{Count: 1, Types: []sqltype.Usage{first.Type(sqltype.Text).Usage()}},
		catalogOwner: view.Owner(), catalogEpoch: view.Epoch(), types: first.Prepared.Types, analysis: &preparedAnalysis{},
	}
	next := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
	node, handled, err := next.BindSQLCallable(raw)
	if err != nil || !handled || node.ResultUsage() != captured.Base {
		t.Fatal("retained typename assembly", handled, err)
	}
	parameters := 0
	plan.WalkExpression(node, func(n expr.Expr) {
		if p, ok := n.(plan.ParameterExpr); ok {
			parameters++
			if p.Number != 1 || p.SourcePosition() != paramPos || p.ResultUsage() != first.Type(sqltype.Text).Usage() {
				t.Fatal("parameter/source authority changed", p)
			}
		}
	})
	if parameters != 1 || next.Prepared.Types[key].Base != captured.Base {
		t.Fatal("retained operand/capture lost")
	}
	sibling := raw
	sibling.Pos = parser.Position{Offset: 40, Character: 41}
	if _, handled, err := next.BindSQLCallable(sibling); err != nil || handled {
		t.Fatal("sibling borrowed retained target", handled, err)
	}
	foreign := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, expected)
	if _, handled, err := foreign.BindSQLCallable(raw); err != nil || handled {
		t.Fatal("foreign owner reused retained target", handled, err)
	}
	bad := *expected
	bad.types = map[analyze.PreparedTypeKey]analyze.TypeOccurrence{key: {Kind: analyze.TypeNameCast, Base: first.Type(sqltype.Circle).Usage()}}
	invalid := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, &bad)
	if _, handled, err := invalid.BindSQLCallable(raw); err == nil || !handled {
		t.Fatal("mismatched target authority admitted")
	}
	expected.retireAnalysisTypeCasts()
	retired := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
	if _, handled, err := retired.BindSQLCallable(raw); err != nil || handled {
		t.Fatal("retired analysis occurrence revived", handled, err)
	}
}
