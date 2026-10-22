package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Engine.mu is held. Analysis shares the ordinary Boolean binder and frozen
// query presentation; no condition is evaluated and no row storage is opened.
func (h ddlHost) BindPolicyExpression(a ddl.Authority, d *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) (*stored.Policy, error) {
	if raw == nil {
		return nil, nil
	}
	e, auth := h.e, ddlAuthorityOf(a)
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
	b := newExpressionBinder(h.op.Context(), e, input, h.op, nil)
	b.Consumer.PolicyConsumer, b.Params.NoParameters = true, true
	// Query scopes carry this clause to the aggregate's semantic owner.
	b.Clause.AggregateClause = "policy expressions"
	b.Target.Relation = d
	b.Query.Auth, b.Query.Namespace = &auth, &ns
	b.Query.Sources = []exec.Leaf{{Ref: ref, Relation: d}}
	node, err := b.BindBoolean(raw, analyze.ProjectionContext, "POLICY")
	if err != nil {
		return nil, table.AlterCookingError(err)
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
	frozen, err := stored.FreezeQueryPresentation(auth.Catalog, catalogSourceTemporaryOwner(auth), nil, capture, false, false, 0, "", false)
	if err != nil {
		return nil, err
	}
	refs, err := stored.ExpressionDependRefs(h.op.Context(), auth.Catalog, node)
	if err != nil {
		return nil, err
	}
	// pg_depend's semantic edges deliberately omit pinned routines. They are
	// not a replacement for the binder's complete execution lifetime manifest.
	// Keep the frozen provider refs/revisions and add only missing catalog edges.
	original := frozen.Dependencies()
	dependencies := original
	seen := make(map[ids.ObjectAddress]bool, len(original)+len(refs))
	for _, dependency := range original {
		seen[dependency.Ref.Address] = true
	}
	for _, address := range refs {
		if seen[address] {
			continue
		}
		whole := address
		whole.SubID = 0
		if len(dependencies) == len(original) {
			dependencies = make([]catalog.ExpressionDependency, len(original), len(original)+len(refs))
			copy(dependencies, original)
		}
		dependencies = append(dependencies, catalog.ExpressionDependency{
			Ref:      e.catalog.Domain.Ref(auth.Catalog.Epoch(), address),
			Revision: auth.Catalog.Get(whole).Revision(),
		})
		seen[address] = true
	}
	frozen = frozen.With(func(p *stored.ViewParts) {
		p.Frames[1] = root
		p.ExpressionSyntax = raw
		p.Dependencies = dependencies
	})
	return stored.NewPolicy(raw, node, frozen), nil
}
