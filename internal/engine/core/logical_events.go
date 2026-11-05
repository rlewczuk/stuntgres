package core

import (
	"errors"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/logical"
	"stuntgres/internal/engine/session"
)

// appendLogicalEventLocked publishes immutable evidence at the actual staging
// boundary, separately from the rollback-clipped successful-effect projection.
// Owner admission and Engine.Mu are held; callbacks never run here.
func (s *StatementScope) appendLogicalEventLocked(kind logical.RecordKind, c *logical.Change) {
	s.Engine().appendLogicalEventLocked(s.TxRef, kind, c)
}
func (e *Engine) appendLogicalEventLocked(ref ids.TxRef, kind logical.RecordKind, c *logical.Change) {
	if !e.logicalCaptureLocked() {
		return
	}
	xid := e.Visibility.AssignedXIDLocked(ref)
	if xid == 0 {
		return
	}
	owner := session.TxOwnerOf(ref)
	if owner == nil {
		return
	}
	lsn := uint64(0)
	if c != nil {
		lsn = c.LSN
	}
	if lsn == 0 {
		lsn = e.Cluster.logical.lsn.Allocate()
	}
	child, parent := logicalChild(owner, xid)
	origin := uint32(PayloadOf(owner.Session).origin.id)
	if c != nil {
		c.TopXID, c.SubXID, c.Command, c.Origin = xid, child, uint32(owner.Command), origin
	}
	anchor := e.logicalHistory.Anchor(xid, lsn)
	e.logicalHistory.Append(logical.Record{Kind: kind, LSN: lsn, Origin: origin, Txn: anchor, Child: child, Parent: parent, Statement: uint32(owner.Command), Change: c})
}
func logicalChild(t *session.TransactionOwner, top uint32) (uint32, uint32) {
	child, parent := top, top
	for _, m := range t.Savepoints {
		if m.FullXID != 0 {
			parent = child
			child = uint32(m.FullXID)
		}
	}
	return child, parent
}
func ownLogicalChange(c logical.Change) *logical.Change {
	// Accepted storage and rewrite vectors are already immutable. Clip capacity
	// and share them; only pre-index producer scratch detaches below.
	c.New = c.New[:len(c.New):len(c.New)]
	c.Old = c.Old[:len(c.Old):len(c.Old)]
	c.Truncated = c.Truncated[:len(c.Truncated):len(c.Truncated)]
	c.DecodedBytes = c.DecodingSize()
	return &c
}

// StageInsertAttempt is the actual pre-index insertion stage. Failed uniqueness
// retains this evidence but never installs a table row or successful projection.
func (h *statementHost) StageInsertAttempt(ref ids.ObjectRef, c *index.WriteCandidate) error {
	if c.Logical != nil {
		return nil
	}
	s := h.scope()
	e := s.Engine()
	s.Owner.Mu.Lock()
	defer s.Owner.Mu.Unlock()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if !e.logicalCaptureLocked() {
		return nil
	}
	if !s.worked || s.Owner.State == session.TxIdle || s.Owner.State == session.TxAborted {
		return errors.New("stuntgres: inactive write stage")
	}
	if err := s.Op.Context().Err(); err != nil {
		return err
	}
	if ref.Domain() != e.Catalog.Domain {
		return errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	relation := s.logicalRelationLocked(e, ref)
	if relation == nil {
		return nil
	}
	if len(c.Values) != len(relation.Columns) {
		return errors.New("stuntgres: invalid staged row width")
	}
	if err := e.AssignTransactionXIDLocked(s.TxRef); err != nil {
		return err
	}
	c.Logical = ownLogicalChange(logical.Change{LSN: e.Cluster.logical.lsn.Allocate(), Kind: logical.Insert, Relation: relation, New: slices.Clone(c.Values)})
	s.appendLogicalEventLocked(logical.RowEvent, c.Logical)
	return nil
}

// abortLogicalTransactionLocked runs before canonical completion consumes the
// top assignment. Already-committed/prepared/aborted owners have no open anchor.
func (e *Engine) abortLogicalTransactionLocked(ref ids.TxRef) {
	if !e.logicalCaptureLocked() {
		return
	}
	xid := e.Visibility.AssignedXIDLocked(ref)
	anchor := e.logicalHistory.Active(xid)
	if anchor == nil {
		return
	}
	e.logicalHistory.Append(logical.Record{Kind: logical.Abort, LSN: e.Cluster.logical.lsn.Allocate(), Txn: anchor})
}

// abortLogicalChildrenLocked records one actual subtree abort and retires every
// discarded generation. Descendants released earlier remain canonical children.
func (e *Engine) abortLogicalChildrenLocked(t *session.TransactionOwner, serial uint64) {
	top := e.Visibility.AssignedXIDLocked(t.Ref)
	for i := range t.Savepoints {
		m := &t.Savepoints[i]
		if m.Serial != serial {
			continue
		}
		if m.FullXID != 0 {
			e.Visibility.AbortChildLocked(t.Ref, m.FullXID)
			if anchor := e.logicalHistory.Active(top); anchor != nil {
				parent := top
				if i > 0 && t.Savepoints[i-1].FullXID != 0 {
					parent = uint32(t.Savepoints[i-1].FullXID)
				}
				e.logicalHistory.Append(logical.Record{Kind: logical.SubAbort, LSN: e.Cluster.logical.lsn.Allocate(), Txn: anchor, Child: uint32(m.FullXID), Parent: parent})
			}
		}
		for j := i; j < len(t.Savepoints); j++ {
			t.Savepoints[j].FullXID = 0
		}
		return
	}
}
func (e *Engine) releaseLogicalChildrenLocked(t *session.TransactionOwner, position int) {
	top := e.Visibility.AssignedXIDLocked(t.Ref)
	anchor := e.logicalHistory.Active(top)
	parent := e.Visibility.FullXIDLocked(t.Ref)
	if position > 0 {
		parent = t.Savepoints[position-1].FullXID
	}
	for i := len(t.Savepoints) - 1; i >= position; i-- {
		m := t.Savepoints[i]
		if m.FullXID == 0 {
			continue
		}
		e.Visibility.ReleaseChildLocked(m.FullXID, parent)
		if anchor != nil {
			e.logicalHistory.Append(logical.Record{Kind: logical.SubCommit, LSN: e.Cluster.logical.lsn.Allocate(), Txn: anchor, Child: uint32(m.FullXID), Parent: uint32(parent)})
		}
	}
}

// finishLogicalStatementLocked records the actual command outcome while the
// top owner is still open. A top-level implicit terminal already owns its end.
func (s *StatementScope) finishLogicalStatementLocked(success bool) {
	e := s.Engine()
	xid := e.Visibility.AssignedXIDLocked(s.TxRef)
	if e.logicalHistory.Active(xid) == nil {
		return
	}
	kind := logical.StatementAbort
	if success {
		kind = logical.StatementEnd
	}
	s.appendLogicalEventLocked(kind, nil)
}
