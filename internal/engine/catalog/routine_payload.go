package catalog

import (
	"context"
	"stuntgres/internal/engine/expr"
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

// RoutineDefaults is the checked declaration recipe, distinct from a body.
// Entries are indexed by input slot; only the selected call evaluates them.
type RoutineDefaults interface {
	Default(int) (expr.Expr, bool)
	Dependencies() []ExpressionDependency
	DefaultSQL(context.Context, Read, int) (string, error)
	ValidateDefinition(*Procedure) error
	Validate(Read) error
	Rebind(Read, map[ids.ObjectAddress]ids.ObjectAddress) (RoutineDefaults, error)
}
