package engine

import (
	"context"
	"errors"
	"sync"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func (e *Engine) StartSession(ctx context.Context, start SessionStart) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if start.ID == 0 {
		return nil, errors.New("stuntgres: zero session identity")
	}
	return e.startSession(ctx, start, false)
}

// Private maintenance sessions have genuine resources and leases, but a separate
// pointer identity domain that cannot collide with the driver's public IDs.
func (e *Engine) startSession(ctx context.Context, start SessionStart, private bool) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.databaseNameError != nil {
		return nil, e.databaseNameError
	}
	if start.ID == 0 && !private {
		return nil, errors.New("stuntgres: zero session identity")
	}
	e.mu.Lock()
	authenticated := start.Authenticated
	if authenticated == (authorization.PrincipalRef{}) {
		authenticated = e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	}
	role, authErr := (e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published)).Descriptor(authenticated)
	e.mu.Unlock()
	if authErr != nil {
		return nil, authErr
	}
	if !role.Login {
		return nil, roleLoginError(role.Name)
	}
	// One allocation holds the session, its root payload and the owner extension.
	allocation := &sessionAllocation{session: Session{Host: (*sessionHost)(e), SessionToken: e.catalog.Domain.Session(start.ID), CountedAuthenticated: authenticated, State: initializing, Completed: make(chan struct{}), Leases: 1, Removed: make(map[ResourceHandle]bool)}}
	s, payload := &allocation.session, &allocation.payload
	s.Payload = payload
	if private {
		s.SessionToken = e.catalog.Domain.PrivateSession(s)
	}
	s.DatabaseName = e.databaseName
	s.Cond = sync.NewCond(&s.Mu)
	op := s.Operation(ctx, true)
	auth := &sessionAuthorization{Session: s, State: authorization.RoleState{Authenticated: authenticated, SessionUser: authenticated, CurrentUser: authenticated}, Revision: 1}
	_, authErr = op.Attach(Authorization, auth)
	if authErr == nil {
		s.Authorization = auth
	}
	owner := &sessionSettings{Session: s, Values: make(map[string]string), Defaults: make(map[string]string)}
	_, err := op.Attach(Settings, owner)
	if err == nil {
		s.Settings = owner
		err = owner.Initialize(op.Context(), e.settings.Definitions(), e.settings.Order(), start.Defaults)
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
	if err == nil {
		resetRole, err = e.initialRole(op.Context(), authenticated, start.Defaults)
	}
	if err == nil {
		transactions := &transactionOwner{Session: s, Extension: &allocation.owner}
		_, err = op.Attach(Transaction, (*transactionSessionEnd)(transactions))
		if err == nil {
			s.Transactions = transactions
		}
	}
	if err == nil {
		payload.notifications.engine, payload.notifications.token = e, s.SessionToken
		_, err = op.Attach(Notifications, &payload.notifications)
	}
	if err == nil {
		var ticket *lock.Ticket
		if sessionTokenAttached(s.SessionToken) {
			ticket, err = e.locks.RegisterSession(s.SessionToken)
		} else {
			err = errors.New("stuntgres: invalid advisory session")
		}
		if err == nil {
			s.Advisory.Ticket, s.Advisory.Locks = ticket, e.locks
			_, err = op.Attach(AdvisorySession, &s.Advisory)
			if err != nil {
				e.endLockOwner(ticket)
				s.Advisory.Ticket = nil
			}
		}
	}
	if err == nil {
		cursors := &cursorRegistry{entries: make(map[string]*sqlCursor), token: s.SessionToken}
		_, err = op.Attach(Cursors, cursors)
		if err == nil {
			payload.cursors = cursors
		}
	}
	if err == nil {
		prepared := &sqlPreparedRegistry{entries: make(map[string]*sqlPreparedStatement), token: s.SessionToken}
		_, err = op.Attach(PreparedSQL, prepared)
		if err == nil {
			payload.preparedSQL = prepared
		}
	}
	e.mu.Lock()
	if err == nil {
		role, err = (e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published)).Descriptor(authenticated)
		if err == nil && !role.Login {
			err = roleLoginError(role.Name)
		}
		if err == nil && resetRole != (authorization.PrincipalRef{}) {
			err = authorization.ValidateInitialRole(e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published), authenticated, resetRole)
		}
		if err == nil && !private {
			err = e.checkDatabaseConnectLocked(ctx, authenticated)
		}
	}
	s.Mu.Lock()
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && s.State != initializing {
		err = errors.New("stuntgres: session initialization ended")
	}
	if err == nil {
		e.sessionMu.Lock()
		if e.sessions == nil {
			e.sessions = make(map[ids.SessionToken]*Session)
		}
		if e.sessions[s.SessionToken] != nil {
			err = errors.New("stuntgres: duplicate session identity")
		} else if !role.Superuser && role.ConnectionLimit >= 0 && e.roleSessionCountLocked(authenticated) >= int(role.ConnectionLimit) {
			err = roleConnectionLimitError(role.Name)
		} else if err = ctx.Err(); err == nil {
			if resetRole != (authorization.PrincipalRef{}) {
				auth.State.CurrentUser, auth.State.ResetRole = resetRole, resetRole
			}
			s.State = live
			e.sessions[s.SessionToken] = s
		}
		e.sessionMu.Unlock()
	}
	s.Mu.Unlock()
	e.mu.Unlock()
	if err != nil {
		s.RequestEnd(InitFailure)
	}
	op.Done()
	if err != nil {
		return nil, errors.Join(err, s.WaitEnd(context.Background()))
	}
	return s, nil
}

func (e *Engine) validateOperation(op *Operation) error {
	return validateSessionOperation(domainOf(e), op)
}
