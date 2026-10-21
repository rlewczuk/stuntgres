package catalog

import (
	"stuntgres/internal/engine/ids"
)

// Graph addresses are immutable identities, not definition revision pins.
type InheritanceOrigin struct {
	Parent     ids.ObjectAddress
	Attribute  int32
	Constraint ids.OID
}

type InheritedColumn struct {
	Local              bool
	ExplicitDefault    bool
	ExplicitGeneration bool
	Origins            []InheritanceOrigin
}

type InheritedConstraint struct {
	Local   bool
	Origins []InheritanceOrigin
	Retired []RetiredInheritanceOrigin
}

// InheritanceParent is one direct edge and its live catalog ordinal.
// Removing an edge never renumbers its surviving siblings.
type InheritanceParent struct {
	Relation ids.ObjectAddress
	Seqno    int32
}

type TableInheritance struct {
	Parents []InheritanceParent
	Columns map[int32]InheritedColumn
	Checks  map[string]InheritedConstraint
	NotNull map[int32]InheritedConstraint
}

func (g *TableInheritance) HasParent(parent ids.ObjectAddress) bool {
	if g != nil {
		for _, edge := range g.Parents {
			if edge.Relation == parent {
				return true
			}
		}
	}
	return false
}

func (g *TableInheritance) HasRetired() bool {
	if g != nil {
		for _, p := range g.NotNull {
			if len(p.Retired) != 0 {
				return true
			}
		}
		for _, p := range g.Checks {
			if len(p.Retired) != 0 {
				return true
			}
		}
	}
	return false
}
