package core

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
			return partitionCheckReplayAllowed(op, auth, s, d) || partitionUsingIndexReplayAllowed(op, auth, s, d) || PartitionUniqueReplayAllowed(op, auth, s, d)
		case *parser.AlterTableDefault:
			return PartitionDefaultReplayAllowed(operationEngine(op), op, auth, s)
		}
		return false
	}
}

func PartitionDefaultReplayAllowed(e *Engine, op *session.Operation, auth AuthorizationView, statement *parser.AlterTableDefault) bool {
	if e == nil || op == nil || op.Self != op || !OperationReplayOrZero(op).DefaultReplay || op.Session == nil || EngineOf(op.Session) != e || e.ImportTargets == nil || auth.Owner != &e.Catalog.Owner || auth.Session != op.Session || auth.RoleRecord != e.RoleStore.Published || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.Catalog.Domain || auth.Catalog.State() != e.Catalog.State || auth.Catalog.Epoch() != e.Catalog.State.Epoch() || statement == nil || statement.Action != parser.SetDefault || statement.Expression == nil {
		return false
	}
	if err := e.ValidateOperation(op); err != nil {
		return false
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	return err == nil && bootstrap.Superuser
}

// Dump may replay differently owned members. Only the private, authenticated
// importer can open their parent as its bootstrap principal; the child owner
// and all type and scalar-expression authority remain the named CurrentUser.
func (e *Engine) PartitionParentAuthorization(op *session.Operation, auth AuthorizationView, syntax *parser.CreateTable) (AuthorizationView, error) {
	if syntax == nil || syntax.PartitionOf == nil || op == nil || !OperationReplayOrZero(op).TableDestination {
		return auth, nil
	}
	if e == nil || op.Self != op || op.Session == nil || EngineOf(op.Session) != e || e.ImportTargets == nil || auth.Owner != &e.Catalog.Owner || auth.Session != op.Session || auth.RoleRecord != e.RoleStore.Published || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.Catalog.Domain || auth.Catalog.State() != e.Catalog.State || auth.Catalog.Epoch() != e.Catalog.State.Epoch() {
		return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore partition parent authority")
	}
	if err := e.ValidateOperation(op); err != nil {
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

func (s *StatementScope) AcquirePartitionLock(ref ids.ObjectRef, mode lock.Mode) error {
	e := s.Engine()
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: ref}, mode, lock.Policy{Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext})
	if request != nil {
		s.admissionWaited = true
		if len(s.rlsActive) != 0 {
			s.rlsImportWaits++
		}
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return s.ObservePartitionRelationLocked(e, ref)
}

// A SELECT's admitted topology is immutable even though AS permits ATTACH SUE.
// Accept only same-authority append growth; all replacement/decrease fences stay.
func (s *StatementScope) ObservePartitionRelationLocked(e *Engine, ref ids.ObjectRef) error {
	view := e.PrivateCatalogLocked(s.Owner.Catalog)
	d, ok := view.Relation(ref.Address)
	if !ok || ref.Domain() != e.Catalog.Domain || ref.Epoch != view.Epoch() {
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
	e := s.Engine()
	e.Mu.Lock()
	ancestors, err := partition.Ancestors(e.PrivateCatalogLocked(s.Owner.Catalog), ref)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	for _, ancestor := range ancestors {
		if err := s.AcquirePartitionLock(ancestor, lock.AccessShare); err != nil {
			return err
		}
	}
	return nil
}

// Discover the topology once after the parent grant. Descendant waits must not
// broaden an already admitted SELECT to newly attached branches.
func (s *StatementScope) AcquirePartitionDescendants(ref ids.ObjectRef, mode lock.Mode) error {
	e := s.Engine()
	e.Mu.Lock()
	catalog := e.PrivateCatalogLocked(s.Owner.Catalog)
	d, ok := catalog.Relation(ref.Address)
	if !ok || ref.Epoch != catalog.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	if err := s.ObservePartitionRelationLocked(e, ref); err != nil {
		e.Mu.Unlock()
		return err
	}
	var children []ids.ObjectAddress
	if d.Partition != nil {
		if captured := s.PartitionRelations[ref.Address]; captured != nil {
			d = captured
		}
		children = d.Partition.Children
	}
	e.Mu.Unlock()
	for _, address := range children {
		child := ref.Domain().Ref(ref.Epoch, address)
		if err := s.AcquirePartitionLock(child, mode); err != nil {
			return err
		}
		if err := s.AcquirePartitionDescendants(child, mode); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) acquirePartitionDefinitionDefault(parent ids.ObjectRef) error {
	e := s.Engine()
	e.Mu.Lock()
	catalog := e.PrivateCatalogLocked(s.Owner.Catalog)
	d, ok := catalog.Relation(parent.Address)
	if !ok || parent.Epoch != catalog.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	if err := s.ObservePartitionRelationLocked(e, parent); err != nil {
		e.Mu.Unlock()
		return err
	}
	var child ids.ObjectRef
	if d.Partition != nil && d.Partition.DefaultChild != (ids.ObjectAddress{}) {
		child = parent.Domain().Ref(parent.Epoch, d.Partition.DefaultChild)
	}
	e.Mu.Unlock()
	if child.Domain() == nil {
		return nil
	}
	if err := s.AcquirePartitionLock(child, lock.AccessExclusive); err != nil {
		return err
	}
	return s.AcquirePartitionDescendants(child, lock.AccessExclusive)
}
