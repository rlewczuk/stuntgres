package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
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
	roles := e.roles.read(&e.catalog.Owner, e.catalog.Current(), e.roles.published)
	return entry.CheckActor(roles, op.Session.Authorization.State.CurrentUser)
}

// Called at the canonical publisher's callback-free final boundary.
func (e *Engine) installPreparedLocked(p *session.PreparedPublication) {
	e.visibility.SerializablePreparedLocked(p.Entry.Ref)
	e.visibility.DropSnapshotLocked(p.Entry.Ref)
	e.prepared.InstallLocked(p.Entry)
	p.Origin.Ticket = nil
	clearTransaction(p.Origin)
	p.Origin.Session.RetireTransactionTimeoutLocked()
}

func (e *Engine) finishPreparedLocked(p *session.PreparedPublication) {
	if p == nil {
		return
	}
	e.prepared.RemoveLocked(p.Entry.GID)
	e.endSerializableLocked(p.Entry.Ref, true)
}

func (s *StatementScope) executePrepared(control *parser.TransactionControl, step session.ControlStep, state session.TxState, ref ids.TxRef) error {
	e, t := s.engine(), s.owner
	switch step.Action {
	case session.ControlRefuse:
		return step.Err
	case session.ControlComplete:
		return e.completePrepared(s.op, control.GID, control.Kind == parser.KindCommitPrepared)
	}
	if step.Warning != nil {
		s.op.EmitNotice(session.LevelWarning, *step.Warning)
	}
	if step.Action == session.ControlWarn {
		return nil
	}
	// Every admitted PREPARE, even failure, ends the whole incarnation.
	s.controlOutcome = controlExecuted
	if state == session.TxAborted {
		_, err := e.endAdmitted(s.op, t, ref, false, false, true, s.statementStart, s)
		return err
	}
	t.Mu.Lock()
	err := t.PrepareRefusal(control.GID, payloadOf(t.Session).cursors.hasCurrentHold(ref), ownerHasForeign(t))
	var entry *session.PreparedTransaction
	if err == nil {
		auth, authErr := operationAuthorization(s.op)
		err = authErr
		if err == nil {
			entry = t.PreparedEntry(control.GID, ref, auth.State.CurrentUser, s.op.Session.DatabaseName, time.Now().UTC().Truncate(time.Microsecond))
			e.mu.Lock()
			e.prepared.ReserveLocked()
			e.mu.Unlock()
			s.attachTransactionLocked()
			t.Mu.Unlock()
			err = e.locks.CheckPreparedAdvisory(entry.Ticket)
			if err == nil {
				err = e.checkTransactionExclusions(s)
			}
			if err == nil {
				err = e.publishRowsPrepared(s.op, ref, t.Writes, t.Deletes, t.Catalog, nil, &session.PreparedPublication{Entry: entry, Preparing: true, Origin: t}, s)
			}
			t.Mu.Lock()
		}
	}
	if err != nil {
		ticket := t.Ticket
		t.Ticket = nil
		e.endSerializable(ref, false)
		clearTransaction(t)
		t.Session.RetireTransactionTimeout()
		t.Session.Settings.FinishTransaction(false)
		t.Mu.Unlock()
		e.endLockOwner(ticket)
		return err
	}
	t.Session.Settings.FinishTransaction(true)
	t.Mu.Unlock()
	e.locks.DetachPrepared(entry.Ticket)
	return nil
}

func (e *Engine) completePrepared(op *session.Operation, gid string, commit bool) error {
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	var entry *session.PreparedTransaction
	if err == nil {
		entry, err = e.prepared.ClaimLocked(gid, auth.Roles, auth.State.CurrentUser)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if commit {
		err = e.publishRowsPrepared(op, entry.Ref, entry.Writes, entry.Deletes, entry.Catalog, nil, &session.PreparedPublication{Entry: entry}, nil)
	} else {
		e.mu.Lock()
		op.Session.Mu.Lock()
		err = publicationOperationLocked(e, op)
		if err == nil {
			err = e.preparedPublicationPreflightLocked(op, &session.PreparedPublication{Entry: entry})
		}
		if err == nil {
			e.prepared.RemoveLocked(gid)
			e.endSerializableLocked(entry.Ref, false)
		}
		op.Session.Mu.Unlock()
		e.mu.Unlock()
	}
	if err != nil {
		e.mu.Lock()
		e.prepared.ReleaseLocked(entry)
		e.mu.Unlock()
		return err
	}
	e.endLockOwner(entry.Ticket)
	return nil
}
