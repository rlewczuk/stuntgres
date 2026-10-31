package binder_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowValueGraphAndMetadata(t *testing.T) {
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT first_value(v) OVER w,nth_value(v,id) OVER w,nth_value(v,id+1) OVER w FROM t WINDOW w AS(ORDER BY id ROWS CURRENT ROW)")
	defer source.Close()
	node := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	for _, column := range query.Root.Schema() {
		if !column.Nullable || !column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 || column.Usage.Typmod != -1 || column.Usage.Type.ID() != sqltype.Int4 {
			t.Fatal(column)
		}
	}
	if len(node.Calls) != 3 || node.Calls[1].Ordinal == nil || node.Calls[2].Ordinal == nil {
		t.Fatal("value/ordinal identity", node.Calls)
	}
	binder := &analyze.Binder{}
	if binder.SameWindowCall(node.Calls[1], node.Calls[2]) {
		t.Fatal("different ordinals collapsed")
	}
	call := node.Calls[1]
	foundValue, foundOrdinal := false, false
	plan.QueryExpressions(node, func(expr expr.Expr) {
		foundValue = foundValue || expr == call.Argument
		foundOrdinal = foundOrdinal || expr == call.Ordinal
	})
	if !foundValue || !foundOrdinal {
		t.Fatal("child missing from graph", foundValue, foundOrdinal)
	}
	replacement := node.Calls[2].Ordinal
	next, err := plan.RewriteQueryExpressions(node, func(expr expr.Expr) (expr.Expr, bool, error) {
		if expr == call.Ordinal {
			return replacement, true, nil
		}
		return expr, false, nil
	})
	if err != nil || next == node || next.(*plan.WindowNode).Calls[1].Ordinal != replacement || node.Calls[1].Ordinal != call.Ordinal {
		t.Fatal("immutable ordinal rewrite", next, err)
	}
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	mapped, err := stored.RemapViewQuery(engine.New().CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	remapped := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode).Calls[1]
	if remapped.Ordinal == call.Ordinal || remapped.Argument == call.Argument || remapped.Expression == call.Expression || remapped.Ordinal.ResultUsage().Type != types[sqltype.Int4] || remapped.Expression.ResultUsage().Type != types[sqltype.Int4] {
		t.Fatal("child/result descriptor ownership", remapped)
	}
}

func TestQueryWindowValueBindingUnwind(t *testing.T) {
	for _, tc := range []struct{ q, code string }{
		{"SELECT nth_value(first_value(1) OVER(),1::bigint) OVER()", "42883"},
		{"SELECT nth_value(first_value(1) OVER(),1) OVER()", "42P20"},
		{"SELECT first_value(last_value(1) OVER()) FILTER(WHERE TRUE) OVER()", "0A000"},
		{"SELECT nth_value(1,missing) OVER()", "42703"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
			b.Clause.AggregateClause, b.Query.Frame = "SELECT", 1
			seed := enginetest.Statement(t, "SELECT row_number() OVER()").(*parser.Select).Targets[0]
			if _, err := b.Bind(seed, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err != nil {
				t.Fatal(err)
			}
			before := b.Grouping.WindowCalls[:len(b.Grouping.WindowCalls):len(b.Grouping.WindowCalls)]
			b.Grouping.WindowCalls = before
			values := append([]plan.WindowCall(nil), before...)
			_, err := b.Bind(enginetest.Statement(t, tc.q).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != tc.code {
				t.Fatal(err)
			}
			if len(b.Grouping.WindowCalls) != len(before) || cap(b.Grouping.WindowCalls) != cap(before) || &b.Grouping.WindowCalls[0] != &before[0] || !reflect.DeepEqual(values, b.Grouping.WindowCalls) || b.Grouping.ArgumentOwner != (analyze.ArgumentWindowOwner{}) {
				t.Fatal("failed call changed registration/owner", b.Grouping.WindowCalls, b.Grouping.ArgumentOwner)
			}
		})
	}
}

func TestQueryWindowValueDomainRemap(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE DOMAIN window_value_domain AS INT")
	enginetest.Run(t, e, "CREATE TABLE t(id INT)")
	ast := enginetest.Statement(t, "SELECT nth_value(id::window_value_domain,id::window_value_domain) OVER(ORDER BY id ROWS CURRENT ROW EXCLUDE TIES) FROM t")
	ref := enginetest.RoleTableRef(t, e, "t")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	description, err := core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := description.Query.Root
	original := root.(*plan.ProjectNode).Child.(*plan.WindowNode).Calls[0]
	domain := original.Argument.ResultUsage().Type
	if !domain.IsDomain() || original.Expression.ResultUsage().Type != domain {
		t.Fatal("polymorphic domain lost", original)
	}
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	types[domain.ID()], err = sqltype.Domain(domain.ID(), domain.DiagnosticName(), domain.SQLName(), types[domain.DomainBase().Type.ID()].Usage())
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] }
	mapped, err := stored.RemapViewQuery(engine.New().CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, lookup)
	if err != nil {
		t.Fatal(err)
	}
	node := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode)
	next := node.Calls[0]
	var ordinalDomain *sqltype.Descriptor
	plan.WalkExpression(next.Ordinal, func(expr expr.Expr) {
		if expr.ResultUsage().Type.IsDomain() {
			ordinalDomain = expr.ResultUsage().Type
		}
	})
	if next.Expression.ResultUsage().Type != types[domain.ID()] || next.Argument.ResultUsage().Type != types[domain.ID()] || ordinalDomain != types[domain.ID()] || ordinalDomain == domain || !analyze.QueryDescriptorsChanged(root, lookup) || analyze.QueryDescriptorsChanged(mapped, lookup) || node.Specs[0].Frame.Exclusion != parser.ExcludeTies {
		t.Fatal("value/ordinal/result ownership", next, ordinalDomain)
	}
}

func TestQueryWindowValueSortOwnerSlots(t *testing.T) {
	for _, tc := range []struct {
		order string
		calls int
		same  bool
	}{
		{" ORDER BY a", 1, true}, {" ORDER BY b", 1, true},
		{" ORDER BY id", 2, false}, {" ORDER BY a,b", 2, false},
		{"", 2, false},
	} {
		t.Run(tc.order, func(t *testing.T) {
			query, source := enginetest.QueryFixture(t, engine.New(), "SELECT first_value(nextval($1)) OVER w AS a,first_value(nextval($1)) OVER w AS b FROM t WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)"+tc.order)
			defer source.Close()
			project := query.Root.(*plan.ProjectNode)
			child := project.Child
			if sorted, ok := child.(*plan.SortNode); ok {
				child = sorted.Child
				if tc.order == " ORDER BY a,b" && (sorted.Keys[0].Cache == sorted.Keys[1].Cache || sorted.Keys[0].TargetIdentity == sorted.Keys[1].TargetIdentity) {
					t.Fatal("required target caches collapsed", sorted.Keys)
				}
			}
			node := child.(*plan.WindowNode)
			if len(node.Calls) != tc.calls {
				t.Fatal("executing target owners", len(node.Calls), tc.calls)
			}
			if tc.order == " ORDER BY b" && node.Calls[0].Expression.Call != 1 {
				t.Fatal("later demanded ordinal did not retain its own executing slot", node.Calls[0].Expression)
			}
			first, second := analyze.WindowOccurrence(project.Expressions[0]), analyze.WindowOccurrence(project.Expressions[1])
			// Demanded projections may read ORDER caches instead of a leaf.
			if first != nil && second != nil && (first.Slot == second.Slot) != tc.same {
				t.Fatal("slot ownership", first, second)
			}
		})
	}
	t.Run("retained-wrapper-is-not-a-leaf-owner", func(t *testing.T) {
		query, source := enginetest.QueryFixture(t, engine.New(), "SELECT first_value(nextval($1)) OVER w+1 AS a,first_value(nextval($1)) OVER w AS b FROM t WINDOW w AS(ORDER BY id) ORDER BY a")
		defer source.Close()
		node := query.Root.(*plan.ProjectNode).Child.(*plan.SortNode).Child.(*plan.WindowNode)
		if len(node.Calls) != 2 || node.Calls[0].Expression.Slot == node.Calls[1].Expression.Slot {
			t.Fatal("retained wrapper donated nested leaf", node.Calls)
		}
	})
}

func TestQueryWindowValueHiddenSortOwner(t *testing.T) {
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT first_value(nextval($1)) OVER w+1 AS a FROM t WINDOW w AS(ORDER BY id) ORDER BY first_value(nextval($1)) OVER w")
	defer source.Close()
	project := query.Root.(*plan.ProjectNode)
	node := project.Child.(*plan.SortNode).Child.(*plan.WindowNode)
	if len(project.Schema()) != 1 || len(node.Calls) != 1 || node.Calls[0].Expression.Call != 1 {
		t.Fatal("hidden whole owner lost or leaked into public schema", project.Schema(), node.Calls)
	}
}
