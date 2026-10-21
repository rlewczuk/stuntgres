package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

// The named cursor owns its retained execution. Mutations borrow only a physical
// occurrence and then remap its stable RowID into their fresh command view.
func (s *StatementScope) positionedMutationRow(b analyze.Binding) (mutationRowIdentity, error) {
	c := payloadOf(s.op.Session).cursors.entries[b.CurrentOf]
	if c == nil || (!c.promoted && c.tx != s.txRef) {
		return mutationRowIdentity{}, missingSQLCursor(b.CurrentOf)
	}
	if c.epoch != s.txRef.Epoch() {
		return mutationRowIdentity{}, ids.ErrCatalogReplaced
	}
	if c.position == cursorFailed || c.root == nil {
		return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" is held from a previous transaction", b.CurrentOf))
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
				return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" has multiple FOR UPDATE/SHARE references to table \"%s\"", b.CurrentOf, b.Relation.Name()))
			}
			occurrence = i
		}
	}
	if marked {
		if occurrence == -1 {
			return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" does not have a FOR UPDATE/SHARE reference to table \"%s\"", b.CurrentOf, b.Relation.Name()))
		}
	} else {
		scan := exec.CursorSimpleScan(c.root, c.source)
		if scan == nil || scan.Ref != b.Ref {
			return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" is not a simply updatable scan of table \"%s\"", b.CurrentOf, b.Relation.Name()))
		}
		occurrence = scan.Occurrence
	}
	if c.position != cursorOn {
		return mutationRowIdentity{}, sqlError("24000", fmt.Sprintf("cursor \"%s\" is not positioned on a row", b.CurrentOf))
	}
	if c.locks == nil || occurrence < 0 || occurrence >= len(c.locks.Leaves) {
		return mutationRowIdentity{}, ids.ErrCatalogReplaced
	}
	physical := exec.CursorOccurrencePhysicalRow(c.currentSource, c.currentIndex, occurrence, c.locks)
	if physical.Version.Row == 0 {
		return mutationRowIdentity{}, nil
	}
	storage := s.mutationStorage(physical.Leaf.Ref)
	if storage == nil {
		// ONLY excludes a positioned descendant, without changing its cursor.
		return mutationRowIdentity{}, nil
	}
	if storage != physical.Leaf.Storage {
		return mutationRowIdentity{}, ids.ErrCatalogReplaced
	}
	return mutationRowIdentity{physical.Leaf.Ref, physical.Version.Row}, nil
}
