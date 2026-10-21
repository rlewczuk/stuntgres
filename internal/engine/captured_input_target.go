package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
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

// detachCursorInput rebinds the captured resolvers of a cursor's input to the
// cursor's execution authority: notices then go to the operation that fetches,
// not the one that opened the cursor, and the opening statement's qualified
// and resolved sinks are dropped.
func detachCursorInput(in *sqltype.InputEnvironment, authority *exec.Authority) {
	if reader, ok := in.Regtype.(capturedRegtypeInput); ok {
		reader.op, reader.authority = nil, authority
		reader.qualified = nil
		in.Regtype = reader
	}
	if reader, ok := in.Operator.(capturedOperatorInput); ok {
		reader.types.op, reader.types.authority = nil, authority
		reader.types.qualified, reader.qualified = nil, nil
		in.Operator = reader
	}
	if reader, ok := in.Regproc.(capturedRoutineInput); ok {
		reader.types.op, reader.types.authority = nil, authority
		reader.types.qualified = nil
		in.Regproc = reader
	}
	if reader, ok := in.RegTS.(capturedRegTSInput); ok {
		reader.qualified, reader.resolved = nil, nil
		in.RegTS = reader
	}
}
