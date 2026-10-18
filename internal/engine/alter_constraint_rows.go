package engine

import "stuntgres/internal/engine/ids"

// Historical ALTER worklists are separate from unconditional successor DML.
// The caller supplies only selected valid CHECKs and required valid NN jobs.
func (s *StatementScope) validateAddedConstraintRows(validator alterRowValidator, versions []rowVersion, ends *deletionRoot, frontier ids.CommitSeq, writes []rowVersion, deletes *privateDeletionRoot) error {
	var replaced map[ids.RowID]bool
	for _, row := range writes {
		if row.base != 0 {
			if replaced == nil {
				replaced = make(map[ids.RowID]bool)
			}
			replaced[row.row] = true
		}
	}
	evaluation := EvalContext{Context: s.op.Context(), Scope: 1, Frame: 1, statementScope: s, Input: s.input, Output: s.output}
	scan := func(rows []rowVersion, committed bool) error {
		for _, row := range rows {
			if err := s.op.Context().Err(); err != nil {
				return err
			}
			if committed && replaced[row.row] || !liveVersion(row, frontier, ends, deletes) {
				continue
			}
			if err := validator.validate(row.values, &evaluation); err != nil {
				return err
			}
		}
		return nil
	}
	if err := scan(versions, true); err != nil {
		return err
	}
	return scan(writes, false)
}

func validCheckTraversal(parent *relationDescriptor) *relationDescriptor {
	valid := 0
	for _, c := range parent.CheckConstraints {
		if !c.NotValid {
			valid++
		}
	}
	if valid == len(parent.CheckConstraints) {
		return parent
	}
	next := *parent
	next.CheckConstraints = make([]checkConstraint, 0, valid)
	for _, c := range parent.CheckConstraints {
		if !c.NotValid {
			next.CheckConstraints = append(next.CheckConstraints, c)
		}
	}
	return &next
}
