package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowOffsetTarget(t *testing.T) {
	for _, tc := range []struct {
		size, current int
		offset        int32
		reverse       bool
		want          int
		present       bool
	}{
		{4, 0, 1, false, 1, true}, {4, 0, 1, true, 0, false}, {4, 3, 1, false, 0, false}, {4, 3, 1, true, 2, true},
		{4, 2, 0, false, 2, true}, {4, 2, 0, true, 2, true}, {4, 2, -1, false, 1, true}, {4, 2, -1, true, 3, true},
		{4, 0, -1, false, 0, false}, {4, 3, -1, true, 0, false},
		{4, 2, math.MinInt32, false, 0, false}, {4, 2, math.MinInt32, true, 0, false},
		{4, 2, math.MaxInt32, false, 0, false}, {4, 2, math.MaxInt32, true, 0, false},
		{math.MaxInt, math.MaxInt - 1, 1, false, 0, false}, {math.MaxInt, math.MaxInt - 1, 1, true, math.MaxInt - 2, true},
		{math.MaxInt, math.MaxInt - 1, math.MinInt32, true, 0, false},
		{math.MaxInt, math.MaxInt - 1, math.MinInt32, false, math.MaxInt - 1 - 2147483648, true},
	} {
		got, present := windowOffsetTarget(tc.size, tc.current, tc.offset, tc.reverse)
		if present != tc.present || present && got != tc.want {
			t.Fatalf("%+v => %d/%v", tc, got, present)
		}
	}
	for _, op := range []windowOperation{windowLead, windowLag} {
		if op.frameDependent() || !op.evaluatesArgument() {
			t.Fatal("offset demand classification", op)
		}
	}
}

func TestQueryWindowOffsetGraph(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT lead(v,id,id+1) OVER w,lead(v,id,id+2) OVER w FROM t WINDOW w AS(ORDER BY id)")
	defer source.close()
	node := query.Root.(*projectNode).child.(*windowNode)
	if len(node.calls) != 2 {
		t.Fatal(node.calls)
	}
	first, second := node.calls[0], node.calls[1]
	if (&expressionBinder{}).sameWindowCall(first, second) {
		t.Fatal("different defaults matched")
	}
	var found [3]bool
	queryExpressions(node, func(e expr.Expr) {
		for i, child := range [3]expr.Expr{first.argument, first.ordinal, first.defaultValue} {
			found[i] = found[i] || e == child
		}
	})
	if found != [3]bool{true, true, true} {
		t.Fatal("missing window child", found)
	}
	next, err := rewriteQueryExpressions(node, func(e expr.Expr) (expr.Expr, bool, error) {
		if e == first.defaultValue {
			return second.defaultValue, true, nil
		}
		return e, false, nil
	})
	if err != nil || next == node || next.(*windowNode).calls[0].defaultValue != second.defaultValue || node.calls[0].defaultValue != first.defaultValue {
		t.Fatal("mutable/default rewrite", next, err)
	}
	for _, column := range query.Root.Schema() {
		if !column.Nullable || !column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 || column.Usage.Typmod != -1 {
			t.Fatal("computed descriptor", column)
		}
	}
	t.Run("grouped-default", func(t *testing.T) {
		_, grouped, input := queryCoreFixture(t, "SELECT lead(sum(v),0,sum(id)) OVER() FROM t")
		defer input.close()
		fallback := grouped.Root.(*projectNode).child.(*windowNode).calls[0].defaultValue
		if _, ok := fallback.(groupExpression); !ok {
			t.Fatalf("default not rewritten to owned group slot: %T", fallback)
		}
	})
	t.Run("matched-order-default", func(t *testing.T) {
		_, matched, input := queryCoreFixture(t, "SELECT lead(v,0,(SELECT max(id) FROM t)) OVER() AS x FROM t ORDER BY lead(v,0,(SELECT max(id) FROM t)) OVER()")
		defer input.close()
		project := matched.Root.(*projectNode)
		found := false
		for _, bound := range project.analysisExpressions {
			walkBoundExpression(bound, func(child expr.Expr) {
				if _, ok := child.(subqueryExpression); ok {
					found = true
				}
			})
		}
		if !found {
			t.Fatal("matched ORDER lost independently analyzed default scalar child")
		}
	})
	// Binding failure must unwind nested registrations, not leak a third-child call.
	for _, tc := range []struct{ q, code string }{
		{"SELECT lead(1,1::bigint,lag(1) OVER()) OVER()", "42883"},
		{"SELECT lead(1,1,lag(1) OVER()) OVER()", "42P20"},
		{"SELECT lead(1,0,missing) OVER()", "42703"},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		b.clause.aggregateClause, b.query.frame = "SELECT", 1
		if _, err := b.bind(statement(t, "SELECT row_number() OVER()").(*parser.Select).Targets[0], expressionPolicy{context: projectionContext}); err != nil {
			t.Fatal(err)
		}
		before := b.grouping.windowCalls[:len(b.grouping.windowCalls):len(b.grouping.windowCalls)]
		b.grouping.windowCalls = before
		values := append([]windowCall(nil), before...)
		_, err := b.bind(statement(t, tc.q).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code || len(b.grouping.windowCalls) != len(before) || cap(b.grouping.windowCalls) != cap(before) || &b.grouping.windowCalls[0] != &before[0] || !reflect.DeepEqual(values, b.grouping.windowCalls) || b.grouping.argumentOwner != (argumentWindowOwner{}) {
			t.Fatal("registration unwind", tc.q, err, b.grouping.windowCalls)
		}
	}
}

func TestQueryWindowOffsetOwnership(t *testing.T) {
	e := New()
	run(t, e, "CREATE DOMAIN window_offset_domain AS INT")
	run(t, e, "CREATE TABLE t(id INT)")
	ast := statement(t, "SELECT lead(id::window_offset_domain,0,(id+1)::window_offset_domain) OVER(ORDER BY id) FROM t")
	ref := roleTableRef(t, e, "t")
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	description, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := description.query.Root
	original := root.(*projectNode).child.(*windowNode).calls[0]
	domain := original.argument.ResultUsage().Type
	if !domain.IsDomain() || original.defaultValue.ResultUsage().Type != domain || original.expression.ResultUsage().Type != domain {
		t.Fatal("common domain lost", original)
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
	mapped, err := remapViewQuery(New().CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, lookup)
	if err != nil {
		t.Fatal(err)
	}
	next := mapped.(*projectNode).child.(*windowNode).calls[0]
	if next.argument == original.argument || next.ordinal == original.ordinal || next.defaultValue == original.defaultValue || next.expression == original.expression || next.argument.ResultUsage().Type != types[domain.ID()] || next.defaultValue.ResultUsage().Type != types[domain.ID()] || next.expression.ResultUsage().Type != types[domain.ID()] || !queryDescriptorsChanged(root, lookup) || queryDescriptorsChanged(mapped, lookup) {
		t.Fatal("source-owned default/result", next)
	}
}

func TestQueryWindowOffsetCancellation(t *testing.T) {
	for _, phase := range []string{"offset", "value", "default"} {
		t.Run(phase, func(t *testing.T) {
			offset := "0"
			if phase == "default" {
				offset = "100"
			}
			_, query, source := queryCoreFixture(t, "SELECT lead(v,"+offset+",id) OVER(ORDER BY id) FROM t")
			call := &query.Root.(*projectNode).child.(*windowNode).calls[0]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := call.ordinal
			if phase == "value" {
				original = call.argument
			} else if phase == "default" {
				original = call.defaultValue
			}
			entered := false
			probe := queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
				value, err := original.Eval(c)
				entered = true
				cancel()
				return value, err
			})
			switch phase {
			case "offset":
				call.ordinal = probe
			case "value":
				call.argument = probe
			case "default":
				call.defaultValue = probe
			}
			rows, count, err := finalizeQuery(ctx, query.Root, source, nil)
			if rows != nil {
				rows.Close()
			}
			var d *sqlstate.Error
			if !entered || rows != nil || count != 0 || !errors.Is(err, context.Canceled) || errors.As(err, &d) || source.view != nil {
				t.Fatal("canceled offset window published/leaked", entered, count, err, source.view)
			}
		})
	}
}

func TestQueryWindowOffsetEvaluationRoles(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT lead(v,CASE WHEN id<4 THEN 4-id ELSE 100 END,id) OVER(ORDER BY id) FROM t")
	call := &query.Root.(*projectNode).child.(*windowNode).calls[0]
	argument, offset, fallback := call.argument, call.ordinal, call.defaultValue
	var current, selected, defaults []int64
	call.ordinal = queryProbe(offset.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		current = append(current, id)
		return offset.Eval(c)
	})
	call.argument = queryProbe(argument.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		selected = append(selected, id)
		return argument.Eval(c)
	})
	call.defaultValue = queryProbe(fallback.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		defaults = append(defaults, id)
		return fallback.Eval(c)
	})
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if count != 4 || !reflect.DeepEqual(current, []int64{1, 2, 3, 4}) || !reflect.DeepEqual(selected, []int64{4, 4, 4}) || !reflect.DeepEqual(defaults, []int64{4}) {
		t.Fatal("lazy role ownership/repeated selection", count, current, selected, defaults)
	}
}
