package core

import (
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Descendant targets belong to the active command, not its SELECT sources or
// another CTE. The named root retains the embedded, allocation-free fast path.
func (s *StatementScope) acquireInheritedMutationTargets(statement parser.Statement) error {
	only := true
	switch command := statement.(type) {
	case *parser.Update:
		only = command.Only
	case *parser.Delete:
		only = command.Only
	}
	if only {
		return nil
	}
	e := s.Engine()
	for {
		e.Mu.Lock()
		view := e.PrivateCatalogLocked(s.Owner.Catalog)
		root, ok := view.Relation(s.LockedRef.Address)
		if !ok {
			e.Mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		if root.RelationKind != catalog.TableRelation && root.RelationKind != catalog.PartitionedRelation {
			e.Mu.Unlock()
			return nil
		}
		for _, target := range s.InheritedTargets {
			if _, err := CurrentQuerySourceLocked(e, view, s.Owner.Catalog, target); err != nil {
				e.Mu.Unlock()
				return err
			}
		}
		var pending []exec.Leaf
		collect := func(child *catalog.Relation) error {
			if child.Address() == root.Address() {
				return nil
			}
			for _, target := range s.InheritedTargets {
				if target.Ref.Address == child.Address() {
					return nil
				}
			}
			ref := s.LockedRef
			ref.Address = child.Address()
			pending = append(pending, exec.Leaf{Ref: ref, Relation: child, Storage: e.PrivateStorageLocked(s.Owner.Catalog, child.Address()), Only: true})
			return nil
		}
		var err error
		if root.Partition != nil {
			captured := partition.CaptureCatalog(view, s.PartitionRelations)
			err = partition.PhysicalDescendants(captured, captured.MustRelation(root.Address()), collect)
		} else {
			err = table.InheritanceQueryClosure(view, root, s.Op.Session.SessionToken, collect)
		}
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		for _, target := range pending {
			policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
			request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: target.Ref}, lock.RowExclusive, policy)
			if request != nil {
				s.admissionWaited = true
				if s.ReplacementRead {
					e.ReplacementMu.RUnlock()
					s.ReplacementRead = false
				}
				err = request.Wait()
			}
			if err != nil {
				return err
			}
			s.Owner.Mu.Lock()
			implicit := s.Owner.State == session.TxImplicit
			s.Owner.Mu.Unlock()
			if implicit && !s.ReplacementRead {
				e.ReplacementMu.RLock()
				s.ReplacementRead = true
			}
			e.Mu.Lock()
			_, err = CurrentQuerySourceLocked(e, e.PrivateCatalogLocked(s.Owner.Catalog), s.Owner.Catalog, target)
			if err == nil {
				err = s.ObservePartitionRelationLocked(e, target.Ref)
			}
			if err == nil {
				s.InheritedTargets = append(s.InheritedTargets, target)
				e.markTemporaryAccessLocked(s, target.Ref, temporaryOwnAccess)
			}
			e.Mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

// Descendant replacement authority is a real command target, captured generation
// and row grant. Neither a source grant nor an RI authorization can manufacture it.
func (s *StatementScope) validateInheritedReplacements(ref ids.ObjectRef, old []storage.RowVersion) error {
	if ref == s.LockedRef {
		return nil
	}
	store := s.mutationStorage(ref)
	if store == nil {
		return errors.New("stuntgres: replacement is outside its mutation targets")
	}
	if s.View == nil || s.View.Data().GenerationAt(ref.Address) != store {
		return errors.New("stuntgres: replacement requires its captured target view")
	}
	var small [8]ids.RowID
	target, missing := s.Engine().Locks.ReplacementGrants(s.LockTicket, ref, dml.VersionRows(small[:0], old))
	if s.LockTicket == nil || s.LockTicket.Ref() != s.TxRef || !target {
		return errors.New("stuntgres: replacement requires its target grant")
	}
	data := s.View.Data()
	for i, row := range old {
		if i == missing {
			return errors.New("stuntgres: replacement requires its row grant")
		}
		captured := data.Committed[ref.Address]
		if row.Born == 0 {
			captured = data.Own[ref.Address]
		}
		if _, _, found := storage.RowByID(captured, row.Row, -1); !found {
			return errors.New("stuntgres: replacement is outside its candidate view")
		}
	}
	return nil
}

// mutationStorage is the storage of ref when it is the admitted target or
// one of its inherited targets, else nil.
func (s *StatementScope) mutationStorage(ref ids.ObjectRef) *storage.Relation {
	return s.Command.Storage(s.LockedRef, s.LockedStorage, ref)
}
