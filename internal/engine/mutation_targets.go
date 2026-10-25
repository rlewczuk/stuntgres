package engine

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
	e := s.engine()
	for {
		e.mu.Lock()
		view := e.privateCatalogLocked(s.owner.Catalog)
		root, ok := view.Relation(s.lockedRef.Address)
		if !ok {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		if root.RelationKind != catalog.TableRelation && root.RelationKind != catalog.PartitionedRelation {
			e.mu.Unlock()
			return nil
		}
		for _, target := range s.InheritedTargets {
			if _, err := currentQuerySourceLocked(e, view, s.owner.Catalog, target); err != nil {
				e.mu.Unlock()
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
			ref := s.lockedRef
			ref.Address = child.Address()
			pending = append(pending, exec.Leaf{Ref: ref, Relation: child, Storage: e.privateStorageLocked(s.owner.Catalog, child.Address()), Only: true})
			return nil
		}
		var err error
		if root.Partition != nil {
			captured := partition.CaptureCatalog(view, s.PartitionRelations)
			err = partition.PhysicalDescendants(captured, captured.MustRelation(root.Address()), collect)
		} else {
			err = table.InheritanceQueryClosure(view, root, s.op.Session.SessionToken, collect)
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		for _, target := range pending {
			policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
			request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: target.Ref}, lock.RowExclusive, policy)
			if request != nil {
				s.admissionWaited = true
				if s.replacementRead {
					e.replacementMu.RUnlock()
					s.replacementRead = false
				}
				err = request.Wait()
			}
			if err != nil {
				return err
			}
			s.owner.Mu.Lock()
			implicit := s.owner.State == session.TxImplicit
			s.owner.Mu.Unlock()
			if implicit && !s.replacementRead {
				e.replacementMu.RLock()
				s.replacementRead = true
			}
			e.mu.Lock()
			_, err = currentQuerySourceLocked(e, e.privateCatalogLocked(s.owner.Catalog), s.owner.Catalog, target)
			if err == nil {
				err = s.observePartitionRelationLocked(e, target.Ref)
			}
			if err == nil {
				s.InheritedTargets = append(s.InheritedTargets, target)
				e.markTemporaryAccessLocked(s, target.Ref, temporaryOwnAccess)
			}
			e.mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

// Descendant replacement authority is a real command target, captured generation
// and row grant. Neither a source grant nor an RI authorization can manufacture it.
func (s *StatementScope) validateInheritedReplacements(ref ids.ObjectRef, old []storage.RowVersion) error {
	if ref == s.lockedRef {
		return nil
	}
	store := s.mutationStorage(ref)
	if store == nil {
		return errors.New("stuntgres: replacement is outside its mutation targets")
	}
	if s.view == nil || s.view.Data().GenerationAt(ref.Address) != store {
		return errors.New("stuntgres: replacement requires its captured target view")
	}
	var small [8]ids.RowID
	target, missing := s.engine().locks.ReplacementGrants(s.ticket, ref, dml.VersionRows(small[:0], old))
	if s.ticket == nil || s.ticket.Ref() != s.txRef || !target {
		return errors.New("stuntgres: replacement requires its target grant")
	}
	data := s.view.Data()
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
	return s.Command.Storage(s.lockedRef, s.lockedStorage, ref)
}
