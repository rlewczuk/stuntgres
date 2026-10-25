package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestGeometryConversionCTypeNameCapture(t *testing.T) {
	e := engine.New()
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
	expected := core.NewDescription(core.DescriptionFields{
		Parameters:   analyze.ParameterInfo{Count: 1, Types: []sqltype.Usage{first.Type(sqltype.Text).Usage()}},
		CatalogOwner: view.Owner(), CatalogEpoch: view.Epoch(), Types: first.Prepared.Types, Analysis: core.NewPreparedAnalysis(core.PreparedAnalysisFields{}),
	})
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
	if node, handled, err := next.BindSQLCallable(sibling); err != nil || !handled || node.ResultUsage() != captured.Base || next.Prepared.Types[analyze.PreparedTypeKey{Offset: 40, Kind: analyze.TypeNameCast}].Base != captured.Base {
		t.Fatal("sibling did not acquire its own occurrence", handled, err)
	}
	foreign := newTestExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, expected)
	if node, handled, err := foreign.BindSQLCallable(raw); err != nil || !handled || node.ResultUsage().Type != foreign.Type(sqltype.Polygon) || node.ResultUsage().Type == captured.Base.Type {
		t.Fatal("foreign owner reused retained target", handled, err)
	}
	bad := *expected
	core.SetDescriptionTypes(&bad, map[analyze.PreparedTypeKey]analyze.TypeOccurrence{key: {Kind: analyze.TypeNameCast, Base: first.Type(sqltype.Circle).Usage()}})
	invalid := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, &bad)
	if _, handled, err := invalid.BindSQLCallable(raw); err == nil || !handled {
		t.Fatal("mismatched target authority admitted")
	}
	core.DescriptionRetireAnalysisTypeCasts(expected)
	retired := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
	if node, handled, err := retired.BindSQLCallable(raw); err != nil || !handled || node.ResultUsage() != retired.Type(sqltype.Polygon).Usage() {
		t.Fatal("retired occurrence failed fresh selection", handled, err)
	}
}
