package core

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
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (s *StatementScope) acquireAlterForeignKeys(raw *parser.AlterTable) error {
	count := 0
	for _, action := range raw.Actions {
		if action.Action == parser.AddForeignKey || action.Action == parser.ValidateConstraint {
			count++
		}
	}
	if count == 0 {
		return nil
	}
	e := s.Engine()
	auth, err := OperationAuthorization(s.Op)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	auth.Catalog = e.PrivateCatalogLocked(s.PendingCatalog())
	auth.Recapture(auth.Catalog)
	childRef, err := e.ResolveRelationReference(auth, raw.Name, true)
	if err != nil {
		e.Mu.Unlock()
		if raw.IfExists && table.RenameMissing(err) {
			return nil
		}
		return err
	}
	child := auth.Catalog.MustRelation(childRef.Address)
	if err = auth.Check(access.Requirement{Object: childRef, OwnerOnly: true}); err == nil {
		label := "VALIDATE CONSTRAINT"
		if slices.ContainsFunc(raw.Actions, func(a parser.AlterTableAction) bool { return a.Action == parser.AddForeignKey }) {
			label = "ADD CONSTRAINT"
		}
		err = analyze.TableKindError(child, label)
	}
	if err == nil {
		err = table.InheritanceAlterActionsGuard(auth.Catalog, child, raw.Actions)
	}
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	captures := make([]table.AlterForeignKeyAdmission, 0, count)
	var validations []table.AlterForeignKeyAdmission
	// Exact descriptor originals belong to stored-key validation only. ADD
	// captures names/storage/supporting keys, then reopens current REFERENCES
	// authority during ordinary definition cooking after its unlocked waits.
	// An ACL replacement must not preempt that privilege diagnostic.
	originals := make(map[ids.ObjectAddress]catalog.Descriptor)
	modes := map[ids.ObjectAddress]lock.Mode{childRef.Address: table.AlterTableLockMode(raw)}
	refs := []ids.ObjectRef{childRef}
	stores := map[ids.ObjectAddress]*storage.Relation{childRef.Address: e.PrivateStorageLocked(s.PendingCatalog(), childRef.Address)}
	i := 0
	for _, action := range raw.Actions {
		if action.Action == parser.ValidateConstraint {
			// Earlier DROP/ADD may replace the name; only an authentic incumbent
			// invalid FK contributes existing-key admission here.
			selected, selectErr := table.SelectAlterConstraintValidation(child, action.Constraint.Text)
			if selectErr != nil || selected.ForeignKey == nil || !selected.Invalid() {
				continue
			}
			fk := selected.ForeignKey
			ref := childRef
			ref.Address = fk.Target
			parent, ok := auth.Catalog.Relation(ref.Address)
			if !ok {
				e.Mu.Unlock()
				return ids.ErrCatalogReplaced
			}
			provider, ok := auth.Catalog.Relation(fk.Index)
			if !ok {
				e.Mu.Unlock()
				return ids.ErrCatalogReplaced
			}
			originals[fk.Index] = provider
			originals[childRef.Address] = child
			originals[ref.Address] = parent
			capture := table.AlterForeignKeyAdmission{Target: ref, Storage: e.PrivateStorageLocked(s.PendingCatalog(), ref.Address), Index: fk.Index, Constraint: fk.OID, Parent: parent, Provider: provider}
			validations = append(validations, capture)
			if !slices.Contains(refs, ref) {
				refs = append(refs, ref)
				stores[ref.Address] = capture.Storage
				modes[ref.Address] = lock.RowShare
			}
			continue
		}
		if action.Action != parser.AddForeignKey {
			continue
		}
		ref, resolveErr := e.ResolveReference(auth, action.ForeignKey.Target)
		if resolveErr != nil {
			e.Mu.Unlock()
			return table.AlterCookingError(resolveErr)
		}
		captures = append(captures, table.AlterForeignKeyAdmission{})
		captures[i].Target = ref
		captures[i].Storage = e.PrivateStorageLocked(s.PendingCatalog(), ref.Address)
		if ref != childRef {
			captures[i].Index = table.AlterForeignKeyIndex(auth.Catalog, ref.Address, *action.ForeignKey)
		}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
			stores[ref.Address] = captures[i].Storage
		}
		if ref != childRef {
			modes[ref.Address] = lock.ShareRowExclusive
		}
		i++
	}
	e.Mu.Unlock()
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		mode := modes[ref.Address]
		if err := s.ForeignKeyGrant(ref, 0, mode, stores[ref.Address]); err != nil {
			return err
		}
	}
	auth, err = OperationAuthorization(s.Op)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth.Catalog = e.PrivateCatalogLocked(s.PendingCatalog())
	auth.Recapture(auth.Catalog)
	sameName := func(name parser.ObjectName, expected ids.ObjectRef) error {
		current, resolveErr := e.ResolveReference(auth, name)
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
	for address, original := range originals {
		if auth.Catalog.Get(address) != original {
			return ids.ErrCatalogReplaced
		}
	}
	for _, capture := range validations {
		if _, ok := auth.Catalog.Relation(capture.Index); !ok {
			return ids.ErrCatalogReplaced
		}
	}
	i = 0
	for _, action := range raw.Actions {
		if action.Action != parser.AddForeignKey {
			continue
		}
		if err := sameName(action.ForeignKey.Target, captures[i].Target); err != nil {
			return err
		}
		if captures[i].Index != (ids.ObjectAddress{}) {
			if _, ok := auth.Catalog.Relation(captures[i].Index); !ok {
				return ids.ErrCatalogReplaced
			}
		}
		i++
	}
	s.foreignKeys().AlterKeys = captures
	s.foreignKeys().ValidationKeys = validations
	return nil
}
