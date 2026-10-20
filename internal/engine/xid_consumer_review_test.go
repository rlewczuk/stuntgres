package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIdentifierExtremaInitialization(t *testing.T) {
	ctx := context.Background()
	for _, typ := range []string{"xid", "cid"} {
		for _, function := range []string{"GREATEST", "LEAST"} {
			engine := New()
			b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
			parsed, err := parser.Parse("SELECT " + function + "(NULL::" + typ + ",NULL::" + typ + ")")
			if err != nil {
				t.Fatal(err)
			}
			bound, err := b.bind(parsed[0].(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal("describable extrema rejected at analysis", err)
			}
			node := bound.(conditionalExpression)
			if !node.MissingOrdering || node.Comparison != (sqltype.Comparison{}) {
				t.Fatal("missing ordering acquired executable comparator", node)
			}
			if _, err := (scalarRenderer{}).sql(ctx, engine.CatalogRead(), node); err != nil {
				t.Fatal("unavailable capability lost stored identity", err)
			}
			calls := 0
			node.Info.Property.RowDependent = true
			node.Args = []expr.Expr{conditionalSpy{Info: expr.Info{Usage: node.Usage, Property: expr.Properties{RowDependent: true}}, value: sqltype.Null(node.Usage.Type), calls: &calls, failure: errors.New("argument demanded")}, node.Args[1]}
			evaluation := &EvalContext{evalStatement: evalStatement{Context: ctx}}
			assert := func(err error) {
				t.Helper()
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Severity != "ERROR" || diagnostic.SeverityUnlocalized != "ERROR" || diagnostic.Code != "42883" || diagnostic.Message != "could not identify a comparison function for type "+typ || diagnostic.Position != "" || diagnostic.Hint != "" || diagnostic.Detail != "" || diagnostic.Context != "" || calls != 0 {
					t.Fatal("extrema initialization boundary", diagnostic, err, calls)
				}
			}
			_, _, err = preflightCoercions(evaluation, node)
			assert(err)
			_, _, err = simplifyConditional(evaluation, node, false)
			assert(err)
			_, err = node.Eval(evaluation)
			assert(err)
		}
	}
}

func TestIdentifierPublicDeparseLeaves(t *testing.T) {
	ctx := context.Background()
	engine := New()
	view := engine.CatalogRead()
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
		node := literalExpression{Info: expr.Info{Usage: typ.Usage()}, Value: value}
		for _, pretty := range []bool{false, true} {
			renderer := pgRenderer{view: view, pretty: pretty}
			got, err := renderer.literal(ctx, node)
			if err != nil || got != "'"+tc.max+"'::"+tc.name {
				t.Fatal(got, err)
			}
			null := node
			null.Value = sqltype.Null(typ)
			got, err = renderer.literal(ctx, null)
			if err != nil || got != "NULL::"+tc.name {
				t.Fatal(got, err)
			}
		}
	}
	b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	parsed, err := parser.Parse("SELECT ('4294967297'::xid8)::xid")
	if err != nil {
		t.Fatal(err)
	}
	node, err := b.bind(parsed[0].(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	got, err := (pgRenderer{view: view}).sql(ctx, node, pgRenderParent{})
	if err != nil || got != "('4294967297'::xid8)::xid" {
		t.Fatal("actual conversion tree lost", got, err)
	}
}
