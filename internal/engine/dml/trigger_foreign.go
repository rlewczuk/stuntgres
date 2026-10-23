package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/sqltype"
)

// BEFORE row triggers require each accepted foreign insertion to reach the
// existing participant before the next invocation. The participant retains its
// existing Finish/rollback contract; this is not an independent transaction.
type foreignTriggerInsert struct {
	modification *fdw.Modification
	binding      analyze.Binding
	output       *exec.FinalRows
	count        int64
}

func (s *foreignTriggerInsert) accept(h Host, row []sqltype.Value, evaluation *exec.Context) error {
	rows := [1][]sqltype.Value{row}
	count, err := s.modification.InsertRows(h.Context(), h, rows[:], true, func(actual []sqltype.Value) error {
		if err := CheckViewRow(h, &s.binding, actual, evaluation); err != nil {
			return err
		}
		return ProjectForeignReturning(s.binding, s.output, actual, nil, evaluation)
	})
	if err == nil {
		s.count += count
	}
	return err
}
