package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
)

func (e *Engine) rebuildAlterForeignKeysLocked(stage *CatalogStage, parent *catalog.Relation, transformations []index.ColumnTransformation) (*catalog.Relation, error) {
	if len(transformations) == 0 {
		return parent, nil
	}
	view := e.stageRead(stage)
	for _, descriptor := range view.Descriptors() {
		old, ok := descriptor.(*catalog.Relation)
		if !ok || len(old.Constraints.ForeignKeys) == 0 {
			continue
		}
		var next *catalog.Relation
		for i, f := range old.Constraints.ForeignKeys {
			affected := false
			for _, change := range transformations {
				affected = affected || (old.Address() == parent.Address() && slices.Contains(f.Attributes, change.Attribute)) || (f.Target == parent.Address() && slices.Contains(f.Referenced, change.Attribute))
			}
			if !affected {
				continue
			}
			if next == nil {
				copy := *old
				copy.Constraints.ForeignKeys = slices.Clone(old.Constraints.ForeignKeys)
				next = &copy
				if old.Address() == parent.Address() {
					*next = *parent
					next.Constraints.ForeignKeys = slices.Clone(parent.Constraints.ForeignKeys)
				}
			}
			c := &next.Constraints.ForeignKeys[i]
			target, ok := view.Relation(c.Target)
			if c.Target == parent.Address() {
				target = parent
				ok = true
			}
			if !ok {
				return nil, ids.ErrCatalogReplaced
			}
			index, err := foreignKeyIndex(view, target, c.Referenced)
			if err != nil {
				return nil, err
			}
			c.Index = index.Address()
			if _, err := bindForeignKeyComparison(view, next, target, index, *c); err != nil {
				return nil, err
			}
			c.OID, err = e.catalog.allocateOIDLocked()
			if err != nil {
				return nil, err
			}
		}
		if next != nil {
			var err error
			next.ObjectRevision, err = e.catalog.revisionLocked()
			if err != nil {
				return nil, err
			}
			if err := stage.Replace(next); err != nil {
				return nil, err
			}
			if next.Address() == parent.Address() {
				parent = next
			}
		}
	}
	return parent, nil
}

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
			comparisons, err := bindForeignKeyComparison(view, child, parent, index, f)
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
			if err := validateForeignKeyData(s.op.Context(), auth, child, parent, f, comparisons, children, parents, s.input, s.output); err != nil {
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
			eligible, err := foreignKeyIndexEligible(parent, index, f.Referenced)
			if err != nil {
				return err
			}
			if !eligible {
				return ids.ErrCatalogReplaced
			}
			if _, err := bindForeignKeyComparison(auth.Catalog, child, parent, index, f); err != nil {
				return err
			}
			if err := foreignKeyReferencesAllowed(auth.View, auth, parent, f.Referenced); err != nil {
				return err
			}
		}
	}
	return nil
}
