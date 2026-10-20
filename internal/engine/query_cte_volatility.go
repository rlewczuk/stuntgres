package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// Stored views are source leaves, not logical inputs. Inspect their immutable
// definitions using the same identity-bound graph authority as view expansion;
// no source is opened and no expression is evaluated during this analysis.
func cteStoredViewVolatile(view catalog.Read, root plan.Node) (bool, error) {
	var leaves []exec.Leaf
	var sourceErr error
	plan.VisitCTEQuery(root, func(node plan.Node) {
		if source, ok := node.(*plan.SourceNode); ok {
			relation, _ := view.Relation(source.Ref.Address)
			if relation == nil {
				sourceErr = ids.ErrCatalogReplaced
			} else if relation.RelationKind == catalog.ViewRelation {
				leaves = append(leaves, exec.Leaf{Relation: relation})
			}
		}
	}, func(expr.Expr) {}, true)
	if sourceErr != nil {
		return false, sourceErr
	}
	if len(leaves) == 0 {
		return false, nil
	}
	if _, err := validateViewReadGraph(view, leaves); err != nil {
		return false, err
	}
	// Validation above owns cycle and stored-reference errors. Memoization here
	// only avoids re-inspecting a shared acyclic definition in this analysis.
	memo := make(map[ids.ObjectAddress]bool)
	var visit func(*catalog.Relation) bool
	visit = func(relation *catalog.Relation) bool {
		if relation.RelationKind != catalog.ViewRelation {
			return relation.RelationKind == catalog.CatalogRelation && relation.Address().ClassID == ids.RelationClassID && relation.Address().ObjectID == 12048
		}
		if volatile, ok := memo[relation.Address()]; ok {
			return volatile
		}
		volatile := analyze.CTEVolatile(viewOf(relation).query.Root)
		for _, source := range viewOf(relation).sources {
			if volatile {
				break
			}
			child := view.MustRelation(source.ref.Address)
			volatile = visit(child) || volatile
		}
		memo[relation.Address()] = volatile
		return volatile
	}
	for _, leaf := range leaves {
		if visit(leaf.Relation) {
			return true, nil
		}
	}
	return false, nil
}
