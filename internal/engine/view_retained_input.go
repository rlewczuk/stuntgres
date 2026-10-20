package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Each frozen consumer supplies its own coherent catalog and input owners.
func captureRetainedDefinitionInput(view catalog.Read, definition *viewDefinition, element func() error, diagnostic func(*sqltype.Descriptor) error) error {
	seen := make(map[*viewDefinition]bool)
	var capture func(*viewDefinition) error
	capture = func(definition *viewDefinition) error {
		if seen[definition] {
			return nil
		}
		seen[definition] = true
		var failure error
		analyze.WalkQueryArgumentNode(definition.query.Root, func(node expr.Expr) {
			if failure == nil {
				failure = analyze.CaptureRetainedExpressionInput(node, element, diagnostic)
			}
		})
		if failure != nil {
			return failure
		}
		for _, source := range definition.sources {
			relation, ok := view.Relation(source.ref.Address)
			if ok && relation.RelationKind == catalog.ViewRelation {
				if err := capture(viewOf(relation)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return capture(definition)
}
