package exec

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// CheckProgram owns command-private cooked expressions and successfully admitted
// actor phases. Nil Expressions means every original expression is unchanged.
// Admission pointers remain stable while lazy truth appends consumed calls.
// Neither expressions nor phases retain granted rights.
type CheckProgram struct {
	Expressions []expr.Expr
	Phases      []CheckPhase
}

// CheckPhase separates the actual invoker from trusted referential-action
// authority, even when both have the same principal in the same command.
type CheckPhase struct {
	Command   ids.CommandIdentity
	Actor     authorization.PrincipalRef
	RI        bool
	Admission *RoutineProgram
}
