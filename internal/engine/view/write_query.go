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
			b.QuerySources = append(b.QuerySources, exec.Leaf{Ref: use, Relation: relation, Uses: uses, FrozenAttributes: leaf.Attributes, Mark: leaf.Mark, Only: leaf.Only, AnalysisOnly: leaf.AnalysisOnly, SystemAttributeRead: leaf.SystemAttributeRead, SystemColumns: leaf.SystemColumns, WholeRowRead: leaf.WholeRowRead})
			b.SubqueryDemand = true
		}
		return slot, nil
	}
	// Own structural frames, including their free reads inside scalar queries.
	// Each scalar's recursive graft still owns its local sources and frames.
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
		if system, ok := n.(plan.SystemAttributeExpr); ok && system.Scope != 0 && system.QueryOccurrence > 0 && frames[system.Frame] {
			slot, err := mapOccurrence(system.QueryOccurrence - 1)
			if err != nil {
				return nil, false, err
			}
			use, err := stored.ViewUseReference(view, consumer, system.Owner)
			if err != nil {
				return nil, false, err
			}
			system.QueryOccurrence, system.Owner = slot+1, use
			return system, true, nil
		}
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
	mapCaptures := func(captures []plan.FunctionCorrelation) ([]plan.FunctionCorrelation, error) {
		var mapped []plan.FunctionCorrelation
		for i, capture := range captures {
			if capture.QueryOccurrence == 0 || !frames[capture.Frame] {
				continue
			}
			slot, err := mapOccurrence(capture.QueryOccurrence - 1)
			if err != nil {
				return nil, err
			}
			if slot+1 == capture.QueryOccurrence {
				continue
			}
			if mapped == nil {
				mapped = append([]plan.FunctionCorrelation(nil), captures...)
			}
			mapped[i].QueryOccurrence = slot + 1
		}
		if mapped == nil {
			return captures, nil
		}
		return mapped, nil
	}
	var mapNode func(plan.Node, bool) (plan.Node, error)
	var translateExpression func(expr.Expr) (expr.Expr, bool, error)
	translateExpression = func(n expr.Expr) (expr.Expr, bool, error) {
		// Cross scalar boundaries only for this graft's frames. Leave local
		// sources and lowering to rewrite's normal recursive query callback.
		return stored.MapExpression(view, n, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, func(root plan.Node) (plan.Node, error) {
			return plan.RewriteQueryExpressions(root, translateExpression, func(node plan.Node) (plan.Node, error) {
				return mapNode(node, false)
			})
		}, nil, translate)
	}
	mapExpression := func(n expr.Expr) (expr.Expr, bool, error) {
		next, translated, err := translateExpression(n)
		if err != nil {
			return nil, false, err
		}
		lowered, changed, err := rewrite(next)
		return lowered, translated || changed, err
	}
	mapNode = func(node plan.Node, importSources bool) (plan.Node, error) {
		mapped := node
		switch n := node.(type) {
		case *plan.DerivedNode:
			captures, err := mapCaptures(n.Captures)
			if err != nil {
				return nil, err
			}
			if len(captures) != 0 && &captures[0] != &n.Captures[0] {
				copy := *n
				copy.Captures = captures
				mapped = &copy
			}
		case *plan.AggregateNode:
			var dependencies []plan.GroupDependency
			for i, proof := range n.Dependencies {
				// Scalar-local proofs belong to their recursive graft, just
				// like scalar-local sources and captured frame identities.
				if !frames[proof.Frame] {
					continue
				}
				occurrence, err := mapOccurrence(proof.Occurrence)
				if err != nil {
					return nil, err
				}
				use, err := stored.ViewUseReference(view, consumer, proof.Parent)
				if err != nil {
					return nil, err
				}
				if occurrence == proof.Occurrence && use == proof.Parent {
					continue
				}
				if dependencies == nil {
					dependencies = append([]plan.GroupDependency(nil), n.Dependencies...)
				}
				dependencies[i].Occurrence, dependencies[i].Parent = occurrence, use
			}
			if dependencies != nil {
				copy := *n
				copy.Dependencies = dependencies
				mapped = &copy
			}
		case *plan.FunctionFromNode:
			captures, err := mapCaptures(n.Captures)
			if err != nil {
				return nil, err
			}
			if len(captures) != 0 && &captures[0] != &n.Captures[0] {
				copy := *n
				copy.Captures = captures
				mapped = &copy
			}
		case *plan.JoinNode:
			captures, err := mapCaptures(n.Captures)
			if err != nil {
				return nil, err
			}
			if len(captures) != 0 && &captures[0] != &n.Captures[0] {
				copy := *n
				copy.Captures = captures
				mapped = &copy
			}
		case *plan.SourceNode:
			copy := *n
			if importSources {
				slot, err := mapOccurrence(n.Occurrence)
				if err != nil {
					return nil, err
				}
				copy.Occurrence, copy.Ref = slot, b.QuerySources[slot].Ref
			}
			if n.Sample != nil {
				captures, err := mapCaptures(n.Sample.Captures)
				if err != nil {
					return nil, err
				}
				if len(captures) != 0 && &captures[0] != &n.Sample.Captures[0] {
					sample := *n.Sample
					sample.Captures = captures
					copy.Sample = &sample
				}
			}
			if copy.Occurrence != n.Occurrence || copy.Ref != n.Ref || copy.Sample != n.Sample {
				mapped = &copy
			}
		}
		if mapped == node {
			return node, nil
		}
		expressions := translateExpression
		if importSources {
			expressions = mapExpression
		}
		// A node replacement is a complete leaf to RewriteQueryExpressions.
		// Descend its executable inputs once, bypassing only this mapped root.
		return plan.RewriteQueryExpressions(mapped, expressions, func(child plan.Node) (plan.Node, error) {
			if child == mapped {
				return child, nil
			}
			return mapNode(child, importSources)
		})
	}
	return plan.RewriteQueryExpressions(root, mapExpression, func(node plan.Node) (plan.Node, error) {
		return mapNode(node, true)
	})
}
