package engine

import (
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// versionRows appends the logical row identities of versions to buf.
func versionRows(buf []ids.RowID, versions []storage.RowVersion) []ids.RowID {
	for _, version := range versions {
		buf = append(buf, version.Row)
	}
	return buf
}

// Descendant replacement authority is a real command target, captured generation
// and row grant. Neither a source grant nor an RI authorization can manufacture it.
func (s *StatementScope) validateInheritedReplacements(ref ids.ObjectRef, old []storage.RowVersion) error {
	if ref == s.lockedRef {
		return nil
	}
	store := s.mutationStorage(ref)
	if store == nil {
		return errors.New("stuntgres: replacement is outside its mutation targets")
	}
	if s.view == nil || s.view.Data().GenerationAt(ref.Address) != store {
		return errors.New("stuntgres: replacement requires its captured target view")
	}
	var small [8]ids.RowID
	target, missing := s.engine().locks.ReplacementGrants(s.ticket, ref, versionRows(small[:0], old))
	if s.ticket == nil || s.ticket.Ref() != s.txRef || !target {
		return errors.New("stuntgres: replacement requires its target grant")
	}
	data := s.view.Data()
	for i, row := range old {
		if i == missing {
			return errors.New("stuntgres: replacement requires its row grant")
		}
		captured := data.Committed[ref.Address]
		if row.Born == 0 {
			captured = data.Own[ref.Address]
		}
		if _, _, found := rowByID(captured, row.Row, -1); !found {
			return errors.New("stuntgres: replacement is outside its candidate view")
		}
	}
	return nil
}
