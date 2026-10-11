package engine

import (
	"errors"
	"time"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The registry owns these roots and the original lock/SSI identity after detach.
// No session attachment is consulted to authorize completion.
type preparedTransaction struct {
	gid      string
	owner    PrincipalRef
	ref      TxRef
	prepared time.Time
	database string
	ticket   *relationTicket
	writes   map[ObjectAddress][]rowVersion
	deletes  map[ObjectAddress]*privateDeletionRoot
	catalog  *catalogChanges
	claimed  bool
	roots    map[ObjectAddress]transactionRoot
	frontier CommitSeq
	options  TxOptions
}

type preparedPublication struct {
	entry     *preparedTransaction
	preparing bool
	origin    *transactionOwner
}

func (e *Engine) preparedPublicationPreflightLocked(op *Operation, p *preparedPublication) error {
	if p == nil {
		return nil
	}
	if p.preparing {
		if p.origin.ref != p.entry.ref || !p.origin.inBlock() {
			return errors.New("stuntgres: stale preparing transaction")
		}
		if e.prepared[p.entry.gid] != nil {
			return sqlError("42710", `transaction identifier "`+p.entry.gid+`" is already in use`)
		}
	} else if e.prepared[p.entry.gid] != p.entry || !p.entry.claimed {
		return errors.New("stuntgres: stale prepared publication")
	}
	if !p.preparing {
		return e.preparedActorLocked(op, p.entry)
	}
	return nil
}

// Engine.mu and the finishing Session.mu are held at final installation.
func (e *Engine) preparedActorLocked(op *Operation, entry *preparedTransaction) error {
	roles := RoleRead{e, e.roles}
	current := op.session.authorization.state.CurrentUser
	if err := roles.validatePrincipal(current); err != nil {
		return err
	}
	actor, exists := roles.state.roles[current]
	if current != entry.owner && (!exists || !actor.Superuser) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to finish prepared transaction", Hint: "Must be superuser or the user that prepared the transaction."}
	}
	return nil
}

// Called at the canonical publisher's callback-free final boundary.
func (e *Engine) installPreparedLocked(p *preparedPublication) {
	e.serializablePreparedLocked(p.entry.ref)
	e.dropVisibilitySnapshotLocked(p.entry.ref)
	e.prepared[p.entry.gid] = p.entry
	p.origin.ticket = nil
	p.origin.clear()
	p.origin.session.retireTransactionTimeoutLocked()
}

func (e *Engine) finishPreparedLocked(p *preparedPublication) {
	if p == nil {
		return
	}
	delete(e.prepared, p.entry.gid)
	e.endSerializableLocked(p.entry.ref, true)
}

func (s *StatementScope) executePrepared(control *parser.TransactionControl, state TxState, ref TxRef) error {
	e, t := s.op.session.Engine(), s.owner
	if control.Kind != parser.KindPrepareTransaction {
		if state != TxIdle {
			word := "COMMIT"
			if control.Kind == parser.KindRollbackPrepared {
				word = "ROLLBACK"
			}
			return sqlError("25001", word+" PREPARED cannot run inside a transaction block")
		}
		return e.completePrepared(s.op, control.GID, control.Kind == parser.KindCommitPrepared)
	}
	if state == TxIdle {
		s.op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "there is no transaction in progress"})
		return nil
	}
	if state == TxImplicit {
		s.op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "there is no transaction in progress"})
	}
	// Every admitted PREPARE, even failure, ends the whole incarnation.
	s.controlOutcome = controlExecuted
	if state == TxAborted {
		_, err := e.endAdmitted(s.op, t, ref, false, false, true, s.statementStart)
		return err
	}
	t.mu.Lock()
	var err error
	if t.session.cursors.hasCurrentHold(ref) {
		err = sqlError("0A000", "cannot PREPARE a transaction that has created a cursor WITH HOLD")
	}
	if err == nil && len(control.GID) >= 200 {
		err = sqlError("22023", `transaction identifier "`+control.GID+`" is too long`)
	}
	if err == nil && t.accessedTemporary {
		err = sqlError("0A000", "cannot PREPARE a transaction that has operated on temporary objects")
	}
	var entry *preparedTransaction
	if err == nil {
		auth, authErr := s.op.Authorization()
		err = authErr
		if err == nil {
			entry = &preparedTransaction{gid: control.GID, owner: auth.state.CurrentUser, ref: ref, prepared: time.Now().UTC().Truncate(time.Microsecond), database: s.op.session.databaseName, ticket: t.ticket, writes: t.writes, deletes: t.deletes, catalog: t.catalog, roots: t.roots, frontier: t.frontier, options: TxOptions{Isolation: t.isolation, ReadOnly: t.readOnly, Deferrable: t.deferrable}}
			e.mu.Lock()
			if e.prepared == nil {
				e.prepared = make(map[string]*preparedTransaction)
			}
			e.mu.Unlock()
			t.mu.Unlock()
			err = e.checkTransactionExclusions(s.op, t)
			if err == nil {
				err = e.publishRowsPrepared(s.op, ref, t.writes, t.deletes, t.catalog, nil, &preparedPublication{entry: entry, preparing: true, origin: t})
			}
			t.mu.Lock()
		}
	}
	if err != nil {
		ticket := t.ticket
		t.ticket = nil
		e.endSerializable(ref, false)
		t.clear()
		t.session.retireTransactionTimeout()
		t.session.settings.finishTransaction(false)
		t.mu.Unlock()
		e.locks.end(ticket)
		return err
	}
	t.session.settings.finishTransaction(true)
	t.mu.Unlock()
	e.locks.detachPrepared(entry.ticket)
	return nil
}

func (e *Engine) completePrepared(op *Operation, gid string, commit bool) error {
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	entry := e.prepared[gid]
	if err == nil && entry == nil {
		err = sqlError("42704", `prepared transaction with identifier "`+gid+`" does not exist`)
	}
	if err == nil {
		actor, exists := auth.roles.state.roles[auth.state.CurrentUser]
		if auth.state.CurrentUser != entry.owner && (!exists || !actor.Superuser) {
			err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to finish prepared transaction", Hint: "Must be superuser or the user that prepared the transaction."}
		}
	}
	if err == nil && entry.claimed {
		err = sqlError("55000", `prepared transaction with identifier "`+gid+`" is busy`)
	}
	if err == nil {
		entry.claimed = true
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if commit {
		err = e.publishRowsPrepared(op, entry.ref, entry.writes, entry.deletes, entry.catalog, nil, &preparedPublication{entry: entry})
	} else {
		e.mu.Lock()
		op.session.mu.Lock()
		err = publicationOperationLocked(e, op)
		if err == nil {
			err = e.preparedPublicationPreflightLocked(op, &preparedPublication{entry: entry})
		}
		if err == nil {
			delete(e.prepared, gid)
			e.endSerializableLocked(entry.ref, false)
		}
		op.session.mu.Unlock()
		e.mu.Unlock()
	}
	if err != nil {
		e.mu.Lock()
		entry.claimed = false
		e.mu.Unlock()
		return err
	}
	e.locks.end(entry.ticket)
	return nil
}
