package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
	"time"
)

func (e *Engine) preparedPublicationPreflightLocked(op *session.Operation, p *session.PreparedPublication) error {
	if p == nil {
		return nil
	}
	if err := e.prepared.PublicationPreflightLocked(p); err != nil || p.Preparing {
		return err
	}
	return e.preparedActorLocked(op, p.Entry)
}

// Engine.mu and the finishing Session.mu are held at final installation.
func (e *Engine) preparedActorLocked(op *session.Operation, entry *session.PreparedTransaction) error {
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current())
	return entry.CheckActor(roles, op.Session.Authorization.State.CurrentUser)
}

// Called at the canonical publisher's callback-free final boundary.
func (e *Engine) installPreparedLocked(p *session.PreparedPublication) {
	prepare := logicalCommitIdentity{xid: e.Visibility.AssignedXIDLocked(p.Entry.Ref), stamp: time.Now().UnixMicro() - postgresEpochUnixMicros}
	if c := e.advanceOriginSessionLocked(p.Entry.Ref, p.Origin.Session); c != nil {
		prepare.origin = uint32(c.id)
	}
	e.appendLogicalPrepareLocked(p.Entry, prepare)
	e.Visibility.SerializablePreparedLocked(p.Entry.Ref)
	e.Visibility.DropSnapshotLocked(p.Entry.Ref)
	e.prepared.InstallLocked(p.Entry)
	p.Origin.Ticket = nil
	e.prepareReplicationOriginsLocked(p.Entry.Ref, true)
	statsTransactionStopped(p.Origin)
	clearTransactionContents(p.Origin)
	p.Origin.Session.RetireTransactionTimeoutLocked()
}

func (e *Engine) finishPreparedLocked(p *session.PreparedPublication) {
	if p == nil {
		return
	}
	if p.Entry.Catalog != nil {
		e.settleHeapDiscardsLocked(p.Entry.Ref, p.Entry.Catalog.DiscardedHeaps, nil, nil)
	}
	e.prepared.RemoveLocked(p.Entry.GID)
	e.prepareReplicationOriginsLocked(p.Entry.Ref, false)
	e.releaseReplicationOriginsLocked(p.Entry.Ref)
	e.endSerializableLocked(p.Entry.Ref, true)
}

func (s *StatementScope) executePrepared(control *parser.TransactionControl, step session.ControlStep, state session.TxState, ref ids.TxRef) error {
	e, t := s.Engine(), s.Owner
	switch step.Action {
	case session.ControlRefuse:
		return step.Err
	case session.ControlComplete:
		err := e.completePrepared(s.Op, control.GID, control.Kind == parser.KindCommitPrepared)
		s.statsControlOutcome(err == nil)
		return err
	}
	if step.Warning != nil {
		s.Op.EmitNotice(session.LevelWarning, *step.Warning)
	}
	if step.Action == session.ControlWarn {
		s.statsControlOutcome(true)
		return nil
	}
	// Every admitted PREPARE, even failure, ends the whole incarnation.
	s.controlOutcome = controlExecuted
	if state == session.TxAborted {
		_, err := e.endAdmitted(s.Op, t, ref, false, restartNone, true, s.statementStart, s)
		return err
	}
	t.Mu.Lock()
	err := t.PrepareRefusal(control.GID, PayloadOf(t.Session).Cursors.hasCurrentHold(ref), ownerHasForeign(t))
	var entry *session.PreparedTransaction
	if err == nil {
		auth, authErr := OperationAuthorization(s.Op)
		err = authErr
		if err == nil {
			entry = t.PreparedEntry(control.GID, ref, auth.State.CurrentUser, s.Op.Session.DatabaseName, time.Now().UTC().Truncate(time.Microsecond))
			e.Mu.Lock()
			e.prepared.ReserveLocked()
			e.Mu.Unlock()
			s.attachTransactionLocked()
			t.Mu.Unlock()
			err = e.Locks.CheckPreparedAdvisory(entry.Ticket)
			if err == nil {
				err = e.checkTransactionConstraints(s)
			}
			if err == nil {
				t.Mu.Lock()
				// Settlement may have replaced complete maps and the journal.
				entry.Writes, entry.Deletes, entry.Catalog, entry.Stats = t.Writes, t.Deletes, t.Catalog, t.Stats
				entry.GinRevision = t.GinRevision
				entry.Roots, entry.Frontier = t.Roots, t.Frontier
				t.Mu.Unlock()
				err = e.publishRowsPrepared(s.Op, ref, entry.Writes, entry.Deletes, entry.Catalog, s.CatalogAuthority, &session.PreparedPublication{Entry: entry, Preparing: true, Origin: t}, s)
			}
			t.Mu.Lock()
		}
	}
	if err != nil {
		s.AbortStatsPopulation()
		t.Stats.Take(&s.Stage.Stats)
		e.Mu.Lock()
		t.Catalog = e.discardWritesLocked(ref, s.PendingCatalog(), s.Stage.Rows, t.Writes, t.Catalog, s.Stage.DiscardedHeaps)
		e.Mu.Unlock()
		ticket := t.Ticket
		t.Ticket = nil
		e.endSerializable(ref, false)
		clearTransaction(t, false)
		t.Session.RetireTransactionTimeout()
		t.Session.Settings.FinishTransaction(false)
		t.Mu.Unlock()
		e.EndLockOwner(ticket)
		return err
	}
	t.Session.Settings.FinishTransaction(true)
	t.Mu.Unlock()
	e.Locks.DetachPrepared(entry.Ticket)
	return nil
}

func (e *Engine) completePrepared(op *session.Operation, gid string, commit bool) error {
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var entry *session.PreparedTransaction
	var captured session.PreparedTransaction
	if err == nil {
		entry, err = e.prepared.ClaimLocked(gid, auth.Roles, auth.State.CurrentUser)
		if err == nil {
			captured = *entry
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if commit {
		err = e.publishRowsPrepared(op, captured.Ref, captured.Writes, captured.Deletes, captured.Catalog, nil, &session.PreparedPublication{Entry: entry}, nil)
	} else {
		e.Mu.Lock()
		op.Session.Mu.Lock()
		err = PublicationOperationLocked(e, op)
		if err == nil {
			err = e.preparedPublicationPreflightLocked(op, &session.PreparedPublication{Entry: entry})
		}
		if err == nil {
			rollback := logicalCommitIdentity{stamp: time.Now().UnixMicro() - postgresEpochUnixMicros}
			if c := e.advanceOriginSessionLocked(entry.Ref, op.Session); c != nil {
				rollback.origin = uint32(c.id)
			}
			e.appendLogicalFinishLocked(entry, false, rollback)
			e.discardWritesLocked(entry.Ref, entry.Catalog, entry.Writes, nil, nil, nil)
			settleStatsLedger(e, op.Session, &entry.Stats, false)
			retireStatsObjects(e, op.Session, entry.Ref.Epoch(), false, entry.Catalog)
			e.prepared.RemoveLocked(gid)
			e.prepareReplicationOriginsLocked(entry.Ref, false)
			e.releaseReplicationOriginsLocked(entry.Ref)
			e.endSerializableLocked(entry.Ref, false)
		}
		op.Session.Mu.Unlock()
		e.Mu.Unlock()
	}
	if err != nil {
		e.Mu.Lock()
		e.prepared.ReleaseLocked(entry)
		e.Mu.Unlock()
		return err
	}
	// REL_18_6 FinishPreparedTransaction calls AtEOXact_PgStat after removing
	// the prepared entry. Only this successful claimed completion resolves the
	// original outcome; the admitted finisher command records its own outcome.
	outcome := statistics.DatabaseFact{Rollbacks: 1}
	if commit {
		outcome.Commits, outcome.Rollbacks = 1, 0
	}
	addDatabaseStats(e, op.Session, outcome)
	e.EndLockOwner(entry.Ticket)
	return nil
}
