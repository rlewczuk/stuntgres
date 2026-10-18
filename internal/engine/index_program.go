package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Only actual consumed calls need migration. An untouched structural binding
// must retain the established non-call rebind behavior.
func (b *uniqueWriteBinding) hasConsumedPrograms() bool {
	for i := range b.keys {
		if len(b.keys[i].preparation.admission.calls) != 0 {
			return true
		}
	}
	for i := range b.insertion {
		if b.insertion[i].unique == nil && len(b.insertion[i].preparation.admission.calls) != 0 {
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
func (b *uniqueWriteBinding) inheritConsumedPrograms(old *uniqueWriteBinding) error {
	if old == nil || !old.hasConsumedPrograms() {
		return nil
	}
	if old.ref != b.ref || old.catalog == nil || b.catalog == nil ||
		old.ref.Epoch != old.catalog.Epoch() || b.ref.Epoch != b.catalog.Epoch() ||
		old.catalog.Get(old.ref.Address) != old.parent || b.catalog.Get(b.ref.Address) != b.parent ||
		old.parent.Revision() != b.parent.Revision() || !sameRelationRowShape(old.parent, b.parent) {
		return ids.ErrCatalogReplaced
	}
	for i := range old.keys {
		key := &old.keys[i]
		if err := b.inheritIndexProgram(old.catalog, key.index, &key.preparation.admission); err != nil {
			return err
		}
	}
	for i := range old.insertion {
		insertion := &old.insertion[i]
		if insertion.unique != nil {
			continue
		}
		if err := b.inheritIndexProgram(old.catalog, insertion.index, &insertion.preparation.admission); err != nil {
			return err
		}
	}
	return nil
}

func (b *uniqueWriteBinding) inheritIndexProgram(original *catalog.State, index *catalog.Relation, programme *routineAdmissionProgram) error {
	if len(programme.calls) == 0 {
		return nil
	}
	if index == nil || original.Get(index.Address()) != index {
		return ids.ErrCatalogReplaced
	}
	current, ok := b.catalog.Relation(index.Address())
	if !ok || current.Revision() != index.Revision() || !sameIndexKeySemantics(index, current) {
		return ids.ErrCatalogReplaced
	}
	for i := range b.keys {
		if b.keys[i].index == current {
			b.keys[i].preparation.admission.include(programme)
			return nil
		}
	}
	for i := range b.insertion {
		if b.insertion[i].unique == nil && b.insertion[i].index == current {
			b.insertion[i].preparation.admission.include(programme)
			return nil
		}
	}
	return ids.ErrCatalogReplaced
}
