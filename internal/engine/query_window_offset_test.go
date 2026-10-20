package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
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
		got, present := exec.WindowOffsetTarget(tc.size, tc.current, tc.offset, tc.reverse)
		if present != tc.present || present && got != tc.want {
			t.Fatalf("%+v => %d/%v", tc, got, present)
		}
	}
	for _, op := range []plan.WindowOperation{plan.WindowLead, plan.WindowLag} {
		if op.FrameDependent() || !op.EvaluatesArgument() {
			t.Fatal("offset demand classification", op)
		}
	}
}

func TestQueryWindowOffsetGraph(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT lead(v,id,id+1) OVER w,lead(v,id,id+2) OVER w FROM t WINDOW w AS(ORDER BY id)")
	defer source.Close()
	node := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if len(node.Calls) != 2 {
		t.Fatal(node.Calls)
	}
	first, second := node.Calls[0], node.Calls[1]
	if (&expressionBinder{}).sameWindowCall(first, second) {
		t.Fatal("different defaults matched")
	}
	var found [3]bool
	plan.QueryExpressions(node, func(e expr.Expr) {
		for i, child := range [3]expr.Expr{first.Argument, first.Ordinal, first.DefaultValue} {
			found[i] = found[i] || e == child
		}
	})
	if found != [3]bool{true, true, true} {
		t.Fatal("missing window child", found)
	}
	next, err := plan.RewriteQueryExpressions(node, func(e expr.Expr) (expr.Expr, bool, error) {
		if e == first.DefaultValue {
			return second.DefaultValue, true, nil
		}
		return e, false, nil
	})
	if err != nil || next == node || next.(*plan.WindowNode).Calls[0].DefaultValue != second.DefaultValue || node.Calls[0].DefaultValue != first.DefaultValue {
		t.Fatal("mutable/default rewrite", next, err)
	}
	for _, column := range query.Root.Schema() {
		if !column.Nullable || !column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 || column.Usage.Typmod != -1 {
			t.Fatal("computed descriptor", column)
		}
	}
	t.Run("grouped-default", func(t *testing.T) {
		_, grouped, input := queryCoreFixture(t, "SELECT lead(sum(v),0,sum(id)) OVER() FROM t")
		defer input.Close()
		fallback := grouped.Root.(*plan.ProjectNode).Child.(*plan.WindowNode).Calls[0].DefaultValue
		if _, ok := fallback.(plan.GroupExpr); !ok {
			t.Fatalf("default not rewritten to owned group slot: %T", fallback)
		}
	})
	t.Run("matched-order-default", func(t *testing.T) {
		_, matched, input := queryCoreFixture(t, "SELECT lead(v,0,(SELECT max(id) FROM t)) OVER() AS x FROM t ORDER BY lead(v,0,(SELECT max(id) FROM t)) OVER()")
		defer input.Close()
		project := matched.Root.(*plan.ProjectNode)
		found := false
		for _, bound := range project.AnalysisExpressions {
			plan.WalkExpression(bound, func(child expr.Expr) {
				if _, ok := child.(plan.SubqueryExpr); ok {
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
		values := append([]plan.WindowCall(nil), before...)
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
	description, err := describe(op.ctx, e.analysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := description.query.Root
	original := root.(*plan.ProjectNode).Child.(*plan.WindowNode).Calls[0]
	domain := original.Argument.ResultUsage().Type
	if !domain.IsDomain() || original.DefaultValue.ResultUsage().Type != domain || original.Expression.ResultUsage().Type != domain {
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
	next := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode).Calls[0]
	if next.Argument == original.Argument || next.Ordinal == original.Ordinal || next.DefaultValue == original.DefaultValue || next.Expression == original.Expression || next.Argument.ResultUsage().Type != types[domain.ID()] || next.DefaultValue.ResultUsage().Type != types[domain.ID()] || next.Expression.ResultUsage().Type != types[domain.ID()] || !queryDescriptorsChanged(root, lookup) || queryDescriptorsChanged(mapped, lookup) {
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
			call := &query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode).Calls[0]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := call.Ordinal
			if phase == "value" {
				original = call.Argument
			} else if phase == "default" {
				original = call.DefaultValue
			}
			entered := false
			probe := queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
				value, err := original.Eval(c)
				entered = true
				cancel()
				return value, err
			})
			switch phase {
			case "offset":
				call.Ordinal = probe
			case "value":
				call.Argument = probe
			case "default":
				call.DefaultValue = probe
			}
			rows, count, err := exec.Finalize(ctx, query.Root, source, nil)
			if rows != nil {
				rows.Close()
			}
			var d *sqlstate.Error
			if !entered || rows != nil || count != 0 || !errors.Is(err, context.Canceled) || errors.As(err, &d) || source.View != nil {
				t.Fatal("canceled offset window published/leaked", entered, count, err, source.View)
			}
		})
	}
}

func TestQueryWindowOffsetEvaluationRoles(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT lead(v,CASE WHEN id<4 THEN 4-id ELSE 100 END,id) OVER(ORDER BY id) FROM t")
	call := &query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode).Calls[0]
	argument, offset, fallback := call.Argument, call.Ordinal, call.DefaultValue
	var current, selected, defaults []int64
	call.Ordinal = queryProbe(offset.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		current = append(current, id)
		return offset.Eval(c)
	})
	call.Argument = queryProbe(argument.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		selected = append(selected, id)
		return argument.Eval(c)
	})
	call.DefaultValue = queryProbe(fallback.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		defaults = append(defaults, id)
		return fallback.Eval(c)
	})
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if count != 4 || !reflect.DeepEqual(current, []int64{1, 2, 3, 4}) || !reflect.DeepEqual(selected, []int64{4, 4, 4}) || !reflect.DeepEqual(defaults, []int64{4}) {
		t.Fatal("lazy role ownership/repeated selection", count, current, selected, defaults)
	}
}
