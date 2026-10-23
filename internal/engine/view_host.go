package engine

import (
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ddlHost implements view.Host and view.StageHost. The decisions made before
// a stage exists get a ddlHost{e, op} with no stage: none of the view.Host
// methods reads it. ResolveRelation (role_host.go), StageRowType and
// DisplaceAssociatedArray (typedef_host.go) and BindPolicyExpression
// (policy_expression.go) are shared with the other feature hosts.
var (
	_ view.Host      = ddlHost{}
	_ view.StageHost = ddlHost{}
)

func (h ddlHost) TemporaryOwner(a ddl.Authority) ids.SessionToken {
	return catalogSourceTemporaryOwner(ddlAuthorityOf(a))
}

func (h ddlHost) DescribeRule(a ddl.Authority, stmt parser.Statement, target ids.ObjectRef, input sqltype.InputEnvironment, ns *analyze.RuleNamespace) (analyze.Binding, error) {
	return describeWithRule(h.op.Context(), h.e.analysisHost(h.op), stmt, ddlAuthorityOf(a), target, input, nil, nil, ns)
}

func (h ddlHost) BindRuleCondition(a ddl.Authority, ns *analyze.RuleNamespace, raw parser.Expr, input sqltype.InputEnvironment) (expr.Expr, *parser.Select, analyze.Binding, error) {
	auth := ddlAuthorityOf(a)
	binder := newExpressionBinder(h.op.Context(), h.e, input, h.op, nil)
	binder.Target.RuleNamespace = ns
	binder.Params.NoParameters = true
	binder.Query.Auth = &auth
	binder.Clause.AggregateClause = "WHERE"
	binder.Query.Frame = 1
	binder.Query.NextQueryFrame = 1
	condition, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.FilterContext})
	if err != nil {
		return condition, nil, analyze.Binding{}, err
	}
	normalized := binder.NormalizeStoredExpression(raw)
	if err := binder.CaptureViewLiterals(normalized, condition, analyze.ViewTargetLiteral, 0); err != nil {
		return condition, nil, analyze.Binding{}, err
	}
	normalized = binder.NormalizeStoredExpression(normalized)
	query := &parser.Select{Targets: []parser.Expr{normalized}}
	b := analyze.Binding{Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "condition", Usage: condition.ResultUsage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{condition}, Frame: binder.FrameID(), Syntax: query}}, Dependencies: binder.Dependencies, QuerySources: binder.Query.Sources, Capture: analyze.ViewCapture{Columns: binder.Capture.Columns, Literals: binder.Capture.Literals, CastUsages: binder.Capture.CastUsages, Composites: binder.Capture.Composites, Collations: binder.Capture.Collations, Namespaces: binder.Capture.Namespaces}, Prepared: analyze.PreparedCaptures{SequenceConstants: binder.Prepared.SequenceConstants, RegtypeConstants: binder.Prepared.RegtypeConstants}}
	b.Capture.RoutineRefs = binder.Capture.RoutineRefs
	b.Capture.OperatorRefs = binder.Capture.OperatorRefs
	for i := range b.QuerySources {
		b.QuerySources[i].FrozenAttributes = slices.Clone(b.QuerySources[i].Relation.Attributes)
	}
	return condition, query, b, nil
}

func (h ddlHost) ReownDisplacedTypes() error {
	if h.stage.OldNameCount() == 0 {
		return nil
	}
	return h.e.reownStagedTypeGenerationsLocked(h.stage)
}
