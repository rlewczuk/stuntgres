package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// IdentifierTarget points a captured catalog-identifier resolver at one
// literal: its source position and the sinks that collect the namespaces it
// qualified and the providers it resolved. A nil sink keeps the resolver's
// current sink; each resolver uses only the parts its input records.
type IdentifierTarget struct {
	Position  parser.Position
	Qualified *[]ids.ObjectAddress
	Operands  *[]catalog.ExpressionDependency // operator and routine inputs
	Resolved  *[]catalog.ExpressionDependency // text-search inputs
}

// The engine's captured input resolvers implement these, so a literal can
// retarget the resolver in its input environment without naming the engine
// type. Each returns a retargeted copy; the receiver is unchanged.
type (
	// RegtypeRetarget sets the position and the qualified sink.
	RegtypeRetarget interface {
		RetargetRegtype(IdentifierTarget) sqltype.RegtypeResolver
	}
	// OperatorRetarget sets the operand types' position and the qualified
	// and operand sinks.
	OperatorRetarget interface {
		RetargetOperator(IdentifierTarget) sqltype.OperatorResolver
	}
	// RoutineRetarget sets the argument types' position and qualified sink
	// and the operand sink.
	RoutineRetarget interface {
		RetargetRoutine(IdentifierTarget) sqltype.RoutineResolver
	}
	// RegTSRetarget sets the qualified and resolved sinks; it has no
	// position.
	RegTSRetarget interface {
		RetargetRegTS(IdentifierTarget) sqltype.RegTSResolver
	}
)
