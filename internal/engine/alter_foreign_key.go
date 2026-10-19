package engine

import (
	"cmp"
	"errors"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// External providers are captured before any wait. Self references instead bind
// against the completed private candidate, which may own a newly added key.
type alterForeignKeyAdmission struct {
	target  ids.ObjectRef
	storage *storage.Relation
	index   ids.ObjectAddress
}

func alterTableLockMode(raw *parser.AlterTable) lock.Mode {
	for _, action := range raw.Actions {
		if action.Action != parser.AddForeignKey {
			return lock.AccessExclusive
		}
	}
	if len(raw.Actions) == 0 {
		return lock.AccessExclusive
	}
	return lock.ShareRowExclusive
}

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
	e := s.op.session.Engine()
	auth, err := s.op.Authorization()
	if err != nil {
		return err
	}
	e.mu.Lock()
	auth.catalog = e.privateCatalogLocked(s.owner.catalog)
	auth.Recapture(auth.catalog)
	childRef, err := e.resolveRelationReference(auth, raw.Name, true)
	if err != nil {
		e.mu.Unlock()
		if raw.IfExists && renameMissing(err) {
			return nil
		}
		return err
	}
	child := auth.catalog.MustRelation(childRef.Address)
	if err = auth.Check(AccessRequirement{Object: childRef, OwnerOnly: true}); err == nil {
		err = tableKind(child, "ADD CONSTRAINT")
	}
	if err == nil {
		err = inheritanceAlterActionsGuard(auth.catalog, child, raw.Actions)
	}
	if err != nil {
		e.mu.Unlock()
		return err
	}
	captures := make([]alterForeignKeyAdmission, count)
	refs := []ids.ObjectRef{childRef}
	stores := map[ids.ObjectAddress]*storage.Relation{childRef.Address: e.privateStorageLocked(s.owner.catalog, childRef.Address)}
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
		captures[i].storage = e.privateStorageLocked(s.owner.catalog, ref.Address)
		if ref != childRef {
			parent, ok := auth.catalog.Relation(ref.Address)
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
					if index, keyErr := foreignKeyIndex(auth.catalog, parent, attributes); keyErr == nil {
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
	auth, err = s.op.Authorization()
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth.catalog = e.privateCatalogLocked(s.owner.catalog)
	auth.Recapture(auth.catalog)
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
			if _, ok := auth.catalog.Relation(captures[i].index); !ok {
				return ids.ErrCatalogReplaced
			}
		}
		i++
	}
	s.alterForeignKeys = captures
	return nil
}

// Only definitions introduced by this ALTER get an initial historical scan.
// The ordinary Finish checker remains responsible for genuine later writes.
func (s *StatementScope) validateAddedForeignKeys(view catalog.Read, child *catalog.Relation, added []ids.OID) error {
	if len(added) == 0 {
		return nil
	}
	auth, err := s.op.Authorization()
	if err != nil {
		return err
	}
	auth.catalog = view
	auth.Recapture(auth.catalog)
	e := s.op.session.Engine()
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
