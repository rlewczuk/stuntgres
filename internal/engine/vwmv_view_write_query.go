package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

type viewWriteOccurrence struct {
	definition *stored.View
	ordinal    int
}

// Only transplanted, demanded subqueries acquire an execution source slot.
// Frozen view layouts are bridged by capture, not by re-resolving SQL names.
func (w *viewWrite) graftQuery(b *analyze.Binding, view catalog.Read, root plan.Node, rewrite func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		return plan.RewriteQueryExpressions(root, rewrite)
	}
	var definition *stored.View
	var consumer *catalog.Relation
	for _, layer := range w.layers {
		if _, found := stored.ViewOf(layer.relation).Queries()[project.Syntax]; found {
			definition = stored.ViewOf(layer.relation)
			consumer = layer.relation
			break
		}
	}
	b.SubqueryDemand = true
	if definition == nil {
		return plan.RewriteQueryExpressions(root, rewrite)
	}
	mapOccurrence := func(occurrence int) (int, error) {
		if occurrence < 0 || occurrence >= len(definition.Sources()) {
			return 0, ids.ErrCatalogReplaced
		}
		key := viewWriteOccurrence{definition, occurrence}
		slot, found := w.queryOccurrences[key]
		if !found {
			leaf := definition.Sources()[occurrence]
			relation, ok := view.Relation(leaf.Ref.Address)
			if !ok {
				return 0, ids.ErrCatalogReplaced
			}
			slot = len(b.QuerySources)
			if w.queryOccurrences == nil {
				w.queryOccurrences = make(map[viewWriteOccurrence]int)
			}
			w.queryOccurrences[key] = slot
			use, err := stored.ViewUseReference(view, consumer, leaf.Ref)
			if err != nil {
				return 0, err
			}
			b.QuerySources = append(b.QuerySources, exec.Leaf{Ref: use, Relation: relation, FrozenAttributes: leaf.Attributes, Mark: leaf.Mark})
			b.SubqueryDemand = true
		}
		return slot, nil
	}
	// Structural derived inputs belong to this transplant. Scalar child queries
	// are lowered by rewrite's explicit query callback and must not map twice.
	frames := make(map[uint32]bool)
	var ownFrames func(plan.Node)
	ownFrames = func(node plan.Node) {
		if node == nil {
			return
		}
		if project, ok := node.(*plan.ProjectNode); ok {
			frames[project.Frame] = true
		}
		left, right := plan.QueryInputs(node)
		ownFrames(left)
		ownFrames(right)
	}
	ownFrames(root)
	translate := func(n expr.Expr) (expr.Expr, bool, error) {
		column, ok := n.(plan.ColumnExpr)
		if !ok || column.Origin == (ids.ObjectRef{}) || !frames[column.Frame] {
			return n, false, nil
		}
		changed := false
		if column.QueryOccurrence != 0 {
			slot, err := mapOccurrence(column.QueryOccurrence - 1)
			if err != nil {
				return nil, false, err
			}
			column.QueryOccurrence = slot + 1
			changed = true
		}
		use, err := stored.ViewUseReference(view, consumer, column.Origin)
		if err != nil {
			return nil, false, err
		}
		changed = changed || column.Origin != use
		column.Origin = use
		return column, changed, nil
	}
	return plan.RewriteQueryExpressions(root, func(n expr.Expr) (expr.Expr, bool, error) {
		next, translated, err := stored.MapExpression(view, n, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil, translate)
		if err != nil {
			return nil, false, err
		}
		lowered, changed, err := rewrite(next)
		return lowered, translated || changed, err
	}, func(node plan.Node) (plan.Node, error) {
		source, ok := node.(*plan.SourceNode)
		if !ok {
			return node, nil
		}
		slot, err := mapOccurrence(source.Occurrence)
		if err != nil {
			return nil, err
		}
		copy := *source
		copy.Occurrence = slot
		copy.Ref = b.QuerySources[slot].Ref
		return &copy, nil
	})
}
