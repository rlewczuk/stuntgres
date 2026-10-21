package stored

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Each frozen consumer supplies its own coherent catalog and input owners.
func CaptureRetainedInput(view catalog.Read, definition *View, element func() error, diagnostic func(*sqltype.Descriptor) error) error {
	seen := make(map[*View]bool)
	var capture func(*View) error
	capture = func(definition *View) error {
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
			relation, ok := view.Relation(source.Ref.Address)
			if ok && relation.RelationKind == catalog.ViewRelation {
				if err := capture(ViewOf(relation)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return capture(definition)
}
