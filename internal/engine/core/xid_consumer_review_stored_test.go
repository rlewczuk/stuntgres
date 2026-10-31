package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestIdentifierPublicDeparseLeaves(t *testing.T) {
	ctx := context.Background()
	eng := engine.New()
	view := eng.CatalogRead()
	for _, tc := range []struct {
		id        sqltype.ID
		max, name string
	}{{sqltype.Xid, "4294967295", "xid"}, {sqltype.Cid, "4294967295", "cid"}, {sqltype.Xid8, "18446744073709551615", "xid8"}} {
		typ, ok := view.LookupType(tc.id)
		if !ok {
			t.Fatal(tc.id)
		}
		value, err := typ.Input(ctx, tc.max, -1)
		if err != nil {
			t.Fatal(err)
		}
		node := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
		for _, pretty := range []bool{false, true} {
			renderer := stored.Deparser{View: view, Pretty: pretty}
			got, err := renderer.Literal(ctx, node)
			if err != nil || got != "'"+tc.max+"'::"+tc.name {
				t.Fatal(got, err)
			}
			null := node
			null.Value = sqltype.Null(typ)
			got, err = renderer.Literal(ctx, null)
			if err != nil || got != "NULL::"+tc.name {
				t.Fatal(got, err)
			}
		}
	}
	b := enginetest.ExpressionBinder(t, ctx, eng, sqltype.InputEnvironment{}, nil)
	parsed, err := parser.Parse("SELECT ('4294967297'::xid8)::xid")
	if err != nil {
		t.Fatal(err)
	}
	node, err := b.Bind(parsed[0].(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	got, err := (stored.Deparser{View: view}).SQL(ctx, node, stored.DeparseParent{})
	if err != nil || got != "('4294967297'::xid8)::xid" {
		t.Fatal("actual conversion tree lost", got, err)
	}
}
