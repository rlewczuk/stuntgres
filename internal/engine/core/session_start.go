package core

import (
	"context"
	"errors"
	"sync"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func (e *Engine) StartSession(ctx context.Context, admission *DatabaseAdmission, start session.SessionStart) (*session.Session, error) {
	if err := admission.claim(e); err != nil {
		return nil, err
	}
	return e.startSession(ctx, start, admission.principal, admission, false)
}

// StartRestoreSession initializes the private replay owner. It has real cleanup
// but no public physical database reservation or persistent session identity.
func (e *Engine) StartRestoreSession(ctx context.Context, principal authorization.PrincipalRef) (*session.Session, error) {
	return e.startSession(ctx, session.SessionStart{}, principal, nil, true)
}

// Private maintenance sessions have genuine resources and leases, but a separate
// pointer identity domain that cannot collide with the driver's public IDs.
func (e *Engine) startSession(ctx context.Context, start session.SessionStart, authenticated authorization.PrincipalRef, admission *DatabaseAdmission, private bool) (*session.Session, error) {
	// Until the Session takes ownership, every early return releases the claimed
	// reservation. Afterwards only terminal Unregister may release it.
	owned := admission != nil
	defer func() {
		if owned {
			admission.release(true)
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.DatabaseNameError != nil {
		return nil, e.DatabaseNameError
	}
	if start.ID == 0 && !private {
		return nil, errors.New("stuntgres: zero session identity")
	}
	var startRoles authorization.Read
	var roleDescriptor authorization.RoleDescriptor
	var authErr error
	var captured map[string]session.CapturedDefault
	if admission != nil {
		startRoles = admission.roles
		roleDescriptor, authErr = startRoles.Descriptor(authenticated)
		captured = admission.defaults()
	} else {
		e.Mu.Lock()
		startRoles = e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current())
		roleDescriptor, authErr = startRoles.Descriptor(authenticated)
		e.Mu.Unlock()
	}
	if authErr != nil {
		return nil, authErr
	}
	if !roleDescriptor.Login {
		return nil, role.LoginError(roleDescriptor.Name)
	}
	// One allocation holds the session, its root payload and the owner extension.
	allocation := &sessionAllocation{session: session.Session{Host: (*sessionHost)(e), SessionToken: e.Catalog.Domain.Session(start.ID), CountedAuthenticated: authenticated, State: session.Initializing, Completed: make(chan struct{}), Leases: 1, Removed: make(map[session.ResourceHandle]bool)}}
	s, payload := &allocation.session, &allocation.payload
	s.Payload = payload
	payload.admission = admission
	owned = false
	if private {
		s.SessionToken = e.Catalog.Domain.PrivateSession(s)
	}
	s.DatabaseName = e.DatabaseName
	s.Cond = sync.NewCond(&s.Mu)
	op := s.Operation(ctx, true)
	auth := &session.AuthorizationState{Session: s, State: authorization.RoleState{Authenticated: authenticated, SessionUser: authenticated, CurrentUser: authenticated}, Revision: 1}
	_, authErr = op.Attach(session.Authorization, auth)
	if authErr == nil {
		s.Authorization = auth
	}
	owner := &session.SettingsStore{Session: s, Values: make(map[string]string)}
	_, err := op.Attach(session.Settings, owner)
	if err == nil {
		s.Settings = owner
		if start.Defaults == nil {
			// Provider-less sessions share their registry's defaults.
			if err = op.Context().Err(); err == nil {
				owner.InitializeShared(e.Settings.SessionDefaults())
			}
		} else {
			err = owner.Initialize(op.Context(), e.Settings.Definitions(), e.Settings.Order(), start.Defaults)
		}
		if err == nil {
			err = owner.CapturePersistedDefaults(op.Context(), e.Settings, captured)
		}
		if err == nil {
			owner.Mu.Lock()
			s.Mu.Lock()
			s.PublishTimeoutSettingsLocked(owner, false, time.Now())
			s.Mu.Unlock()
			owner.Mu.Unlock()
		}
	}
	if err == nil {
		err = authErr
	}
	var resetRole authorization.PrincipalRef
	var resetRoleText string
	if err == nil {
		op.SnapshotNoticeThreshold()
		resetRole, resetRoleText, err = e.initialRole(op, authenticated, start.Defaults, startRoles, captured["role"])
	}
	if err == nil {
		transactions := &session.TransactionOwner{Session: s, Extension: &allocation.owner}
		_, err = op.Attach(session.Transaction, (*transactionSessionEnd)(transactions))
		if err == nil {
			s.Transactions = transactions
		}
	}
	if err == nil {
		payload.notifications.Session = s
		_, err = op.Attach(session.Notifications, (*notificationSessionEnd)(&payload.notifications))
	}
	if err == nil {
		var ticket *lock.Ticket
		if session.TokenAttached(s.SessionToken) {
			ticket, err = e.Locks.RegisterSession(s.SessionToken)
		} else {
			err = errors.New("stuntgres: invalid advisory session")
		}
		if err == nil {
			s.Advisory.Ticket, s.Advisory.Locks = ticket, e.Locks
			_, err = op.Attach(session.AdvisorySession, &s.Advisory)
			if err != nil {
				e.EndLockOwner(ticket)
				s.Advisory.Ticket = nil
			}
		}
	}
	if err == nil {
		cursors := &cursorRegistry{Entries: make(map[string]*sqlCursor), token: s.SessionToken}
		_, err = op.Attach(session.Cursors, cursors)
		if err == nil {
			payload.Cursors = cursors
		}
	}
	if err == nil {
		prepared := NewPreparedRegistry(s.SessionToken)
		_, err = op.Attach(session.PreparedSQL, prepared)
		if err == nil {
			payload.PreparedSQL = prepared
		}
	}
	e.Mu.Lock()
	if err == nil {
		roleDescriptor, err = e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()).Descriptor(authenticated)
		if err == nil && !roleDescriptor.Login {
			err = role.LoginError(roleDescriptor.Name)
		}
		if err == nil && resetRole != (authorization.PrincipalRef{}) {
			err = authorization.ValidateInitialRole(e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()), authenticated, resetRole)
		}
	}
	if err == nil {
		owner.Mu.Lock()
		id := owner.TypedValue("client_encoding").ClientEncoding
		owner.Mu.Unlock()
		var policy sqltype.ClientEncodingPolicy
		policy, err = e.prepareClientEncodingLocked(op, id)
		if err == nil {
			owner.Mu.Lock()
			owner.AcceptClientEncodingLocked(policy)
			owner.Mu.Unlock()
		}
	}
	s.Mu.Lock()
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && s.State != session.Initializing {
		err = errors.New("stuntgres: session initialization ended")
	}
	if err == nil {
		e.sessionMu.Lock()
		if e.sessions == nil {
			e.sessions = make(map[ids.SessionToken]*session.Session)
		}
		if e.sessions[s.SessionToken] != nil {
			err = errors.New("stuntgres: duplicate session identity")
		} else if !roleDescriptor.Superuser && roleDescriptor.ConnectionLimit >= 0 && e.roleSessionCountLocked(authenticated) >= int(roleDescriptor.ConnectionLimit) {
			err = role.ConnectionLimitError(roleDescriptor.Name)
		} else if err = ctx.Err(); err == nil {
			if resetRole != (authorization.PrincipalRef{}) {
				auth.State.CurrentUser, auth.State.ResetRole = resetRole, resetRole
				auth.State.ActiveRole = true
				auth.State.RoleText, auth.State.ResetRoleText = resetRoleText, resetRoleText
			}
			s.State = session.Live
			e.sessions[s.SessionToken] = s
		}
		e.sessionMu.Unlock()
	}
	s.Mu.Unlock()
	e.Mu.Unlock()
	if err != nil {
		s.RequestEnd(session.InitFailure)
	}
	op.Done()
	if err != nil {
		return nil, errors.Join(err, s.WaitEnd(context.Background()))
	}
	if start.NoticeHandler != nil {
		for _, notice := range op.DetachNotices() {
			start.NoticeHandler(notice)
		}
	}
	return s, nil
}

func (e *Engine) validateOperation(op *session.Operation) error {
	return session.ValidateOperation(DomainOf(e), op)
}
