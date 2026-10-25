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
	"stuntgres/internal/engine/table"
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
	e := s.Engine()
	auth, err := OperationAuthorization(s.Op)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	auth.Catalog = e.PrivateCatalogLocked(s.Owner.Catalog)
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
		err = analyze.TableKindError(child, "ADD CONSTRAINT")
	}
	if err == nil {
		err = table.InheritanceAlterActionsGuard(auth.Catalog, child, raw.Actions)
	}
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	captures := make([]table.AlterForeignKeyAdmission, count)
	refs := []ids.ObjectRef{childRef}
	stores := map[ids.ObjectAddress]*storage.Relation{childRef.Address: e.PrivateStorageLocked(s.Owner.Catalog, childRef.Address)}
	i := 0
	for _, action := range raw.Actions {
		if action.Action != parser.AddForeignKey {
			continue
		}
		ref, resolveErr := e.ResolveReference(auth, action.ForeignKey.Target)
		if resolveErr != nil {
			e.Mu.Unlock()
			return table.AlterCookingError(resolveErr)
		}
		captures[i].Target = ref
		captures[i].Storage = e.PrivateStorageLocked(s.Owner.Catalog, ref.Address)
		if ref != childRef {
			captures[i].Index = table.AlterForeignKeyIndex(auth.Catalog, ref.Address, *action.ForeignKey)
		}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
			stores[ref.Address] = captures[i].Storage
		}
		i++
	}
	e.Mu.Unlock()
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		mode := lock.ShareRowExclusive
		if ref == childRef {
			mode = table.AlterTableLockMode(raw)
		}
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
	auth.Catalog = e.PrivateCatalogLocked(s.Owner.Catalog)
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
	return nil
}

// validateForeignKeys scans the existing rows of checks, keys of the
// candidate view, as the operation's authority over view. Only definitions an
// ALTER introduced or whose values it changed get this initial historical
// scan; the ordinary Finish checker remains responsible for later writes.
func (s *StatementScope) validateForeignKeys(view catalog.Read, checks []table.ForeignKeyCheck) error {
	auth, err := OperationAuthorization(s.Op)
	if err != nil {
		return err
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	scan := table.ForeignKeyScan{View: auth.View, Env: auth, Domain: s.Engine().Catalog.Domain, Capture: s.CaptureForeignKeyRows, Input: s.Input, Output: s.Output}
	return table.ScanForeignKeys(s.Op.Context(), scan, checks)
}
