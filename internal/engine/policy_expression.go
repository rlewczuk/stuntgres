package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// Engine.mu is held. Analysis shares the ordinary Boolean binder and frozen
// query presentation; no condition is evaluated and no row storage is opened.
func (e *Engine) bindPolicyExpression(scope *StatementScope, auth AuthorizationView, d *relationDescriptor, raw parser.Expr) (*policyExpression, error) {
	if raw == nil {
		return nil, nil
	}
	ref := e.catalog.Domain.Ref(auth.catalog.Epoch(), d.Address())
	columns := make([]queryColumn, len(d.Columns))
	for i, c := range d.Columns {
		columns[i] = queryColumn{descriptor: c, slot: i, leaf: 0, physical: i}
	}
	ns := queryNamespace{
		columns:    columns,
		qualifiers: []queryQualifier{{name: d.Name(), original: d.Name(), schema: auth.catalog.Get(d.Namespace()).Name(), ref: ref, columns: columns}},
		frame:      d.Columns,
	}
	b := newExpressionBinder(scope.op.Context(), e, scope.input, scope.op, nil)
	b.policyConsumer, b.noParameters = true, true
	// Query scopes carry this clause to the aggregate's semantic owner.
	b.aggregateClause = "policy expressions"
	b.relation = d
	b.queryAuth, b.queryNamespace = &auth, &ns
	b.querySources = []queryLeaf{{ref: ref, relation: d}}
	node, err := b.bindBoolean(raw, projectionContext, "POLICY")
	if err != nil {
		return nil, alterCookingError(err)
	}
	raw = b.normalizeStoredExpression(raw)
	if err = b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil {
		return nil, err
	}
	source := &sourceNode{querySchema: d.Columns, ref: ref, occurrence: 0}
	root := &projectNode{child: source, expressions: []expr.Expr{node}, frame: 1, querySchema: []ColumnDescriptor{{Usage: node.ResultUsage()}}}
	capture := binding{
		query: &BoundQuery{Root: root}, dependencies: b.dependencies, querySources: b.querySources,
		viewLiterals: b.viewLiterals, viewCastUsages: b.viewCastUsages, viewSimilarSubstrings: b.viewSimilarSubstrings,
		viewIntaggRefs:      b.viewIntaggRefs,
		viewOperatorRefs:    b.viewOperatorRefs,
		viewPosixSubstrings: b.viewPosixSubstrings,
		viewCollations:      b.viewCollations, viewColumns: b.viewColumns, viewStars: b.viewStars,
		viewComposites: b.viewComposites,
		viewNamespaces: b.viewNamespaces, sequenceConstants: b.sequenceConstants, regtypeConstants: b.regtypeConstants,
	}
	frozen, err := e.freezeQueryPresentationLocked(auth, nil, capture, false, false, 0, "", false)
	if err != nil {
		return nil, err
	}
	frozen.frames[1] = root
	frozen.expressionSyntax = raw
	refs, err := pgDependExpressionRefs(scope.op.Context(), auth.catalog, node)
	if err != nil {
		return nil, err
	}
	frozen.dependencies = make([]expressionDependency, 0, len(refs))
	for _, address := range refs {
		whole := address
		whole.SubID = 0
		frozen.dependencies = append(frozen.dependencies, expressionDependency{
			Ref:      e.catalog.Domain.Ref(auth.catalog.Epoch(), address),
			Revision: auth.catalog.Get(whole).Revision(),
		})
	}
	return &policyExpression{syntax: raw, expression: node, presentation: frozen}, nil
}
