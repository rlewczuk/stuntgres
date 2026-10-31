package core

import (
	"fmt"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// ClusterInUse checks genuine retained portals and deferred transition events.
// The caller owns session admission; it may hold Engine.Mu.
func (s *StatementScope) ClusterInUse(view catalog.Read, relation *catalog.Relation) error {
	if err := s.relationCursorPreflight(view, relation.Address(), "CLUSTER"); err != nil {
		return err
	}
	if s.HasPendingConstraintEvents(relation.Address()) {
		return SQLError("55006", fmt.Sprintf(`cannot CLUSTER "%s" because it has pending trigger events`, relation.Name()))
	}
	return nil
}

// CompleteClusterTarget publishes one bare-CLUSTER target with the ordinary
// transaction publisher, then retires its owner/ticket before the next target.
// Named CLUSTER never calls this: its journal and locks survive until user end.
func (s *StatementScope) CompleteClusterTarget() error {
	e, t := s.Engine(), s.Owner
	if t.InBlock() || t.State != session.TxImplicit {
		return SQLError("25001", "CLUSTER cannot run inside a transaction block")
	}
	if err := e.publishRows(s.Op, s.TxRef, s.Stage.Rows, s.Stage.Deletes, s.Catalog, s.CatalogAuthority, s); err != nil {
		return err
	}
	t.Mu.Lock()
	ticket := t.Ticket
	t.Ticket = nil
	t.Session.Settings.FinishTransaction(true)
	e.endSerializable(t.Ref, true)
	clearTransaction(t, true)
	t.Session.RetireTransactionTimeout()
	t.Mu.Unlock()
	e.EndLockOwner(ticket)
	s.Catalog, s.CatalogAuthority = nil, nil
	s.Stage = WriteStage{scope: s}
	s.Stage.self = &s.Stage
	s.OwnWrites, s.OwnDeletes = nil, nil
	s.ProjectedRoots = false
	s.LockedRef, s.LockedDescriptor, s.LockedStorage = ids.ObjectRef{}, nil, nil
	s.transactionStart = time.Now().UTC().Truncate(time.Microsecond)
	t.Mu.Lock()
	err := s.admitTransactionLocked(e)
	t.Mu.Unlock()
	return err
}
