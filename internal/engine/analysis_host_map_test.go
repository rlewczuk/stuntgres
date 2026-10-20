package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// The host maps a bound tree with the binder's leaf rewrite and keeps types
// and query nodes: a rewritten leaf is copied in, untouched siblings and the
// input tree stay as they were, and a leaf error stops the walk.
func TestAnalysisHostMapsBoundExpressions(t *testing.T) {
	ctx := context.Background()
	e := New()
	view := e.CatalogRead()
	int4, ok := view.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing int4")
	}
	literal := func(text string) plan.LiteralExpr {
		value, err := int4.Input(ctx, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return plan.LiteralExpr{Info: expr.Info{Usage: int4.Usage()}, Value: value}
	}
	one, two, three := literal("1"), literal("2"), literal("3")
	row := plan.RowExpr{Fields: []expr.Expr{one, two}}
	isOne := func(node expr.Expr) bool {
		l, ok := node.(plan.LiteralExpr)
		return ok && reflect.DeepEqual(l, one)
	}
	host := e.analysisHost(nil)

	mapped, err := host.MapBoundExpression(view, nil, row, false, func(node expr.Expr) (expr.Expr, bool, error) {
		if isOne(node) {
			return three, true, nil
		}
		return node, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := mapped.(plan.RowExpr)
	if !ok || len(got.Fields) != 2 || !reflect.DeepEqual(got.Fields[0], three) || !reflect.DeepEqual(got.Fields[1], two) {
		t.Fatalf("mapped row = %#v, want fields 3, 2", mapped)
	}
	if !reflect.DeepEqual(row.Fields[0], one) {
		t.Fatal("mapping changed the input tree")
	}

	unchanged, err := host.MapBoundExpression(view, nil, row, false, func(node expr.Expr) (expr.Expr, bool, error) { return node, false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := unchanged.(plan.RowExpr); !ok || !reflect.DeepEqual(got.Fields, row.Fields) {
		t.Fatalf("identity mapping = %#v, want the input fields", unchanged)
	}

	failure := errors.New("leaf failure")
	if _, err := host.MapBoundExpression(view, nil, row, false, func(node expr.Expr) (expr.Expr, bool, error) {
		if isOne(node) {
			return nil, false, failure
		}
		return node, false, nil
	}); !errors.Is(err, failure) {
		t.Fatalf("leaf error = %v, want %v", err, failure)
	}
}
