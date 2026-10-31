package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowNtileGraphAndMetadata(t *testing.T) {
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT id,ntile(id) OVER w,ntile(id+1) OVER w,ntile((SELECT 2)) OVER w FROM t WINDOW w AS(ORDER BY id)")
	defer source.Close()
	node := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	for i, column := range query.Root.Schema() {
		if i == 0 {
			if column.OriginTable == 0 || column.OriginAttribute == 0 {
				t.Fatal("source origin lost", column)
			}
			continue
		}
		if !column.Nullable || !column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 || column.Usage.Typmod != -1 || column.Usage.Type.ID() != sqltype.Int4 {
			t.Fatal("computed INT4/OID23", column)
		}
	}
	if len(node.Calls) != 3 {
		t.Fatal(node.Calls)
	}
	binder := &analyze.Binder{}
	if binder.SameWindowCall(node.Calls[0], node.Calls[1]) || binder.SameWindowCall(node.Calls[1], node.Calls[2]) {
		t.Fatal("different bucket arguments collapsed")
	}
	call := node.Calls[0]
	found := false
	plan.QueryExpressions(node, func(expr expr.Expr) { found = found || expr == call.Argument })
	if !found {
		t.Fatal("bucket missing from graph")
	}
	replacement := node.Calls[1].Argument
	next, err := plan.RewriteQueryExpressions(node, func(expr expr.Expr) (expr.Expr, bool, error) {
		if expr == call.Argument {
			return replacement, true, nil
		}
		return expr, false, nil
	})
	if err != nil || next == node || next.(*plan.WindowNode).Calls[0].Argument != replacement || node.Calls[0].Argument != call.Argument {
		t.Fatal("immutable bucket rewrite", next, err)
	}
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	mapped, err := stored.RemapViewQuery(engine.New().CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	remapped := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode).Calls[0]
	if remapped.Argument == call.Argument || remapped.Expression == call.Expression || remapped.Argument.ResultUsage().Type != types[sqltype.Int4] || remapped.Expression.ResultUsage().Type != types[sqltype.Int4] {
		t.Fatal("bucket/result remap ownership", remapped)
	}
}

func TestQueryWindowNtileBindingUnwind(t *testing.T) {
	for _, tc := range []struct{ q, code string }{
		{"SELECT ntile(ntile(2) OVER(),1) OVER()", "42883"},
		{"SELECT ntile(ntile(2) OVER()) OVER()", "42P20"},
		{"SELECT ntile(ntile(2) OVER()) FILTER(WHERE TRUE) OVER()", "0A000"},
		{"SELECT ntile(missing) OVER()", "42703"},
	} {
		b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
		b.Clause.AggregateClause, b.Query.Frame = "SELECT", 1
		for _, q := range []string{"SELECT sum(1)", "SELECT row_number() OVER()"} {
			if _, err := b.Bind(enginetest.Statement(t, q).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err != nil {
				t.Fatal(err)
			}
		}
		before := b.Grouping.WindowCalls[:len(b.Grouping.WindowCalls):len(b.Grouping.WindowCalls)]
		b.Grouping.WindowCalls = before
		values := append([]plan.WindowCall(nil), before...)
		aggregates := b.Grouping.Aggregates[:len(b.Grouping.Aggregates):len(b.Grouping.Aggregates)]
		b.Grouping.Aggregates = aggregates
		_, err := b.Bind(enginetest.Statement(t, tc.q).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal(tc, err)
		}
		if len(b.Grouping.WindowCalls) != len(before) || cap(b.Grouping.WindowCalls) != cap(before) || &b.Grouping.WindowCalls[0] != &before[0] || !reflect.DeepEqual(values, b.Grouping.WindowCalls) || b.Grouping.ArgumentOwner != (analyze.ArgumentWindowOwner{}) {
			t.Fatal("failed call changed window registration/owner", tc, b.Grouping.WindowCalls, b.Grouping.ArgumentOwner)
		}
		if len(b.Grouping.Aggregates) != len(aggregates) || cap(b.Grouping.Aggregates) != cap(aggregates) || &b.Grouping.Aggregates[0] != &aggregates[0] {
			t.Fatal("failed call changed aggregate registration", tc)
		}
	}
}
