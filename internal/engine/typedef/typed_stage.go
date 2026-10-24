package typedef

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// TypedStage is the typed-table observation of one catalog stage: the
// composite type an ALTER TYPE changes and the typed tables it admitted. A
// nil slot observes nothing.
type TypedStage struct {
	Source   ids.ObjectAddress
	Observed map[ids.ObjectAddress]catalog.Descriptor
}

func (t *TypedStage) SourceType() ids.ObjectAddress {
	if t == nil {
		return ids.ObjectAddress{}
	}
	return t.Source
}

func (t *TypedStage) ObservedTables() map[ids.ObjectAddress]catalog.Descriptor {
	if t == nil {
		return nil
	}
	return t.Observed
}

// TypedFieldCheckOwner reports whether relation is a typed table of typed's
// source type, or inherits from one, among the tables typed admitted: only
// that propagation closure may prune inherited CHECKs when a composite field
// is dropped. Parents are read from base.
func TypedFieldCheckOwner(typed *TypedStage, base catalog.Read, relation *catalog.Relation) bool {
	if typed.SourceType() == (ids.ObjectAddress{}) || typed.ObservedTables()[relation.Address()] == nil {
		return false
	}
	if relation.OfType == typed.SourceType() {
		return true
	}
	if relation.Inheritance != nil {
		for _, edge := range relation.Inheritance.Parents {
			parent, ok := base.Relation(edge.Relation)
			if ok && TypedFieldCheckOwner(typed, base, parent) {
				return true
			}
		}
	}
	return false
}
