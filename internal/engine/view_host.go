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
	_ view.Host      = DDLHost{}
	_ view.StageHost = DDLHost{}
)

func (h DDLHost) TemporaryOwner(a ddl.Authority) ids.SessionToken {
	return catalogSourceTemporaryOwner(DDLAuthorityOf(a))
}

func (h DDLHost) DescribeRule(a ddl.Authority, stmt parser.Statement, target ids.ObjectRef, input sqltype.InputEnvironment, ns *analyze.RuleNamespace) (analyze.Binding, error) {
	return describeWithRule(h.Op.Context(), h.E.AnalysisHost(h.Op), stmt, DDLAuthorityOf(a), target, input, nil, nil, ns)
}

func (h DDLHost) BindRuleCondition(a ddl.Authority, ns *analyze.RuleNamespace, raw parser.Expr, input sqltype.InputEnvironment) (expr.Expr, *parser.Select, analyze.Binding, error) {
	return h.bindPseudoRowCondition(a, ns, raw, input, "WHERE")
}

// BindTriggerWhen shares scalar capture/freeze inputs with rule conditions, but
// owns an explicit trigger namespace and WHEN diagnostic context.
func (h DDLHost) BindTriggerWhen(a ddl.Authority, ns *analyze.RuleNamespace, raw parser.Expr, input sqltype.InputEnvironment) (expr.Expr, *parser.Select, analyze.Binding, error) {
	return h.bindPseudoRowCondition(a, ns, raw, input, "trigger WHEN condition")
}

func (h DDLHost) bindPseudoRowCondition(a ddl.Authority, ns *analyze.RuleNamespace, raw parser.Expr, input sqltype.InputEnvironment, clause string) (expr.Expr, *parser.Select, analyze.Binding, error) {
	auth := DDLAuthorityOf(a)
	binder := NewExpressionBinder(h.Op.Context(), h.E, input, h.Op, nil)
	binder.Target.RuleNamespace = ns
	binder.Params.NoParameters = true
	binder.Query.Auth = &auth
	binder.Clause.AggregateClause = clause
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

func (h DDLHost) ReownDisplacedTypes() error {
	if h.CatalogStage.OldNameCount() == 0 {
		return nil
	}
	return h.E.ReownStagedTypeGenerationsLocked(h.CatalogStage)
}
