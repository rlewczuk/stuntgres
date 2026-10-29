package regobj

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The captured input resolvers retarget through the analyze interfaces, so
// binder literals never name them. Each sets only the fields its literal
// sites set; a nil sink keeps the captured one.
var (
	_ analyze.RegtypeRetarget  = TypeInput{}
	_ analyze.OperatorRetarget = OperatorInput{}
	_ analyze.RoutineRetarget  = RoutineInput{}
	_ analyze.RegTSRetarget    = TextSearchInput{}
)

func (r TypeInput) RetargetRegtype(t analyze.IdentifierTarget) sqltype.RegtypeResolver {
	r.position = t.Position
	if t.Qualified != nil {
		r.qualified = t.Qualified
	}
	return r
}

func (r OperatorInput) RetargetOperator(t analyze.IdentifierTarget) sqltype.OperatorResolver {
	r.types.position = t.Position
	if t.Qualified != nil {
		r.qualified = t.Qualified
	}
	if t.Operands != nil {
		r.operands = t.Operands
	}
	return r
}

func (r RoutineInput) RetargetRoutine(t analyze.IdentifierTarget) sqltype.RoutineResolver {
	r.types.position = t.Position
	if t.Qualified != nil {
		r.types.qualified = t.Qualified
	}
	if t.Operands != nil {
		r.operands = t.Operands
	}
	return r
}

func (r TextSearchInput) RetargetRegTS(t analyze.IdentifierTarget) sqltype.RegTSResolver {
	if t.Qualified != nil {
		r.qualified = t.Qualified
	}
	if t.Resolved != nil {
		r.resolved = t.Resolved
	}
	return r
}

// DetachCursorInput rebinds the captured resolvers of a cursor's input to the
// cursor's execution authority: notices then go to the operation that fetches,
// not the one that opened the cursor, and the opening statement's qualified
// and resolved sinks are dropped.
func DetachCursorInput(in *sqltype.InputEnvironment, authority *exec.Authority) {
	if reader, ok := in.Regtype.(TypeInput); ok {
		reader.op, reader.authority = nil, authority
		reader.qualified = nil
		in.Regtype = reader
	}
	if reader, ok := in.Operator.(OperatorInput); ok {
		reader.types.op, reader.types.authority = nil, authority
		reader.types.qualified, reader.qualified = nil, nil
		in.Operator = reader
	}
	if reader, ok := in.Regproc.(RoutineInput); ok {
		reader.types.op, reader.types.authority = nil, authority
		reader.types.qualified = nil
		in.Regproc = reader
	}
	if reader, ok := in.RegTS.(TextSearchInput); ok {
		reader.qualified, reader.resolved = nil, nil
		in.RegTS = reader
	}
}

// CaptureEvaluationInput closes resolver execution over immutable facts and
// one evaluator's notice sink. Analysis-observation sinks must not escape into
// workers, and retained cursor authority must not supply live scratch.
func CaptureEvaluationInput(in *sqltype.InputEnvironment, notice func(session.NoticeLevel, sqlstate.Error)) {
	types := func(r TypeInput) TypeInput {
		r.op, r.authority, r.qualified, r.resolved = nil, nil, nil, nil
		r.notice = notice
		return r
	}
	if r, ok := in.Regtype.(TypeInput); ok {
		in.Regtype = types(r)
	}
	if r, ok := in.Operator.(OperatorInput); ok {
		r.types, r.qualified, r.operands = types(r.types), nil, nil
		in.Operator = r
	}
	if r, ok := in.Regproc.(RoutineInput); ok {
		r.types, r.operands = types(r.types), nil
		in.Regproc = r
	}
	if r, ok := in.RegTS.(TextSearchInput); ok {
		r.qualified, r.resolved = nil, nil
		in.RegTS = r
	}
	if r, ok := in.ACLItem.(ACLItemInput); ok {
		r.Op, r.notice = nil, notice
		in.ACLItem = r
	}
}
