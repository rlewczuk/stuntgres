package functions

import "stuntgres/internal/engine/expr"

// Temporary aliases for declarations that moved to expr in ENGINE-SPLIT step
// 2b. Step 2d removes them.
type (
	RoutineReference       = expr.RoutineReference
	RoutineRecipeReference = expr.RoutineRecipeReference
	RandomOwner            = expr.RandomOwner
	IntrinsicPropertyRule  = expr.IntrinsicPropertyRule
)

const (
	ExactStable    = expr.ExactStable
	ExactVolatile  = expr.ExactVolatile
	VolatileOnly   = expr.VolatileOnly
	ExactImmutable = expr.ExactImmutable
)
