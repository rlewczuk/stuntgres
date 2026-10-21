package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
)

func (s *StatementScope) acquireForeignKeyTypeProviders(view catalog.Read, address ids.ObjectAddress) error {
	var refs []ids.ObjectRef
	e := s.engine()
	for _, descriptor := range view.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if child.Address() != address && f.Target != address {
				continue
			}
			for _, a := range []ids.ObjectAddress{child.Address(), f.Target} {
				ref := e.catalog.Domain.Ref(view.Epoch(), a)
				if !slices.Contains(refs, ref) {
					refs = append(refs, ref)
				}
			}
		}
	}
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int {
		if a.Address.ObjectID < b.Address.ObjectID {
			return -1
		}
		if a.Address.ObjectID > b.Address.ObjectID {
			return 1
		}
		return 0
	})
	for _, ref := range refs {
		if err := s.foreignKeyGrant(ref, 0, lock.RowShare, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) validateAlterForeignKeys(view catalog.Read, address ids.ObjectAddress) error {
	auth, err := operationAuthorization(s.op)
	if err != nil {
		return err
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	e := s.engine()
	for _, descriptor := range view.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if f.NotValid || child.Address() != address && f.Target != address {
				continue
			}
			parent := view.MustRelation(f.Target)
			index := view.MustRelation(f.Index)
			comparisons, err := table.BindForeignKeyComparison(view, child, parent, index, f)
			if err != nil {
				return err
			}
			children, err := s.captureForeignKeyRows(e.catalog.Domain.Ref(view.Epoch(), child.Address()), ids.ObjectAddress{})
			if err != nil {
				return err
			}
			parents, err := s.captureForeignKeyRows(e.catalog.Domain.Ref(view.Epoch(), parent.Address()), f.Index)
			if err != nil {
				return err
			}
			if err := table.ValidateForeignKeyData(s.op.Context(), auth.View, auth, child, parent, f, comparisons, children, parents, s.input, s.output); err != nil {
				return err
			}
		}
	}
	return nil
}

// Nested CREATE SCHEMA definitions share the normal candidate. Newly created
// providers need no live grant; external parents retain the canonical DDL mode.
func (s *StatementScope) acquireForeignKeyDefinitionProviders(stage *CatalogStage) error {
	var refs []ids.ObjectRef
	for address, descriptor := range stage.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if installedForeignKey(stage, address, f) {
				continue
			}
			if f.Target == address {
				continue
			}
			if _, external := stage.Base.Find(f.Target); !external {
				continue
			}
			ref := stage.owner.catalog.Domain.Ref(stage.Base.Epoch(), f.Target)
			if !slices.Contains(refs, ref) {
				refs = append(refs, ref)
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int {
		if a.Address.ObjectID < b.Address.ObjectID {
			return -1
		}
		if a.Address.ObjectID > b.Address.ObjectID {
			return 1
		}
		return 0
	})
	for _, ref := range refs {
		if err := s.foreignKeyGrant(ref, 0, lock.ShareRowExclusive, nil); err != nil {
			return err
		}
	}
	auth, err := operationAuthorization(s.op)
	if err != nil {
		return err
	}
	e := stage.owner
	e.mu.Lock()
	auth.Catalog = e.privateCatalogLocked(s.owner.Catalog)
	auth.Recapture(auth.Catalog)
	e.mu.Unlock()
	for address, descriptor := range stage.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if installedForeignKey(stage, address, f) {
				continue
			}
			if f.Target == address {
				continue
			}
			if _, external := stage.Base.Find(f.Target); !external {
				continue
			}
			parent, ok := auth.Catalog.Relation(f.Target)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			index, ok := auth.Catalog.Relation(f.Index)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			eligible, err := table.ForeignKeyIndexEligible(parent, index, f.Referenced)
			if err != nil {
				return err
			}
			if !eligible {
				return ids.ErrCatalogReplaced
			}
			if _, err := table.BindForeignKeyComparison(auth.Catalog, child, parent, index, f); err != nil {
				return err
			}
			if err := table.ForeignKeyReferencesAllowed(auth.View, auth, parent, f.Referenced); err != nil {
				return err
			}
		}
	}
	return nil
}
