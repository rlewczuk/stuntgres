package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// partitionReplayAllowed is the trust callback of partition.ConsumerBoundary:
// the three Restore ALTER TABLE replays and the DEFAULT replay.
func partitionReplayAllowed(op *session.Operation, auth AuthorizationView) func(parser.Statement, *catalog.Relation) bool {
	return func(statement parser.Statement, d *catalog.Relation) bool {
		switch s := statement.(type) {
		case *parser.AlterTable:
			return partitionCheckReplayAllowed(op, auth, s, d) || partitionUsingIndexReplayAllowed(op, auth, s, d) || partitionUniqueReplayAllowed(op, auth, s, d)
		case *parser.AlterTableDefault:
			return partitionDefaultReplayAllowed(operationEngine(op), op, auth, s)
		}
		return false
	}
}

func partitionDefaultReplayAllowed(e *Engine, op *session.Operation, auth AuthorizationView, statement *parser.AlterTableDefault) bool {
	if e == nil || op == nil || op.Self != op || !operationReplayOrZero(op).defaultReplay || op.Session == nil || engineOf(op.Session) != e || e.importTargets == nil || auth.Owner != &e.catalog.Owner || auth.session != op.Session || auth.RoleRecord != e.roles.Published || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.catalog.Domain || auth.Catalog.State() != e.catalog.State || auth.Catalog.Epoch() != e.catalog.State.Epoch() || statement == nil || statement.Action != parser.SetDefault || statement.Expression == nil {
		return false
	}
	if err := e.validateOperation(op); err != nil {
		return false
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	return err == nil && bootstrap.Superuser
}

// Dump may replay differently owned members. Only the private, authenticated
// importer can open their parent as its bootstrap principal; the child owner
// and all type and scalar-expression authority remain the named CurrentUser.
func (e *Engine) partitionParentAuthorization(op *session.Operation, auth AuthorizationView, syntax *parser.CreateTable) (AuthorizationView, error) {
	if syntax == nil || syntax.PartitionOf == nil || op == nil || !operationReplayOrZero(op).tableDestination {
		return auth, nil
	}
	if e == nil || op.Self != op || op.Session == nil || engineOf(op.Session) != e || e.importTargets == nil || auth.Owner != &e.catalog.Owner || auth.session != op.Session || auth.RoleRecord != e.roles.Published || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.catalog.Domain || auth.Catalog.State() != e.catalog.State || auth.Catalog.Epoch() != e.catalog.State.Epoch() {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition parent authority")
	}
	if err := e.validateOperation(op); err != nil {
		return AuthorizationView{}, err
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition parent authority")
	}
	parentAuth := auth
	parentAuth.State.CurrentUser = bootstrap.Ref
	return parentAuth, nil
}

func (s *StatementScope) acquirePartitionLock(ref ids.ObjectRef, mode lock.Mode) error {
	e := s.engine()
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref}, mode, lock.Policy{Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext})
	if request != nil {
		s.admissionWaited = true
		if len(s.rlsActive) != 0 {
			s.rlsImportWaits++
		}
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.observePartitionRelationLocked(e, ref)
}

// A SELECT's admitted topology is immutable even though AS permits ATTACH SUE.
// Accept only same-authority append growth; all replacement/decrease fences stay.
func (s *StatementScope) observePartitionRelationLocked(e *Engine, ref ids.ObjectRef) error {
	view := e.privateCatalogLocked(s.owner.Catalog)
	d, ok := view.Relation(ref.Address)
	if !ok || ref.Domain() != e.catalog.Domain || ref.Epoch != view.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if old := s.PartitionRelations[ref.Address]; old != nil && old != d && !partition.AppendCompatible(old, d) {
		return ids.ErrCatalogReplaced
	}
	if catalog.HierarchyRelation(d) && s.PartitionRelations[ref.Address] == nil {
		if s.PartitionRelations == nil {
			s.PartitionRelations = make(map[ids.ObjectAddress]*catalog.Relation)
		}
		s.PartitionRelations[ref.Address] = d
	}
	return nil
}

// Ancestors are opened root-first before a leaf lock. In particular a direct
// DEFAULT writer must not hold leaf RX while waiting for its parent's AS:
// a creator owns parent AX before requesting DEFAULT AX.
func (s *StatementScope) acquirePartitionAncestors(ref ids.ObjectRef) error {
	e := s.engine()
	e.mu.Lock()
	ancestors, err := partition.Ancestors(e.privateCatalogLocked(s.owner.Catalog), ref)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	for _, ancestor := range ancestors {
		if err := s.acquirePartitionLock(ancestor, lock.AccessShare); err != nil {
			return err
		}
	}
	return nil
}

// Discover the topology once after the parent grant. Descendant waits must not
// broaden an already admitted SELECT to newly attached branches.
func (s *StatementScope) acquirePartitionDescendants(ref ids.ObjectRef, mode lock.Mode) error {
	e := s.engine()
	e.mu.Lock()
	catalog := e.privateCatalogLocked(s.owner.Catalog)
	d, ok := catalog.Relation(ref.Address)
	if !ok || ref.Epoch != catalog.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	if err := s.observePartitionRelationLocked(e, ref); err != nil {
		e.mu.Unlock()
		return err
	}
	var children []ids.ObjectAddress
	if d.Partition != nil {
		if captured := s.PartitionRelations[ref.Address]; captured != nil {
			d = captured
		}
		children = d.Partition.Children
	}
	e.mu.Unlock()
	for _, address := range children {
		child := ref.Domain().Ref(ref.Epoch, address)
		if err := s.acquirePartitionLock(child, mode); err != nil {
			return err
		}
		if err := s.acquirePartitionDescendants(child, mode); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) acquirePartitionDefinitionDefault(parent ids.ObjectRef) error {
	e := s.engine()
	e.mu.Lock()
	catalog := e.privateCatalogLocked(s.owner.Catalog)
	d, ok := catalog.Relation(parent.Address)
	if !ok || parent.Epoch != catalog.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	if err := s.observePartitionRelationLocked(e, parent); err != nil {
		e.mu.Unlock()
		return err
	}
	var child ids.ObjectRef
	if d.Partition != nil && d.Partition.DefaultChild != (ids.ObjectAddress{}) {
		child = parent.Domain().Ref(parent.Epoch, d.Partition.DefaultChild)
	}
	e.mu.Unlock()
	if child.Domain() == nil {
		return nil
	}
	if err := s.acquirePartitionLock(child, lock.AccessExclusive); err != nil {
		return err
	}
	return s.acquirePartitionDescendants(child, lock.AccessExclusive)
}
