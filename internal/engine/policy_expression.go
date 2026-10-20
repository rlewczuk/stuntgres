package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
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
	root := &projectNode{child: source, expressions: []expr.Expr{node}, frame: 1, querySchema: []catalog.Column{{Usage: node.ResultUsage()}}}
	capture := binding{
		query:        &BoundQuery{Root: root},
		dependencies: b.dependencies,
		querySources: b.querySources,
		capture: viewCapture{
			literals:          b.viewLiterals,
			castUsages:        b.viewCastUsages,
			similarSubstrings: b.viewSimilarSubstrings,
			routineRefs:       b.viewRoutineRefs,
			operatorRefs:      b.viewOperatorRefs,
			posixSubstrings:   b.viewPosixSubstrings,
			collations:        b.viewCollations,
			columns:           b.viewColumns,
			stars:             b.viewStars,
			composites:        b.viewComposites,
			namespaces:        b.viewNamespaces,
		},
		prepared: preparedCaptures{
			sequenceConstants: b.sequenceConstants,
			regtypeConstants:  b.regtypeConstants,
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
