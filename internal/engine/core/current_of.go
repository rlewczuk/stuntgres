package core

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

// The named cursor owns its retained execution. Mutations borrow only a physical
// occurrence and then remap its stable RowID into their fresh command view.
func (s *StatementScope) positionedMutationRow(b analyze.Binding) (dml.MutationRowIdentity, error) {
	c := PayloadOf(s.Op.Session).Cursors.Entries[b.CurrentOf]
	if c == nil || (!c.promoted && c.tx != s.TxRef) {
		return dml.MutationRowIdentity{}, missingSQLCursor(b.CurrentOf)
	}
	if c.epoch != s.TxRef.Epoch() {
		return dml.MutationRowIdentity{}, ids.ErrCatalogReplaced
	}
	if c.Place == exec.CursorFailed || c.root == nil {
		return dml.MutationRowIdentity{}, SQLError("24000", fmt.Sprintf("cursor \"%s\" is held from a previous transaction", b.CurrentOf))
	}
	occurrence := -1
	marked := false
	if c.locks != nil {
		for i, leaf := range c.locks.Leaves {
			if leaf.Mark.Mode == 0 {
				continue
			}
			marked = true
			if leaf.Leaf.Ref != b.Ref {
				continue
			}
			if occurrence != -1 {
				return dml.MutationRowIdentity{}, SQLError("24000", fmt.Sprintf("cursor \"%s\" has multiple FOR UPDATE/SHARE references to table \"%s\"", b.CurrentOf, b.Relation.Name()))
			}
			occurrence = i
		}
	}
	if marked {
		if occurrence == -1 {
			return dml.MutationRowIdentity{}, SQLError("24000", fmt.Sprintf("cursor \"%s\" does not have a FOR UPDATE/SHARE reference to table \"%s\"", b.CurrentOf, b.Relation.Name()))
		}
	} else {
		scan := exec.CursorSimpleScan(c.root, c.Source)
		if scan == nil || scan.Ref != b.Ref {
			return dml.MutationRowIdentity{}, SQLError("24000", fmt.Sprintf("cursor \"%s\" is not a simply updatable scan of table \"%s\"", b.CurrentOf, b.Relation.Name()))
		}
		occurrence = scan.Occurrence
	}
	if c.Place != exec.CursorOn {
		return dml.MutationRowIdentity{}, SQLError("24000", fmt.Sprintf("cursor \"%s\" is not positioned on a row", b.CurrentOf))
	}
	if c.locks == nil || occurrence < 0 || occurrence >= len(c.locks.Leaves) {
		return dml.MutationRowIdentity{}, ids.ErrCatalogReplaced
	}
	physical := exec.CursorOccurrencePhysicalRow(c.CurrentSource, c.CurrentIndex, occurrence, c.locks)
	if physical.Version.Row == 0 {
		return dml.MutationRowIdentity{}, nil
	}
	storage := s.mutationStorage(physical.Leaf.Ref)
	if storage == nil {
		// ONLY excludes a positioned descendant, without changing its cursor.
		return dml.MutationRowIdentity{}, nil
	}
	if storage != physical.Leaf.Storage {
		return dml.MutationRowIdentity{}, ids.ErrCatalogReplaced
	}
	return dml.MutationRowIdentity{Ref: physical.Leaf.Ref, Row: physical.Version.Row}, nil
}
