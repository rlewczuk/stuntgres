package catalog

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// RoutineProgram is a checked immutable SQL-standard body. Implementations live
// above catalog; it never owns executable resources or authorization decisions.
type RoutineProgram interface {
	sqltype.StructuralTree
	Len() int
	StatementSQL(context.Context, Read, int) (string, error)
	Dependencies() []ExpressionDependency
	ValidateDefinition(*Procedure) error
	// ValidateMetadata checks retained shape/provenance, not current execution.
	ValidateMetadata(Read) error
	Validate(Read) error
	Rebind(Read, map[ids.ObjectAddress]ids.ObjectAddress, *RoutineRemap) (RoutineProgram, error)
}

// RoutineDefaults is the checked declaration recipe, distinct from a body.
// Entries are indexed by input slot; only the selected call evaluates them.
type RoutineDefaults interface {
	Default(int) (expr.Expr, bool)
	Dependencies() []ExpressionDependency
	DefaultSQL(context.Context, Read, int) (string, error)
	ValidateDefinition(*Procedure) error
	// ValidateMetadata checks retained shape/provenance, not current execution.
	ValidateMetadata(Read) error
	Validate(Read) error
	Rebind(Read, map[ids.ObjectAddress]ids.ObjectAddress, *RoutineRemap) (RoutineDefaults, error)
}

// RoutineRemap is one private replacement's original-payload mapping. The
// callbacks share immutable payload identities between declarations and captures.
type RoutineRemap struct {
	Descriptor   func(Descriptor) Descriptor
	Type         func(*sqltype.Descriptor) *sqltype.Descriptor
	Collation    func(sqltype.CollationRef) (sqltype.CollationRef, error)
	Procedure    ProcedureRemapper
	Definition   func(*proc.Definition) (*proc.Definition, error)
	Body         func(RoutineProgram) (RoutineProgram, error)
	Defaults     func(RoutineDefaults) (RoutineDefaults, error)
	Finish       func() error
	Declarations func() error
	// RefreshDependency is installed only by an explicit same-generation
	// definition refresh. Ordinary type/import remapping retains its old stamps.
	RefreshDependency func(ExpressionDependency) (ExpressionDependency,error)
}
