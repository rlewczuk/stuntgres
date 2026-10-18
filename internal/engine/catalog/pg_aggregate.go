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
