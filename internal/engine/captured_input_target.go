package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/sqltype"
)

// The captured input resolvers retarget through the analyze interfaces, so
// binder literals never name them. Each sets only the fields its literal
// sites set; a nil sink keeps the captured one.
var (
	_ analyze.RegtypeRetarget  = capturedRegtypeInput{}
	_ analyze.OperatorRetarget = capturedOperatorInput{}
	_ analyze.RoutineRetarget  = capturedRoutineInput{}
	_ analyze.RegTSRetarget    = capturedRegTSInput{}
)

func (r capturedRegtypeInput) RetargetRegtype(t analyze.IdentifierTarget) sqltype.RegtypeResolver {
	r.position = t.Position
	if t.Qualified != nil {
		r.qualified = t.Qualified
	}
	return r
}

func (r capturedOperatorInput) RetargetOperator(t analyze.IdentifierTarget) sqltype.OperatorResolver {
	r.types.position = t.Position
	if t.Qualified != nil {
		r.qualified = t.Qualified
	}
	if t.Operands != nil {
		r.operands = t.Operands
	}
	return r
}

func (r capturedRoutineInput) RetargetRoutine(t analyze.IdentifierTarget) sqltype.RoutineResolver {
	r.types.position = t.Position
	if t.Qualified != nil {
		r.types.qualified = t.Qualified
	}
	if t.Operands != nil {
		r.operands = t.Operands
	}
	return r
}

func (r capturedRegTSInput) RetargetRegTS(t analyze.IdentifierTarget) sqltype.RegTSResolver {
	if t.Qualified != nil {
		r.qualified = t.Qualified
	}
	if t.Resolved != nil {
		r.resolved = t.Resolved
	}
	return r
}
