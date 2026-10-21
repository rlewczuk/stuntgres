package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

// Only actual consumed calls need migration. An untouched structural binding
// must retain the established non-call rebind behavior.
func (b *UniqueWriteBinding) hasConsumedPrograms() bool {
	for i := range b.Keys {
		if len(b.Keys[i].Preparation.admission.Calls) != 0 {
			return true
		}
	}
	for i := range b.Insertion {
		if b.Insertion[i].Unique == nil && len(b.Insertion[i].preparation.admission.Calls) != 0 {
			return true
		}
	}
	return false
}

// A drifted command-private binding can replace programme storage, not original
// consumption. Both owning immutable catalog snapshots prove the original
// parent/index lifetime: owner/epoch come from the unchanged original ObjectRef,
// and each index descriptor must be the actual cell of that original snapshot.
// No current address is promoted to a new reference or a completed recipe.
func (b *UniqueWriteBinding) InheritConsumedPrograms(old *UniqueWriteBinding) error {
	if old == nil || !old.hasConsumedPrograms() {
		return nil
	}
	if old.Ref != b.Ref || old.Catalog == nil || b.Catalog == nil ||
		old.Ref.Epoch != old.Catalog.Epoch() || b.Ref.Epoch != b.Catalog.Epoch() ||
		old.Catalog.Get(old.Ref.Address) != old.Parent || b.Catalog.Get(b.Ref.Address) != b.Parent ||
		old.Parent.Revision() != b.Parent.Revision() || !SameRelationRowShape(old.Parent, b.Parent) {
		return ids.ErrCatalogReplaced
	}
	for i := range old.Keys {
		key := &old.Keys[i]
		if err := b.inheritIndexProgram(old.Catalog, key.Index, &key.Preparation.admission); err != nil {
			return err
		}
	}
	for i := range old.Insertion {
		insertion := &old.Insertion[i]
		if insertion.Unique != nil {
			continue
		}
		if err := b.inheritIndexProgram(old.Catalog, insertion.Index, &insertion.preparation.admission); err != nil {
			return err
		}
	}
	return nil
}

func (b *UniqueWriteBinding) inheritIndexProgram(original *catalog.State, index *catalog.Relation, programme *exec.RoutineProgram) error {
	if len(programme.Calls) == 0 {
		return nil
	}
	if index == nil || original.Get(index.Address()) != index {
		return ids.ErrCatalogReplaced
	}
	current, ok := b.Catalog.Relation(index.Address())
	if !ok || current.Revision() != index.Revision() || !SameIndexKeySemantics(index, current) {
		return ids.ErrCatalogReplaced
	}
	for i := range b.Keys {
		if b.Keys[i].Index == current {
			b.Keys[i].Preparation.admission.Include(programme)
			return nil
		}
	}
	for i := range b.Insertion {
		if b.Insertion[i].Unique == nil && b.Insertion[i].Index == current {
			b.Insertion[i].preparation.admission.Include(programme)
			return nil
		}
	}
	return ids.ErrCatalogReplaced
}
