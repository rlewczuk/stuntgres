package enginetests_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestArrayTransformResolverOwners(t *testing.T) {
	ctx := context.Background()
	source, target := engine.New(), engine.New()
	b := enginetest.ExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	tb := enginetest.ExpressionBinder(t, ctx, target, sqltype.InputEnvironment{}, nil)
	integer := func(n int64) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
	}
	text := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	for _, tc := range []struct {
		name, want string
		args       []parser.Expr
	}{
		{"array_append", "{2,1,3}", []parser.Expr{arrayInspectUnitArray("{2,1}"), integer(3)}},
		{"array_prepend", "{3,2,1}", []parser.Expr{integer(3), arrayInspectUnitArray("{2,1}")}},
		{"array_cat", "{2,1,3}", []parser.Expr{arrayInspectUnitArray("{2,1}"), arrayInspectUnitArray("{3}")}},
		{"array_fill", "{7,7}", []parser.Expr{integer(7), arrayInspectUnitArray("{2}")}},
		{"array_reverse", "{1,2}", []parser.Expr{arrayInspectUnitArray("{2,1}")}},
		{"array_sort", "{1,2}", []parser.Expr{arrayInspectUnitArray("{2,1}")}},
		{"trim_array", "{2}", []parser.Expr{arrayInspectUnitArray("{2,1}"), integer(1)}},
		{"array_to_string", "2,1", []parser.Expr{arrayInspectUnitArray("{2,1}"), text(",")}},
	} {
		raw := arrayInspectUnitRaw(tc.name, tc.args...)
		if err := dump.RestoreScalar(raw, false); err != nil {
			t.Fatal(tc.name, "restore INSERT admission", err)
		}
		withParameter := raw
		withParameter.Args = append([]parser.Expr(nil), raw.Args...)
		withParameter.Args[0] = parser.ParameterExpr{Number: 1}
		if err := dump.RestoreScalar(withParameter, false); err == nil {
			t.Fatal(tc.name, "restore admitted nested parameter")
		}
		node, err := b.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(tc.name, err)
		}
		old := node.(functions.ArrayTransform)
		render := func(call functions.ArrayTransform, view catalog.Read) string {
			s, err := stored.DefaultSQL(ctx, view, call)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
		originalSQL := render(old, source.CatalogRead())
		mapped, changed, err := stored.RetagTypes(target.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			if d == nil {
				return nil
			}
			return tb.Type(d.ID())
		})
		if err != nil || !changed {
			t.Fatal(tc.name, changed, err)
		}
		mapper := stored.ViewQueryRemapper{Catalog: target.CatalogRead(), Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: replacementProcedurePolicyFixture(source, target)}
		query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{mapped}}})
		if err != nil {
			t.Fatal(err)
		}
		mapped = query.(*plan.ValuesNode).Rows[0][0]
		next := mapped.(functions.ArrayTransform)
		if old.ResultUsage().Type == next.ResultUsage().Type || old.Args[0].ResultUsage().Type == next.Args[0].ResultUsage().Type {
			t.Fatal("retained source generations")
		}
		for _, call := range []functions.ArrayTransform{old, next} {
			if err := call.Validate(); err != nil {
				t.Fatal(err)
			}
			v, err := call.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
			if err != nil {
				t.Fatal(tc.name, err)
			}
			s, err := v.Type().Output(ctx, v)
			if err != nil || s != tc.want {
				t.Fatal(tc.name, s, err)
			}
			qualified := call
			qualified.Namespace = "pg_catalog"
			if !analyze.SameGroupExpression(call, qualified) || !analyze.SameGroupExpression(qualified, call) {
				t.Fatal("qualification altered builtin identity")
			}
			visited := 0
			plan.ExpressionChildren(call, func(expr.Expr) { visited++ })
			if visited != len(call.Args) || !analyze.ClosedTransactionTimeDefault(call) || !analyze.ClosedUUIDDefault(call) {
				t.Fatal("lost children", visited)
			}
			volatility := expr.Immutable
			if call.Kind == functions.ArrayToString {
				volatility = expr.Stable
			}
			if exec.PlannedPhysicalVolatility(call, false) != volatility || plan.GenerationImmutable(call) != (volatility == expr.Immutable) {
				t.Fatal("lost intrinsic volatility")
			}
			if _, _, err := exec.SimplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, call, false); err != nil {
				t.Fatal(err)
			}
			if _, _, err := exec.PreflightCoercionsWithPolicy(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, call, false); err != nil {
				t.Fatal(err)
			}
		}
		if render(old, source.CatalogRead()) != originalSQL || render(next, target.CatalogRead()) != originalSQL {
			t.Fatal("remap changed symbolic recipe")
		}
	}
}

func TestArrayTransformNamedAndConcatSelection(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	flag := parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}
	raw := arrayInspectUnitRaw("array_sort", flag, arrayInspectUnitArray("{2,NULL,1}"))
	raw.ArgNames = []parser.Identifier{{Text: "descending"}, {Text: "array"}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	call := node.(functions.ArrayTransform)
	if call.Slots != ([3]uint8{1, 0, 2}) || !analyze.SameGroupExpression(call.Formal(0), call.Args[1]) {
		t.Fatal(call.Slots)
	}
	sql, err := stored.DefaultSQL(ctx, e.CatalogRead(), call)
	if err != nil || !strings.Contains(sql, "\"descending\" =>") || !strings.Contains(sql, "\"array\" =>") {
		t.Fatal(sql, err)
	}
	v, err := call.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := v.Type().Output(ctx, v)
	if err != nil || s != "{NULL,2,1}" {
		t.Fatal(s, err)
	}
	var order []sqltype.ID
	_, changed, err := expr.MapSubstituted(call, func(child expr.Expr) (expr.Expr, bool, error) {
		order = append(order, child.ResultUsage().Type.ID())
		return child, false, nil
	})
	if err != nil || changed || len(order) != 2 || order[0] != call.Formal(0).ResultUsage().Type.ID() || order[1] != sqltype.Bool {
		t.Fatal("preflight lost formal child precedence", order, err)
	}
	rawOperator := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "||"}}}, Args: []parser.Expr{arrayInspectUnitArray("{1,2}"), parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}}
	node, err = b.BindStringConcat(rawOperator)
	if err != nil {
		t.Fatal(err)
	}
	cat := node.(functions.ArrayTransform)
	if !cat.Operator || cat.Kind != functions.ArrayConcatenate {
		t.Fatal(cat)
	}
	v, err = cat.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(err)
	}
	s, err = v.Type().Output(ctx, v)
	if err != nil || s != "{1,2}" {
		t.Fatal(s, err)
	}
	if _, err := stored.DefaultSQL(ctx, e.CatalogRead(), cat); err != nil {
		t.Fatal(err)
	}
}

func TestArrayTransformCollationRemap(t *testing.T) {
	ctx := context.Background()
	source, target := engine.New(), engine.New()
	b := enginetest.ExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	tb := enginetest.ExpressionBinder(t, ctx, target, sqltype.InputEnvironment{}, nil)
	array := b.Type(b.Type(sqltype.Text).AssociatedArray())
	usage := array.Usage()
	from, err := sqltype.RegisteredCollation(950, 950)
	if err != nil {
		t.Fatal(err)
	}
	to, err := sqltype.RegisteredCollation(951, 950)
	if err != nil {
		t.Fatal(err)
	}
	usage.Collation = from
	value, err := array.Input(ctx, "{b,a}", -1)
	if err != nil {
		t.Fatal(err)
	}
	arraySortPlan, err := sqltype.BindArraySort(array, from, false)
	if err != nil {
		t.Fatal(err)
	}
	old := functions.ArrayTransform{
		Info: b.Info(usage, parser.Position{}, expr.Properties{}),
		Kind: functions.ArraySort, Slots: [3]uint8{0, 1, 2}, Sort: arraySortPlan,
		Args: []expr.Expr{plan.LiteralExpr{Info: b.Info(usage, parser.Position{}, expr.Properties{}), Value: value}},
	}
	originalSQL, err := stored.DefaultSQL(ctx, source.CatalogRead(), old)
	if err != nil {
		t.Fatal(err)
	}
	mapped, changed, err := stored.RetagTypes(target.CatalogRead(), old,
		func(d *sqltype.Descriptor) *sqltype.Descriptor {
			if d == nil {
				return nil
			}
			return tb.Type(d.ID())
		}, func(ref sqltype.CollationRef) (sqltype.CollationRef, error) {
			if ref == from {
				return to, nil
			}
			return ref, nil
		})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	next := mapped.(functions.ArrayTransform)
	ref, conflict := next.Sort.InputCollation()
	if ref != to || conflict || next.Args[0].ResultUsage().Type == array || next.Args[0].ResultUsage().Collation != to {
		t.Fatal("stale remapped comparison authority", ref, conflict)
	}
	for _, call := range []functions.ArrayTransform{old, next} {
		if err := call.Validate(); err != nil {
			t.Fatal(err)
		}
		v, err := call.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		text, err := v.Type().Output(ctx, v)
		if err != nil || text != "{a,b}" {
			t.Fatal(text, err)
		}
	}
	if text, err := stored.DefaultSQL(ctx, target.CatalogRead(), next); err != nil || text == "" {
		t.Fatal(text, err)
	}
	if text, err := stored.DefaultSQL(ctx, source.CatalogRead(), old); err != nil || text != originalSQL {
		t.Fatal("original renderer changed", text, err)
	}
	ref, conflict = old.Sort.InputCollation()
	if ref != from || conflict {
		t.Fatal("source plan mutated")
	}
}

// replacementProcedurePolicyFixture supplies the real host policy for private
// address/type remap fixtures; it does not manufacture numeric principal links.
func replacementProcedurePolicyFixture(source, target *core.Engine) catalog.ProcedureRemapper {
	sourceRoles := source.RoleStore.PublishedRead(&source.Catalog.Owner, source.Catalog.Current())
	targetRoles := target.RoleStore.PublishedRead(&target.Catalog.Owner, target.Catalog.Current())
	return catalog.NewProcedureRemapper(target.RoleStore.PrincipalRemap(&target.Catalog, sourceRoles, targetRoles, nil, routine.OperatorSeeded))
}
