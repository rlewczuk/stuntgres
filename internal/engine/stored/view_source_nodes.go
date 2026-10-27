package stored

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func viewSourceAt(node plan.Node, occurrence int) *plan.SourceNode {
	switch n := node.(type) {
	case *plan.SourceNode:
		if n.Occurrence == occurrence {
			return n
		}
	case *plan.JoinNode:
		if found := viewSourceAt(n.Left, occurrence); found != nil {
			return found
		}
		return viewSourceAt(n.Right, occurrence)
	}
	return nil
}

func viewDerivedAt(node plan.Node, syntax *parser.Select) *plan.DerivedNode {
	if node == nil {
		return nil
	}
	if derived, ok := node.(*plan.DerivedNode); ok {
		if project, ok := derived.Query.Root.(*plan.ProjectNode); ok && project.Syntax == syntax {
			return derived
		}
	}
	left, right := plan.QueryInputs(node)
	if found := viewDerivedAt(left, syntax); found != nil {
		return found
	}
	return viewDerivedAt(right, syntax)
}

func (v *QueryRenderer) renderDerivedQuerySQL(ctx context.Context, catalogView catalog.Read, n *parser.DerivedQuery, ns analyze.ViewNamespace, rangeName ruleRangeName) (viewRenderFrom, error) {
	root := viewDerivedAt(analyze.ViewFromRoot(v.query.Root), n.Query)
	if root == nil {
		return viewRenderFrom{}, fmt.Errorf("stuntgres: missing frozen derived query")
	}
	policy := v.Presentation
	v.Presentation = policy.nestedQueryPresentation(true)
	text, err := v.SelectQuerySQL(ctx, catalogView, n.Query, v.queries[n.Query])
	v.Presentation = policy
	if err != nil {
		return viewRenderFrom{}, err
	}
	from := viewRenderFrom{text: text, root: root}
	for i, column := range root.Schema() {
		from.columns = append(from.columns, viewRenderColumn{name: column.Name, slot: i})
	}
	if v.logicalSourceNames() {
		return v.ruleDerivedSource(from, rangeName), nil
	}
	if v.Presentation.Deparse {
		from.relation = "(" + strings.ReplaceAll(text, "\n", "\n        ") + ")"
		return from, nil
	}
	aliased := v.aliasFrom(from, ns, v.namespaceAlias(ns))
	aliased.relation = "(" + text + ")"
	return aliased, nil
}

func (v *QueryRenderer) renderValuesSourceSQL(ctx context.Context, catalogView catalog.Read, n *parser.ValuesQuery, ns analyze.ViewNamespace, rangeName ruleRangeName) (viewRenderFrom, error) {
	ordinal := ns.Ordinal
	text, err := v.valuesSQL(ctx, catalogView, n, ordinal)
	if err != nil {
		return viewRenderFrom{}, err
	}
	root := viewValuesAt(v.query.Root, ordinal)
	from := viewRenderFrom{text: text, root: root}
	from.rowSource = true
	for i, column := range root.Schema() {
		from.columns = append(from.columns, viewRenderColumn{name: column.Name, slot: i})
	}
	if v.logicalSourceNames() {
		from.relation = "(" + text + ")"
		aliased := v.sourceAlias(from, rangeName.name, rangeName.columns, true)
		aliased.relation = from.relation
		return aliased, nil
	}
	aliased := v.aliasFrom(from, ns, v.namespaceAlias(ns))
	// An explicit VALUES alias replaces the renderer's generated alias.
	aliased.relation = "(" + text + ")"
	return aliased, nil
}
