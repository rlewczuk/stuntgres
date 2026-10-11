package engine

import "stuntgres/internal/parser"

// Engine.mu is held. Analysis shares the ordinary Boolean binder and frozen
// query presentation; no condition is evaluated and no row storage is opened.
func (e *Engine) bindPolicyExpression(scope *StatementScope, auth AuthorizationView, d *relationDescriptor, raw parser.Expr) (*policyExpression, error) {
	if raw == nil {
		return nil, nil
	}
	ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: d.address}
	columns := make([]queryColumn, len(d.columns))
	for i, c := range d.columns {
		columns[i] = queryColumn{c, i, 0, i}
	}
	ns := queryNamespace{
		columns:    columns,
		qualifiers: []queryQualifier{{name: d.name, original: d.name, schema: auth.catalog.state.descriptors[d.namespace].Name(), ref: ref, columns: columns}},
		frame:      d.columns,
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
	if err = b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil {
		return nil, err
	}
	source := &sourceNode{querySchema: d.columns, ref: ref, occurrence: 0}
	root := &projectNode{child: source, expressions: []BoundExpr{node}, frame: 1, querySchema: []ColumnDescriptor{{Usage: node.ResultUsage()}}}
	capture := binding{
		query: &BoundQuery{Root: root}, dependencies: b.dependencies, querySources: b.querySources,
		viewLiterals: b.viewLiterals, viewCastUsages: b.viewCastUsages, viewSimilarSubstrings: b.viewSimilarSubstrings,
		viewCollations: b.viewCollations, viewColumns: b.viewColumns, viewStars: b.viewStars,
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
			ref:      ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: address},
			revision: auth.catalog.state.descriptors[whole].Revision(),
		})
	}
	return &policyExpression{syntax: raw, expression: node, presentation: frozen}, nil
}
