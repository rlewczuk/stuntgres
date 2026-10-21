package table

import (
	"fmt"
	"maps"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// InheritanceDefaultCandidate is one inheritance member old with the DEFAULT of
// its slot replaced by definition (dropped without an expression), marked as
// an explicit default in its inheritance provenance.
func InheritanceDefaultCandidate(old *catalog.Relation, slot int, definition catalog.ColumnDefault) (*catalog.Relation, error) {
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	attribute := old.Attributes[slot]
	next := *old
	next.ObjectRevision++
	next.ColumnMeta.Defaults = maps.Clone(old.ColumnMeta.Defaults)
	if definition.Expression == nil {
		delete(next.ColumnMeta.Defaults, attribute)
	} else {
		if next.ColumnMeta.Defaults == nil {
			next.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault)
		}
		next.ColumnMeta.Defaults[attribute] = definition
	}
	if next.Inheritance != nil {
		next.Inheritance = catalog.CloneInheritance(old.Inheritance)
		provenance := next.Inheritance.Columns[attribute]
		provenance.ExplicitDefault = true
		next.Inheritance.Columns[attribute] = provenance
	}
	return &next, nil
}
