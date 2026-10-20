package engine

import (
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"time"
)

// The registry owns these roots and the original lock/SSI identity after detach.
// No session attachment is consulted to authorize completion.
type preparedTransaction struct {
	gid      string
	owner    authorization.PrincipalRef
	ref      ids.TxRef
	prepared time.Time
	database string
	ticket   *lock.Ticket
	writes   map[ids.ObjectAddress][]storage.RowVersion
	deletes  map[ids.ObjectAddress]*visibility.PrivateDeletes
	catalog  *session.Journal
	claimed  bool
	roots    map[ids.ObjectAddress]visibility.Root
	frontier ids.CommitSeq
	options  session.TxOptions
}

type preparedPublication struct {
	entry     *preparedTransaction
	preparing bool
	origin    *session.TransactionOwner
}

func (e *Engine) preparedPublicationPreflightLocked(op *session.Operation, p *preparedPublication) error {
	if p == nil {
		return nil
	}
	if p.preparing {
		if p.origin.Ref != p.entry.ref || !p.origin.InBlock() {
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
func (e *Engine) preparedActorLocked(op *session.Operation, entry *preparedTransaction) error {
	roles := e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published)
	current := op.Session.Authorization.State.CurrentUser
	if err := roles.ValidatePrincipal(current); err != nil {
		return err
	}
	actor, exists := roles.Find(current)
	if current != entry.owner && (!exists || !actor.Superuser) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to finish prepared transaction", Hint: "Must be superuser or the user that prepared the transaction."}
	}
	return nil
}

// Called at the canonical publisher's callback-free final boundary.
func (e *Engine) installPreparedLocked(p *preparedPublication) {
	e.visibility.SerializablePreparedLocked(p.entry.ref)
	e.visibility.DropSnapshotLocked(p.entry.ref)
	e.prepared[p.entry.gid] = p.entry
	p.origin.Ticket = nil
	clearTransaction(p.origin)
	p.origin.Session.RetireTransactionTimeoutLocked()
}

func (e *Engine) finishPreparedLocked(p *preparedPublication) {
	if p == nil {
		return
	}
	delete(e.prepared, p.entry.gid)
	e.endSerializableLocked(p.entry.ref, true)
}

func (s *StatementScope) executePrepared(control *parser.TransactionControl, state session.TxState, ref ids.TxRef) error {
	e, t := s.engine(), s.owner
	if control.Kind != parser.KindPrepareTransaction {
		if state != session.TxIdle {
			word := "COMMIT"
			if control.Kind == parser.KindRollbackPrepared {
				word = "ROLLBACK"
			}
			return sqlError("25001", word+" PREPARED cannot run inside a transaction block")
		}
		return e.completePrepared(s.op, control.GID, control.Kind == parser.KindCommitPrepared)
	}
	if state == session.TxIdle {
		s.op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "there is no transaction in progress"})
		return nil
	}
	if state == session.TxImplicit {
		s.op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "there is no transaction in progress"})
	}
	// Every admitted PREPARE, even failure, ends the whole incarnation.
	s.controlOutcome = controlExecuted
	if state == session.TxAborted {
		_, err := e.endAdmitted(s.op, t, ref, false, false, true, s.statementStart, s)
		return err
	}
	t.Mu.Lock()
	var err error
	if payloadOf(t.Session).cursors.hasCurrentHold(ref) {
		err = sqlError("0A000", "cannot PREPARE a transaction that has created a cursor WITH HOLD")
	}
	if err == nil && len(control.GID) >= 200 {
		err = sqlError("22023", `transaction identifier "`+control.GID+`" is too long`)
	}
	if err == nil && t.AccessedTemporary {
		err = sqlError("0A000", "cannot PREPARE a transaction that has operated on temporary objects")
	}
	if err == nil && len(ownerExtensionOrZero(t).foreign.participants) != 0 {
		err = sqlError("0A000", "cannot PREPARE a transaction that has operated on foreign tables")
	}
	if err == nil && (len(t.NotificationActions) != 0 || len(t.NotificationEvents) != 0) {
		err = sqlError("0A000", "cannot PREPARE a transaction that has executed LISTEN, UNLISTEN, or NOTIFY")
	}
	var entry *preparedTransaction
	if err == nil {
		auth, authErr := operationAuthorization(s.op)
		err = authErr
		if err == nil {
			entry = &preparedTransaction{gid: control.GID, owner: auth.State.CurrentUser, ref: ref, prepared: time.Now().UTC().Truncate(time.Microsecond), database: s.op.Session.DatabaseName, ticket: t.Ticket, writes: t.Writes, deletes: t.Deletes, catalog: t.Catalog, roots: t.Roots, frontier: t.Frontier, options: session.TxOptions{Isolation: t.Isolation, ReadOnly: t.ReadOnly, Deferrable: t.Deferrable}}
			e.mu.Lock()
			if e.prepared == nil {
				e.prepared = make(map[string]*preparedTransaction)
			}
			e.mu.Unlock()
			s.attachTransactionLocked()
			t.Mu.Unlock()
			err = e.locks.CheckPreparedAdvisory(entry.ticket)
			if err == nil {
				err = e.checkTransactionExclusions(s)
			}
			if err == nil {
				err = e.publishRowsPrepared(s.op, ref, t.Writes, t.Deletes, t.Catalog, nil, &preparedPublication{entry: entry, preparing: true, origin: t}, s)
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
	e.locks.DetachPrepared(entry.ticket)
	return nil
}

func (e *Engine) completePrepared(op *session.Operation, gid string, commit bool) error {
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	entry := e.prepared[gid]
	if err == nil && entry == nil {
		err = sqlError("42704", `prepared transaction with identifier "`+gid+`" does not exist`)
	}
	if err == nil {
		actor, exists := auth.Roles.Find(auth.State.CurrentUser)
		if auth.State.CurrentUser != entry.owner && (!exists || !actor.Superuser) {
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
		err = e.publishRowsPrepared(op, entry.ref, entry.writes, entry.deletes, entry.catalog, nil, &preparedPublication{entry: entry}, nil)
	} else {
		e.mu.Lock()
		op.Session.Mu.Lock()
		err = publicationOperationLocked(e, op)
		if err == nil {
			err = e.preparedPublicationPreflightLocked(op, &preparedPublication{entry: entry})
		}
		if err == nil {
			delete(e.prepared, gid)
			e.endSerializableLocked(entry.ref, false)
		}
		op.Session.Mu.Unlock()
		e.mu.Unlock()
	}
	if err != nil {
		e.mu.Lock()
		entry.claimed = false
		e.mu.Unlock()
		return err
	}
	e.endLockOwner(entry.ticket)
	return nil
}
