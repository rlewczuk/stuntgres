package catalog

import (
	"context"
	"stuntgres/internal/engine/ids"
)

// RoutineProgram is a checked immutable SQL-standard body. Implementations live
// above catalog; it never owns executable resources or authorization decisions.
type RoutineProgram interface {
	Len() int
	StatementSQL(context.Context, Read, int) (string, error)
	Dependencies() []ExpressionDependency
	ValidateDefinition(*Procedure) error
	Validate(Read) error
	Rebind(Read, map[ids.ObjectAddress]ids.ObjectAddress) (RoutineProgram, error)
}
