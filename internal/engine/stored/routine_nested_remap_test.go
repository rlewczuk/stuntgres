package stored

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Replacement addresses may also be keys: old 101 -> new 102 must not then
// become 103. Exercise the actual nested query and mutation recipe boundaries.
func TestRoutineNestedRemapExactlyOnce(t *testing.T) {
	var integer, bigint, floating *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		switch d.ID() {
		case sqltype.Int4:
			integer = d
		case sqltype.Int8:
			bigint = d
		case sqltype.Float8:
			floating = d
		}
	}
	if integer == nil || bigint == nil || floating == nil {
		t.Fatal("missing numeric descriptors")
	}
	nonidempotentType := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		switch d {
		case integer:
			return bigint
		case bigint:
			return floating
		default:
			return d
		}
	}
	ref := func(r ids.ObjectRef) ids.ObjectRef {
		if r.Address.ClassID == ids.RelationClassID && (r.Address.ObjectID == 101 || r.Address.ObjectID == 102) {
			r.Address.ObjectID++
		}
		return r
	}
	original := ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 101}}
	formal := plan.ParameterExpr{Info: expr.Info{Usage: integer.Usage()}, Number: 1}
	independent := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Usage: integer.Usage()}}, Child: &plan.SourceNode{Ref: ref(original)}, Expressions: []expr.Expr{formal}}
	deeper := plan.SubqueryExpr{Info: expr.Info{Usage: integer.Usage()}, QueryPlan: &plan.Query{Root: independent}}
	inner := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Usage: integer.Usage()}}, Child: &plan.SourceNode{Ref: original}, Expressions: []expr.Expr{formal}, AnalysisExpressions: []expr.Expr{deeper}}
	subquery := plan.SubqueryExpr{Info: expr.Info{Usage: integer.Usage()}, QueryPlan: &plan.Query{Root: inner}}
	check := func(t *testing.T, e expr.Expr) {
		t.Helper()
		root := e.(plan.SubqueryExpr).QueryPlan.Root.(*plan.ProjectNode)
		got := root.Child.(*plan.SourceNode).Ref
		want := ref(original)
		if got != want {
			t.Fatalf("nested identity remapped more than once: got %+v, want %+v", got, want)
		}
		if got := root.Expressions[0].(plan.ParameterExpr); got.Number != 1 || got.ResultUsage() != bigint.Usage() || root.Schema()[0].Usage != bigint.Usage() {
			t.Fatalf("formal changed: %+v", got)
		}
		deep := root.AnalysisExpressions[0].(plan.SubqueryExpr).QueryPlan.Root.(*plan.ProjectNode)
		if deep.Child.(*plan.SourceNode).Ref != ref(ref(original)) || deep.Expressions[0].ResultUsage() != bigint.Usage() || deep.Schema()[0].Usage != bigint.Usage() {
			t.Fatal("independent original B or nested T0 was not replaced exactly once")
		}
		if inner.Child.(*plan.SourceNode).Ref != original {
			t.Fatal("remap mutated original query")
		}
	}
	t.Run("query", func(t *testing.T) {
		mapper := &ViewQueryRemapper{Ref: ref, Type: nonidempotentType, routineFormals: 1}
		root, err := mapper.Nested(&plan.ProjectNode{Child: &plan.SingletonNode{}, Expressions: []expr.Expr{subquery, subquery}})
		if err != nil {
			t.Fatal(err)
		}
		check(t, root.(*plan.ProjectNode).Expressions[0])
		project := root.(*plan.ProjectNode)
		if project.Expressions[0].(plan.SubqueryExpr).QueryPlan.Root != project.Expressions[1].(plan.SubqueryExpr).QueryPlan.Root {
			t.Fatal("repeated original query lost its shared replacement identity")
		}
	})
	t.Run("mutation", func(t *testing.T) {
		binding := analyze.Binding{Update: &analyze.UpdateBinding{Assignments: []expr.Expr{subquery}}}
		mapped, err := remapRuleBinding(catalog.Read{}, binding, ref, nonidempotentType, nil, 1, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		check(t, mapped.Update.Assignments[0])
	})
	t.Run("generic-boundary", func(t *testing.T) {
		if _, err := RemapExpression(subquery, ref, nil); err == nil {
			t.Fatal("generic expression admitted nested routine formals")
		}
		binding := analyze.Binding{Update: &analyze.UpdateBinding{Assignments: []expr.Expr{subquery}}}
		if _, err := RemapRuleBinding(catalog.Read{}, binding, ref, nonidempotentType, nil, nil); err == nil {
			t.Fatal("generic mutation admitted nested routine formals")
		}
	})
}
