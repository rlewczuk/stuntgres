package engine

import (
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
	columns := make([]queryColumn, len(d.Columns))
	for i, c := range d.Columns {
		columns[i] = queryColumn{descriptor: c, slot: i, leaf: 0, physical: i}
	}
	ns := queryNamespace{
		columns:    columns,
		qualifiers: []queryQualifier{{name: d.Name(), original: d.Name(), schema: auth.Catalog.Get(d.Namespace()).Name(), ref: ref, columns: columns}},
		frame:      d.Columns,
	}
	b := newExpressionBinder(scope.op.Context(), e, scope.input, scope.op, nil)
	b.consumer.policyConsumer, b.params.noParameters = true, true
	// Query scopes carry this clause to the aggregate's semantic owner.
	b.clause.aggregateClause = "policy expressions"
	b.target.relation = d
	b.query.auth, b.query.namespace = &auth, &ns
	b.query.sources = []exec.Leaf{{Ref: ref, Relation: d}}
	node, err := b.bindBoolean(raw, projectionContext, "POLICY")
	if err != nil {
		return nil, alterCookingError(err)
	}
	raw = b.normalizeStoredExpression(raw)
	if err = b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil {
		return nil, err
	}
	source := &plan.SourceNode{QuerySchema: d.Columns, Ref: ref, Occurrence: 0}
	root := &plan.ProjectNode{Child: source, Expressions: []expr.Expr{node}, Frame: 1, QuerySchema: []catalog.Column{{Usage: node.ResultUsage()}}}
	capture := binding{
		query:        &plan.Query{Root: root},
		dependencies: b.dependencies,
		querySources: b.query.sources,
		capture:      b.capture,
		prepared: preparedCaptures{
			sequenceConstants: b.prepared.sequenceConstants,
			regtypeConstants:  b.prepared.regtypeConstants,
		},
	}
	frozen, err := e.freezeQueryPresentationLocked(auth, nil, capture, false, false, 0, "", false)
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
