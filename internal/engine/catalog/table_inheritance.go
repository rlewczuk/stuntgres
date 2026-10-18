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

type TableInheritance struct {
	Parents []ids.ObjectAddress
	Columns map[int32]InheritedColumn
	Checks  map[string]InheritedConstraint
	NotNull map[int32]InheritedConstraint
}
