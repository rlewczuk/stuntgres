package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPGStatBGWriterConstantRejection(t *testing.T) {
	read := engine.New().CatalogRead()
	boolean, ok := read.LookupType(sqltype.Bool)
	if !ok {
		t.Fatal("missing bool")
	}
	info := expr.Info{Usage: boolean.Usage()}
	no := plan.LiteralExpr{Info: info, Value: sqltype.Boolean(boolean, false)}
	yes := plan.LiteralExpr{Info: info, Value: sqltype.Boolean(boolean, true)}
	null := plan.LiteralExpr{Info: info, Value: sqltype.Null(boolean)}
	parameter := plan.ParameterExpr{Info: info, Number: 1}
	domain := &ids.Domain{}
	ref := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12293})
	scan := func() *plan.SourceNode { return &plan.SourceNode{Ref: ref} }
	filtered := func(predicate expr.Expr) plan.Node { return &plan.FilterNode{Child: scan(), Predicate: predicate} }
	logical := func(op parser.LogicalOperator, left, right expr.Expr) expr.Expr {
		return plan.LogicalExpr{Info: info, Op: op, Left: left, Right: right}
	}
	sampled := scan()
	sampled.Sample = &plan.TableSample{}
	otherOccurrence := scan()
	otherOccurrence.Occurrence = 1
	aggregate := func(input plan.Node) *plan.AggregateNode {
		return &plan.AggregateNode{Child: input, Calls: []plan.AggregateCall{{Arguments: []expr.Expr{parameter}}}}
	}
	for _, tc := range []struct {
		name string
		root plan.Node
		want bool
	}{
		{"false", filtered(no), true},
		{"projected false", &plan.ProjectNode{Child: filtered(no)}, true},
		{"ordinary aggregate", aggregate(filtered(no)), true},
		{"projected aggregate", &plan.ProjectNode{Child: aggregate(filtered(no))}, true},
		{"grouped ordinary aggregate", &plan.AggregateNode{Child: filtered(no), Keys: []expr.Expr{parameter}}, true},
		{"aggregate ordinary FILTER and ordering", &plan.AggregateNode{Child: filtered(no), Calls: []plan.AggregateCall{{Arguments: []expr.Expr{parameter}, Filter: parameter, Order: []plan.SortKey{{Expression: parameter}}}}}, true},
		{"aggregate direct arguments", &plan.AggregateNode{Child: filtered(no), Calls: []plan.AggregateCall{{Direct: []expr.Expr{parameter}}}}, false},
		{"aggregate identity argument", &plan.AggregateNode{Child: filtered(no), Calls: []plan.AggregateCall{{Arguments: []expr.Expr{plan.SystemAttributeExpr{QueryOccurrence: 1}}}}}, false},
		{"aggregate subquery argument", &plan.AggregateNode{Child: filtered(no), Calls: []plan.AggregateCall{{Arguments: []expr.Expr{plan.SubqueryExpr{Info: info, QueryPlan: &plan.Query{Root: &plan.SingletonNode{}}}}}}}, false},
		{"aggregate TRUE input", aggregate(filtered(yes)), false},
		{"aggregate sampled input", aggregate(&plan.FilterNode{Child: sampled, Predicate: no}), false},
		{"first conjunct", filtered(logical(parser.LogicalAnd, no, parameter)), true},
		{"nested first conjunct", filtered(logical(parser.LogicalAnd, logical(parser.LogicalAnd, no, parameter), yes)), true},
		{"true", filtered(yes), false},
		{"NULL", filtered(null), false},
		{"parameter never evaluated", filtered(parameter), false},
		{"non-BOOL literal", filtered(plan.LiteralExpr{}), false},
		{"false after parameter", filtered(logical(parser.LogicalAnd, parameter, no)), false},
		{"OR", filtered(logical(parser.LogicalOr, no, no)), false},
		{"filter above projection", &plan.FilterNode{Child: &plan.ProjectNode{Child: scan()}, Predicate: no}, false},
		{"filter above join", &plan.FilterNode{Child: &plan.JoinNode{Left: scan(), Right: otherOccurrence}, Predicate: no}, false},
		{"filtered scan below join", &plan.JoinNode{Left: filtered(no), Right: otherOccurrence}, false},
		{"CTE boundary", &plan.ProjectNode{Child: filtered(no), CTEOwner: true}, false},
		{"rule invocation", &plan.ProjectNode{Child: filtered(no), RuleInvocation: true}, false},
		{"duplicate occurrence", &plan.JoinNode{Left: filtered(no), Right: scan()}, false},
		{"other occurrence", &plan.FilterNode{Child: otherOccurrence, Predicate: no}, false},
		{"sampling", &plan.FilterNode{Child: sampled, Predicate: no}, false},
		{"no filter", scan(), false},
		{"nil root", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := exec.CatalogSourceFalse(tc.root, 0, ref); got != tc.want {
				t.Fatal("literal-only occurrence proof", got, tc.want)
			}
		})
	}
	wrong := ref
	wrong.Epoch++
	if exec.CatalogSourceFalse(filtered(no), 0, wrong) || exec.CatalogSourceFalse(filtered(no), 1, ref) {
		t.Fatal("proof escaped captured identity/occurrence")
	}
}
