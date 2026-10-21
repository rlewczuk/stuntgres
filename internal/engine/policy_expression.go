package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Engine.mu is held. Analysis shares the ordinary Boolean binder and frozen
// query presentation; no condition is evaluated and no row storage is opened.
func (e *Engine) bindPolicyExpression(scope *StatementScope, auth AuthorizationView, d *catalog.Relation, raw parser.Expr) (*policyExpression, error) {
	if raw == nil {
		return nil, nil
	}
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
	columns := make([]analyze.QueryColumn, len(d.Columns))
	for i, c := range d.Columns {
		columns[i] = analyze.QueryColumn{Descriptor: c, Slot: i, Leaf: 0, Physical: i}
	}
	ns := analyze.QueryNamespace{
		Columns:    columns,
		Qualifiers: []analyze.QueryQualifier{{Name: d.Name(), Original: d.Name(), Schema: auth.Catalog.Get(d.Namespace()).Name(), Ref: ref, Columns: columns}},
		Frame:      d.Columns,
	}
	b := newExpressionBinder(scope.op.Context(), e, scope.input, scope.op, nil)
	b.Consumer.PolicyConsumer, b.Params.NoParameters = true, true
	// Query scopes carry this clause to the aggregate's semantic owner.
	b.Clause.AggregateClause = "policy expressions"
	b.Target.Relation = d
	b.Query.Auth, b.Query.Namespace = &auth, &ns
	b.Query.Sources = []exec.Leaf{{Ref: ref, Relation: d}}
	node, err := b.BindBoolean(raw, analyze.ProjectionContext, "POLICY")
	if err != nil {
		return nil, alterCookingError(err)
	}
	raw = b.NormalizeStoredExpression(raw)
	if err = b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
		return nil, err
	}
	source := &plan.SourceNode{QuerySchema: d.Columns, Ref: ref, Occurrence: 0}
	root := &plan.ProjectNode{Child: source, Expressions: []expr.Expr{node}, Frame: 1, QuerySchema: []catalog.Column{{Usage: node.ResultUsage()}}}
	capture := analyze.Binding{
		Query:        &plan.Query{Root: root},
		Dependencies: b.Dependencies,
		QuerySources: b.Query.Sources,
		Capture:      b.Capture,
		Prepared: analyze.PreparedCaptures{
			SequenceConstants: b.Prepared.SequenceConstants,
			RegtypeConstants:  b.Prepared.RegtypeConstants,
		},
	}
	frozen, err := freezeQueryPresentation(auth.Catalog, catalogSourceTemporaryOwner(auth), nil, capture, false, false, 0, "", false)
	if err != nil {
		return nil, err
	}
	frozen.frames[1] = root
	frozen.expressionSyntax = raw
	refs, err := pgDependExpressionRefs(scope.op.Context(), auth.Catalog, node)
	if err != nil {
		return nil, err
	}
	frozen.dependencies = make([]catalog.ExpressionDependency, 0, len(refs))
	for _, address := range refs {
		whole := address
		whole.SubID = 0
		frozen.dependencies = append(frozen.dependencies, catalog.ExpressionDependency{
			Ref:      e.catalog.Domain.Ref(auth.Catalog.Epoch(), address),
			Revision: auth.Catalog.Get(whole).Revision(),
		})
	}
	return &policyExpression{syntax: raw, expression: node, presentation: frozen}, nil
}
