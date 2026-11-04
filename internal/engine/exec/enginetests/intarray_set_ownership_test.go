package enginetests_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIntarraySetValues(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION intarray")
	b := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	// The composed binder supplies the selected declaration, formals and source.
	raw := nullCountRaw("_int_union", intarrayOwnershipCast("{3,1,3,2}"), intarrayOwnershipCast("{2,4,2}"))
	bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	node, ok := bound.(functions.IntarraySet)
	if !ok {
		t.Fatalf("selected node %T", bound)
	}
	integer := node.Formals[0].Element()
	three, _ := sqltype.Integer(integer, 3)
	one, _ := sqltype.Integer(integer, 1)
	dimensions := []sqltype.ArrayDimension{{Lower: 0, Length: 3}}
	elements := []sqltype.Value{three, one, three}
	left, err := sqltype.ArrayValue(node.Formals[0], dimensions, elements)
	if err != nil {
		t.Fatal(err)
	}
	right, err := node.Formals[1].Input(t.Context(), "{2,4,2}", -1)
	if err != nil {
		t.Fatal(err)
	}
	node.Args = [2]expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: left.Type().Usage()}, Value: left}, plan.LiteralExpr{Info: expr.Info{Usage: right.Type().Usage()}, Value: right}}
	group := exec.FoldCaptureCandidate{}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Catalog: e.CatalogRead(), FoldCaptures: &group}}
	value, err := exec.FoldPlanningExpression(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	index := -1
	for i, entry := range group.Entries {
		if entry.Callable == node.RoutineReference {
			index = i
		}
	}
	if index < 0 || group.Entries[index].Value != value {
		t.Fatal("typed selected capture absent")
	}
	retained := group.Entries[index]
	backing := &group.Entries[0]
	dimensions[0].Lower = 99
	elements[0] = sqltype.Null(integer)
	if left.ArrayDimension(0).Lower != 0 || left.ArrayElement(0).IsNull() || value.ArrayDimension(0).Lower != 1 || value.ArrayLen() != 4 {
		t.Fatal("constructor scratch escaped into capture")
	}
	group.Private = false
	again, err := exec.FoldPlanningExpression(ctx, node)
	if err != nil || again != value || group.Private || &group.Entries[0] != backing || !enginetest.SamePublishedFold(retained, group.Entries[index]) {
		t.Fatal("accepted fold backing/provenance changed", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	failed := exec.FoldCaptureCandidate{}
	ctx.Context, ctx.FoldCaptures = cancelled, &failed
	if _, err := exec.FoldPlanningExpression(ctx, node); !errors.Is(err, context.Canceled) || failed.Sealed {
		t.Fatal("cancelled selected capture published", err)
	}
	if !enginetest.SamePublishedFold(retained, group.Entries[index]) || &group.Entries[0] != backing {
		t.Fatal("failed attempt changed accepted datum")
	}
}

func intarrayOwnershipCast(text string) parser.Expr {
	return parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}}, TypeName: parser.TypeName{Name: "integer", ArrayBounds: []int32{-1}}, Syntax: parser.PostfixCast}
}
