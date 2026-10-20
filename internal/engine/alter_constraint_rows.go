package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Historical ALTER worklists are separate from unconditional successor DML.
// The caller supplies only selected valid CHECKs and required valid NN jobs.
func (s *StatementScope) validateAddedConstraintRows(validator alterRowValidator, versions []storage.RowVersion, ends *storage.DeletionRoot, frontier ids.CommitSeq, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) error {
	var replaced map[ids.RowID]bool
	for _, row := range writes {
		if row.Base != 0 {
			if replaced == nil {
				replaced = make(map[ids.RowID]bool)
			}
			replaced[row.Row] = true
		}
	}
	evaluation := EvalContext{evalStatement: evalStatement{Context: s.op.Context(), statementScope: s, Input: s.input, Output: s.output}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
	scan := func(rows []storage.RowVersion, committed bool) error {
		for _, row := range rows {
			if err := s.op.Context().Err(); err != nil {
				return err
			}
			if committed && replaced[row.Row] || !visibility.LiveVersion(row, frontier, ends, deletes) {
				continue
			}
			if err := validator.validate(row.Values, &evaluation); err != nil {
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

func validCheckTraversal(parent *catalog.Relation) *catalog.Relation {
	valid := 0
	for _, c := range parent.Constraints.Check {
		if !c.NotValid {
			valid++
		}
	}
	if valid == len(parent.Constraints.Check) {
		return parent
	}
	next := *parent
	next.Constraints.Check = make([]catalog.CheckConstraint, 0, valid)
	for _, c := range parent.Constraints.Check {
		if !c.NotValid {
			next.Constraints.Check = append(next.Constraints.Check, c)
		}
	}
	return &next
}
