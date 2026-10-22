package view

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
	Definition *stored.View
	ordinal    int
}

// Only transplanted, demanded subqueries acquire an execution source slot.
// Frozen view layouts are bridged by capture, not by re-resolving SQL names.
func (w *Write) GraftQuery(b *analyze.Binding, view catalog.Read, root plan.Node, rewrite func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		return plan.RewriteQueryExpressions(root, rewrite)
	}
	var definition *stored.View
	var consumer *catalog.Relation
	for _, layer := range w.Layers {
		if _, found := stored.ViewOf(layer.Relation).Queries()[project.Syntax]; found {
			definition = stored.ViewOf(layer.Relation)
			consumer = layer.Relation
			break
		}
	}
	b.SubqueryDemand = true
	if definition == nil {
		return plan.RewriteQueryExpressions(root, rewrite)
	}
	return graftStoredQuery(b, view, root, definition, consumer, &w.QueryOccurrences, rewrite)
}

// StoredQueryImporter is a command-local occurrence map shared by every nested
// query of one imported stored expression. Existing view writes use the same
// grafting implementation; policy target source zero is never imported.
type StoredQueryImporter struct{ occurrences map[viewWriteOccurrence]int }

func (g *StoredQueryImporter) GraftQuery(b *analyze.Binding, view catalog.Read, root plan.Node, definition *stored.View, consumer *catalog.Relation, rewrite func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
	b.SubqueryDemand = true
	return graftStoredQuery(b, view, root, definition, consumer, &g.occurrences, rewrite)
}

func graftStoredQuery(b *analyze.Binding, view catalog.Read, root plan.Node, definition *stored.View, consumer *catalog.Relation, occurrences *map[viewWriteOccurrence]int, rewrite func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
	mapOccurrence := func(occurrence int) (int, error) {
		if occurrence < 0 || occurrence >= len(definition.Sources()) {
			return 0, ids.ErrCatalogReplaced
		}
		key := viewWriteOccurrence{definition, occurrence}
		slot, found := (*occurrences)[key]
		if !found {
			leaf := definition.Sources()[occurrence]
			relation, ok := view.Relation(leaf.Ref.Address)
			if !ok {
				return 0, ids.ErrCatalogReplaced
			}
			use, err := stored.ViewUseReference(view, consumer, leaf.Ref)
			if err != nil {
				return 0, err
			}
			// Frozen Reads enumerates attribute identities; zero reads is
			// presence-only, unlike the live nil Uses convention for full
			// demand. Translate once per admitted occurrence, not by names.
			reads := leaf.Reads
			if reads == nil {
				reads = []int32{}
			}
			uses, err := analyze.QuerySourceReadUses(reads, relation.Attributes)
			if err != nil {
				return 0, err
			}
			slot = len(b.QuerySources)
			if *occurrences == nil {
				*occurrences = make(map[viewWriteOccurrence]int)
			}
			(*occurrences)[key] = slot
			b.QuerySources = append(b.QuerySources, exec.Leaf{Ref: use, Relation: relation, Uses: uses, FrozenAttributes: leaf.Attributes, Mark: leaf.Mark, Only: leaf.Only, AnalysisOnly: leaf.AnalysisOnly})
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
