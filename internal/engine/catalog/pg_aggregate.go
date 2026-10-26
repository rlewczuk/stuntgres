package catalog

import (
	"stuntgres/internal/engine/ids"
)

// An aggregate definition belongs to its implemented routine. Optional seeds
// distinguish SQL NULL from an empty string without allocating seed pointers.
type AggregateDefinition struct {
	Function                       ids.OID
	Kind                           string
	DirectArgs                     int16
	Functions                      [8]ids.OID // transition, final, combine, serialize, deserialize, moving, inverse, moving-final
	FinalExtra, MovingFinalExtra   bool
	FinalModify, MovingFinalModify string
	SortOperator, TransitionType   ids.OID
	TransitionSpace                int32
	MovingType                     ids.OID
	MovingSpace                    int32
	Initial, MovingInitial         string
	HasInitial, HasMovingInitial   bool
}

// StringAggregateDefinition is shared by the catalog row and the bounded opaque
// executor capability. It does not confer a scalar implementation on support
// routines, or describe any other aggregate family.
func StringAggregateDefinition(id ids.OID) AggregateDefinition {
	var transition, final ids.OID
	switch id {
	case 3538:
		transition, final = 3535, 3536
	case 3545:
		transition, final = 3543, 3544
	default:
		return AggregateDefinition{}
	}
	return AggregateDefinition{
		Function: id, Kind: "n",
		Functions:   [8]ids.OID{transition, final, 6299, 6300, 6301},
		FinalModify: "r", MovingFinalModify: "r", TransitionType: 2281,
	}
}
