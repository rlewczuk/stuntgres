package engine

import (
	"cmp"
	"errors"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (s *StatementScope) acquireAlterForeignKeys(raw *parser.AlterTable) error {
	count := 0
	for _, action := range raw.Actions {
		if action.Action == parser.AddForeignKey {
			count++
		}
	}
	if count == 0 {
		return nil
	}
	e := s.engine()
	auth, err := operationAuthorization(s.op)
	if err != nil {
		return err
	}
	e.mu.Lock()
	auth.Catalog = e.privateCatalogLocked(s.owner.Catalog)
	auth.Recapture(auth.Catalog)
	childRef, err := e.resolveRelationReference(auth, raw.Name, true)
	if err != nil {
		e.mu.Unlock()
		if raw.IfExists && renameMissing(err) {
			return nil
		}
		return err
	}
	child := auth.Catalog.MustRelation(childRef.Address)
	if err = auth.Check(access.Requirement{Object: childRef, OwnerOnly: true}); err == nil {
		err = analyze.TableKindError(child, "ADD CONSTRAINT")
	}
	if err == nil {
		err = inheritanceAlterActionsGuard(auth.Catalog, child, raw.Actions)
	}
	if err != nil {
		e.mu.Unlock()
		return err
	}
	captures := make([]alterForeignKeyAdmission, count)
	refs := []ids.ObjectRef{childRef}
	stores := map[ids.ObjectAddress]*storage.Relation{childRef.Address: e.privateStorageLocked(s.owner.Catalog, childRef.Address)}
	i := 0
	for _, action := range raw.Actions {
		if action.Action != parser.AddForeignKey {
			continue
		}
		ref, resolveErr := e.resolveReference(auth, action.ForeignKey.Target)
		if resolveErr != nil {
			e.mu.Unlock()
			return alterCookingError(resolveErr)
		}
		captures[i].target = ref
		captures[i].storage = e.privateStorageLocked(s.owner.Catalog, ref.Address)
		if ref != childRef {
			parent, ok := auth.Catalog.Relation(ref.Address)
			if ok && parent.RelationKind == catalog.TableRelation {
				var attributes []int32
				if len(action.ForeignKey.Referenced) == 0 {
					for _, key := range parent.Constraints.Unique {
						if key.Primary {
							attributes = key.Attributes
							break
						}
					}
				} else {
					attributes, _ = foreignKeyAttributes(parent, action.ForeignKey.Referenced)
				}
				if len(attributes) != 0 {
					if index, keyErr := foreignKeyIndex(auth.Catalog, parent, attributes); keyErr == nil {
						captures[i].index = index.Address()
					}
				}
			}
		}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
			stores[ref.Address] = captures[i].storage
		}
		i++
	}
	e.mu.Unlock()
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		mode := lock.ShareRowExclusive
		if ref == childRef {
			mode = alterTableLockMode(raw)
		}
		if err := s.foreignKeyGrant(ref, 0, mode, stores[ref.Address]); err != nil {
			return err
		}
	}
	auth, err = operationAuthorization(s.op)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth.Catalog = e.privateCatalogLocked(s.owner.Catalog)
	auth.Recapture(auth.Catalog)
	sameName := func(name parser.ObjectName, expected ids.ObjectRef) error {
		current, resolveErr := e.resolveReference(auth, name)
		if resolveErr != nil {
			var diagnostic *sqlstate.Error
			if errors.As(resolveErr, &diagnostic) && diagnostic.Code == "42501" {
				return resolveErr
			}
			return ids.ErrCatalogReplaced
		}
		if current != expected {
			return ids.ErrCatalogReplaced
		}
		return nil
	}
	if err := sameName(raw.Name, childRef); err != nil {
		return err
	}
	i = 0
	for _, action := range raw.Actions {
		if action.Action != parser.AddForeignKey {
			continue
		}
		if err := sameName(action.ForeignKey.Target, captures[i].target); err != nil {
			return err
		}
		if captures[i].index != (ids.ObjectAddress{}) {
			if _, ok := auth.Catalog.Relation(captures[i].index); !ok {
				return ids.ErrCatalogReplaced
			}
		}
		i++
	}
	s.foreignActions().alterKeys = captures
	return nil
}

// Only definitions introduced by this ALTER get an initial historical scan.
// The ordinary Finish checker remains responsible for genuine later writes.
func (s *StatementScope) validateAddedForeignKeys(view catalog.Read, child *catalog.Relation, added []ids.OID) error {
	if len(added) == 0 {
		return nil
	}
	auth, err := operationAuthorization(s.op)
	if err != nil {
		return err
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	e := s.engine()
	for _, f := range child.Constraints.ForeignKeys {
		if f.NotValid || !slices.Contains(added, f.OID) {
			continue
		}
		parent, ok := view.Relation(f.Target)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		index, ok := view.Relation(f.Index)
		if !ok {
			return ids.ErrCatalogReplaced
		}
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
	return nil
}

func installedForeignKey(stage *CatalogStage, address ids.ObjectAddress, f catalog.ForeignKeyConstraint) bool {
	parent, ok := stage.Base.Relation(address)
	if !ok {
		return false
	}
	for _, old := range parent.Constraints.ForeignKeys {
		if old.Name == f.Name && old.Target == f.Target && old.Full == f.Full && old.NotValid == f.NotValid && old.UpdateAction == f.UpdateAction && old.DeleteAction == f.DeleteAction && slices.Equal(old.DeleteAttributes, f.DeleteAttributes) && slices.Equal(old.Attributes, f.Attributes) && slices.Equal(old.Referenced, f.Referenced) {
			return true
		}
	}
	return false
}
