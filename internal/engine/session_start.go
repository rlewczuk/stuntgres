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
	allocation := &sessionAllocation{session: Session{host: (*sessionHost)(e), token: e.catalog.Domain.Session(start.ID), countedAuthenticated: authenticated, state: initializing, completed: make(chan struct{}), leases: 1, removed: make(map[ResourceHandle]bool)}}
	s, payload := &allocation.session, &allocation.payload
	s.payload = payload
	if private {
		s.token = e.catalog.Domain.PrivateSession(s)
	}
	s.databaseName = e.databaseName
	s.cond = sync.NewCond(&s.mu)
	op := s.operation(ctx, true)
	auth := &sessionAuthorization{session: s, state: authorization.RoleState{Authenticated: authenticated, SessionUser: authenticated, CurrentUser: authenticated}, revision: 1}
	_, authErr = op.Attach(Authorization, auth)
	if authErr == nil {
		s.authorization = auth
	}
	owner := &sessionSettings{session: s, values: make(map[string]string), defaults: make(map[string]string)}
	_, err := op.Attach(Settings, owner)
	if err == nil {
		s.settings = owner
		err = owner.initialize(op.Context(), e.settings.Definitions(), e.settings.Order(), start.Defaults)
		if err == nil {
			owner.mu.Lock()
			s.mu.Lock()
			s.publishTimeoutSettingsLocked(owner, false, time.Now())
			s.mu.Unlock()
			owner.mu.Unlock()
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
		transactions := &transactionOwner{session: s, extension: &allocation.owner}
		_, err = op.Attach(Transaction, (*transactionSessionEnd)(transactions))
		if err == nil {
			s.transactions = transactions
		}
	}
	if err == nil {
		payload.notifications.engine, payload.notifications.token = e, s.token
		_, err = op.Attach(Notifications, &payload.notifications)
	}
	if err == nil {
		var ticket *lock.Ticket
		if sessionTokenAttached(s.token) {
			ticket, err = e.locks.RegisterSession(s.token)
		} else {
			err = errors.New("stuntgres: invalid advisory session")
		}
		if err == nil {
			s.advisory.ticket, s.advisory.locks = ticket, e.locks
			_, err = op.Attach(AdvisorySession, &s.advisory)
			if err != nil {
				e.endLockOwner(ticket)
				s.advisory.ticket = nil
			}
		}
	}
	if err == nil {
		cursors := &cursorRegistry{entries: make(map[string]*sqlCursor), token: s.token}
		_, err = op.Attach(Cursors, cursors)
		if err == nil {
			payload.cursors = cursors
		}
	}
	if err == nil {
		prepared := &sqlPreparedRegistry{entries: make(map[string]*sqlPreparedStatement), token: s.token}
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
	s.mu.Lock()
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && s.state != initializing {
		err = errors.New("stuntgres: session initialization ended")
	}
	if err == nil {
		e.sessionMu.Lock()
		if e.sessions == nil {
			e.sessions = make(map[ids.SessionToken]*Session)
		}
		if e.sessions[s.token] != nil {
			err = errors.New("stuntgres: duplicate session identity")
		} else if !role.Superuser && role.ConnectionLimit >= 0 && e.roleSessionCountLocked(authenticated) >= int(role.ConnectionLimit) {
			err = roleConnectionLimitError(role.Name)
		} else if err = ctx.Err(); err == nil {
			if resetRole != (authorization.PrincipalRef{}) {
				auth.state.CurrentUser, auth.state.ResetRole = resetRole, resetRole
			}
			s.state = live
			e.sessions[s.token] = s
		}
		e.sessionMu.Unlock()
	}
	s.mu.Unlock()
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
