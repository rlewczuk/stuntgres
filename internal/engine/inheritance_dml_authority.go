package engine

import (
	"errors"

	"stuntgres/internal/engine/ids"
)

// versionRows appends the logical row identities of versions to buf.
func versionRows(buf []ids.RowID, versions []rowVersion) []ids.RowID {
	for _, version := range versions {
		buf = append(buf, version.row)
	}
	return buf
}

// Descendant replacement authority is a real command target, captured generation
// and row grant. Neither a source grant nor an RI authorization can manufacture it.
func (s *StatementScope) validateInheritedReplacements(ref ids.ObjectRef, old []rowVersion) error {
	if ref == s.lockedRef {
		return nil
	}
	store := s.mutationStorage(ref)
	if store == nil {
		return errors.New("stuntgres: replacement is outside its mutation targets")
	}
	if s.view == nil || s.view.read().generationAt(ref.Address) != store {
		return errors.New("stuntgres: replacement requires its captured target view")
	}
	var small [8]ids.RowID
	target, missing := s.op.session.Engine().locks.ReplacementGrants(s.ticket, ref, versionRows(small[:0], old))
	if s.ticket == nil || s.ticket.Ref() != s.txRef || !target {
		return errors.New("stuntgres: replacement requires its target grant")
	}
	data := s.view.read()
	for i, row := range old {
		if i == missing {
			return errors.New("stuntgres: replacement requires its row grant")
		}
		captured := data.committed[ref.Address]
		if row.born == 0 {
			captured = data.own[ref.Address]
		}
		if _, _, found := rowByID(captured, row.row, -1); !found {
			return errors.New("stuntgres: replacement is outside its candidate view")
		}
	}
	return nil
}
