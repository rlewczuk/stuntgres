package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"sync"
	"time"
)

type EndReason uint8

const (
	CleanClose EndReason = iota
	InitFailure
	InvalidConnection
	Terminated
	Abandoned
)

type ResourceKind uint8

const (
	Settings ResourceKind = iota
	Temporary
	PreparedSQL
	Cursors
	Authorization
	Transaction
	Notifications
	AdvisorySession
	resourceKinds
)

type ResourceHandle struct {
	session ids.SessionToken
	kind    ResourceKind
	serial  uint64
}

// SessionResource callbacks release in-process resources outside lifecycle locks.
// They must not synchronously reenter lifecycle operations or perform external I/O.
type SessionResource interface {
	EndSession(ids.SessionToken, EndReason) error
}
type SessionStart struct {
	ID            ids.SessionID
	Defaults      SessionDefaultProvider
	Authenticated authorization.PrincipalRef
}
type sessionState uint8

const (
	initializing sessionState = iota
	live
	ending
	ended
)

type resourceEntry struct {
	handle ResourceHandle
	owner  SessionResource
}
type Session struct {
	ids.SessionOwnerMarker
	mu     sync.Mutex
	cond   *sync.Cond
	engine *Engine // Immutable after construction.
	token  ids.SessionToken
	// Written before registry publication, never changed by identity switches.
	countedAuthenticated   authorization.PrincipalRef
	state                  sessionState
	completed              chan struct{}
	endRequested           chan struct{} // Lazy wake projection of ending, not a lease or completion.
	leases                 int
	operations             map[*Operation]struct{}
	detaching              bool
	serial                 uint64
	resources              [resourceKinds]*resourceEntry
	removed                map[ResourceHandle]bool
	reason                 EndReason
	cleanupErrors          []error
	result                 error
	settings               *sessionSettings
	transactions           *transactionOwner
	notifications          sessionNotifications
	advisory               sessionAdvisory
	cursors                *cursorRegistry
	preparedSQL            *sqlPreparedRegistry
	authorization          *sessionAuthorization
	sequences              map[ids.ObjectAddress]*sequenceMemory
	lastSequence           ids.ObjectAddress
	discardPlansGeneration uint64 // serialized by physical-owner statement admission
	temporaryNamespace     ids.ObjectAddress
	sequencePath           sequencePathCache
	random                 randomState
	arrayRandom            randomState // independent of random()/setseed()
	uuid                   uuidClock
	databaseName           string
	timeouts               sessionTimeouts
}

// Operation is pointer-owned; copying it is invalid. Done releases its lease once.
type Operation struct {
	self                       *Operation
	session                    *Session
	ctx                        context.Context
	copyInput                  protocolCopyInputContext
	commandContext             context.Context // Session-cancelable parent of per-statement budgets.
	cancel                     context.CancelCauseFunc
	done                       bool
	private                    bool
	notices                    []sqlstate.Error
	noticeThreshold            noticeLevel
	timerCancel                context.CancelFunc
	lockTimeout                time.Duration
	parallelWorkers            int // Immutable prior-setting snapshot at command admission.
	statementDeadline          time.Time
	commandStart               time.Time
	sourceContext              context.Context
	commandAdmission           bool
	sqlActivity                bool
	sqlFinished                bool // Session.mu: ERROR/completion fence, not transaction state
	timeoutScope               *StatementScope
	definition                 definitionReader // Scoped once-only DDL cooking, restored before evaluation.
	lockWait                   lock.TimeoutSlot // queued lock request's SQL-deadline projection
	nonstandardStrings         bool
	restoreTableDestination    bool
	restoreInsertAuthority     bool
	restoreCommentAuthority    bool
	materializedPopulation     bool                       // Operation-local execution path, never session settings.
	materializedOwner          authorization.PrincipalRef // Population-only effective principal; session_user is unchanged.
	restoreDefaultReplay       bool
	restorePartitionAttach     bool
	restorePartitionCheck      *catalog.Relation          // Exact leased private-import physical CHECK target.
	restorePartitionUsingIndex *partitionUsingIndexReplay // Exact leased private-import physical target/index.
	restorePartitionUnique     *partitionUniqueReplay     // Exact private virtual definition/subtree lease.
	discardRoleReset           authorization.RoleState    // nonzero Authenticated only during DISCARD ALL publication
}

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
	s := &Session{engine: e, token: e.catalog.Domain.Session(start.ID), countedAuthenticated: authenticated, state: initializing, completed: make(chan struct{}), leases: 1, removed: make(map[ResourceHandle]bool)}
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
		err = owner.initialize(op.Context(), e.settingDefinitions(), e.settingsOrder, start.Defaults)
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
		transactions := &transactionOwner{session: s}
		_, err = op.Attach(Transaction, transactions)
		if err == nil {
			s.transactions = transactions
		}
	}
	if err == nil {
		s.notifications.engine, s.notifications.token = e, s.token
		_, err = op.Attach(Notifications, &s.notifications)
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
			s.cursors = cursors
		}
	}
	if err == nil {
		prepared := &sqlPreparedRegistry{entries: make(map[string]*sqlPreparedStatement), token: s.token}
		_, err = op.Attach(PreparedSQL, prepared)
		if err == nil {
			s.preparedSQL = prepared
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
func (s *Session) Engine() *Engine         { return s.engine }
func (s *Session) Token() ids.SessionToken { return s.token }
func (s *Session) operation(ctx context.Context, private bool) *Operation {
	// Caller cancellation propagates through this child context. RequestEnd
	// cancels every admitted child under the same admission lock; Done removes it.
	combined, cancel := context.WithCancelCause(ctx)
	op := &Operation{session: s, ctx: combined, commandContext: combined, sourceContext: ctx, cancel: cancel, private: private}
	op.self = op
	if s.operations == nil {
		s.operations = make(map[*Operation]struct{})
	}
	s.operations[op] = struct{}{}
	return op
}
func (s *Session) Acquire(ctx context.Context) (*Operation, error) {
	return s.acquire(ctx, false)
}
func (s *Session) acquire(ctx context.Context, activity bool) (*Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != live || s.detaching {
		if s.timeouts.fatal != nil {
			copy := *s.timeouts.fatal
			return nil, &copy
		}
		return nil, errors.New("stuntgres: session unavailable")
	}
	s.leases++
	op := s.operation(ctx, false)
	op.sqlActivity = activity
	if activity {
		s.timeouts.sqlLeases++
		s.timeouts.idle.stop()
	}
	return op, nil
}
func (op *Operation) Context() context.Context {
	if op.copyInput.held() {
		op.session.mu.Lock()
		ctx := op.copyInput.effectiveLocked(op)
		op.session.mu.Unlock()
		return ctx
	}
	return op.ctx
}
func (op *Operation) Done() {
	if op == nil || op.self != op || op.session == nil {
		return
	}
	s := op.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.done {
		return
	}
	op.done = true
	op.lockWait.Reset()
	delete(s.operations, op)
	op.cancel(nil)
	if op.timerCancel != nil {
		op.timerCancel()
	}
	s.leases--
	if op.sqlActivity {
		s.timeouts.sqlLeases--
	}
	if op.sqlActivity || op.private {
		s.readyTimeoutLocked(time.Now())
	}
	s.cond.Broadcast()
}
func (e *Engine) validateOperation(op *Operation) error {
	if op == nil || op.self != op || op.session == nil || op.session.token.Domain() != domainOf(e) {
		return errors.New("stuntgres: foreign or missing operation")
	}
	s := op.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.done || s.state != live {
		if s.timeouts.fatal != nil {
			copy := *s.timeouts.fatal
			return &copy
		}
		return errors.New("stuntgres: inactive operation")
	}
	if op.copyInput.held() {
		return op.fatalErrorLocked(op.copyInput.effectiveLocked(op).Err())
	}
	return op.fatalErrorLocked(op.ctx.Err())
}
func nilResource(owner SessionResource) bool {
	if owner == nil {
		return true
	}
	v := reflect.ValueOf(owner)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
func (op *Operation) Attach(kind ResourceKind, owner SessionResource) (ResourceHandle, error) {
	if op == nil || op.self != op || op.session == nil || kind >= resourceKinds || nilResource(owner) {
		return ResourceHandle{}, errors.New("stuntgres: invalid session attachment")
	}
	s := op.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := op.ctx.Err(); err != nil {
		return ResourceHandle{}, err
	}
	if op.done || (s.state != live && !(op.private && s.state == initializing)) || s.detaching {
		return ResourceHandle{}, errors.New("stuntgres: inactive attachment lease")
	}
	if s.resources[kind] != nil {
		return ResourceHandle{}, errors.New("stuntgres: duplicate session attachment")
	}
	if s.serial == ^uint64(0) {
		return ResourceHandle{}, errors.New("stuntgres: resource identity exhausted")
	}
	s.serial++
	h := ResourceHandle{s.token, kind, s.serial}
	s.resources[kind] = &resourceEntry{h, owner}
	return h, nil
}
func (s *Session) Detach(h ResourceHandle, reason EndReason) error {
	s.mu.Lock()
	if h.session != s.token || h.kind >= resourceKinds || h.serial == 0 {
		s.mu.Unlock()
		return errors.New("stuntgres: foreign resource handle")
	}
	if s.removed[h] {
		s.mu.Unlock()
		return nil
	}
	entry := s.resources[h.kind]
	if entry == nil || entry.handle != h {
		s.mu.Unlock()
		return errors.New("stuntgres: stale resource handle")
	}
	if s.state != live || s.leases != 0 || s.detaching {
		s.mu.Unlock()
		return errors.New("stuntgres: session detach requires quiescence")
	}
	s.detaching = true
	s.resources[h.kind] = nil
	s.mu.Unlock()
	err := entry.owner.EndSession(s.token, reason)
	s.mu.Lock()
	s.removed[h] = true
	if err != nil {
		s.cleanupErrors = append(s.cleanupErrors, err)
	}
	s.detaching = false
	s.cond.Broadcast()
	s.mu.Unlock()
	return err
}
func (s *Session) RequestEnd(reason EndReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestEndLocked(reason)
}

// EndRequested wakes observers as soon as ending is requested, before leases
// drain. Check reports the authoritative reason; WaitEnd still joins cleanup.
func (s *Session) EndRequested() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.endRequested == nil {
		s.endRequested = make(chan struct{})
		if s.state == ending || s.state == ended {
			close(s.endRequested)
		}
	}
	return s.endRequested
}

func (s *Session) requestEndLocked(reason EndReason) {
	if s.state == ending || s.state == ended {
		return
	}
	s.state = ending
	if s.endRequested != nil {
		close(s.endRequested)
	}
	s.reason = reason
	s.timeouts.transaction.stop()
	s.timeouts.idle.stop()
	var cause error
	if s.timeouts.fatal != nil {
		cause = s.timeouts.fatal
	}
	for op := range s.operations {
		op.cancel(cause)
		if op.copyInput.cancel != nil {
			op.copyInput.cancel(cause)
		}
	}
	go s.end()
}
func (s *Session) end() {
	s.mu.Lock()
	for s.leases != 0 || s.detaching {
		s.cond.Wait()
	}
	for _, kind := range [...]ResourceKind{Transaction, Notifications, Cursors, PreparedSQL, Temporary, AdvisorySession, Settings, Authorization} {
		entry := s.resources[kind]
		if entry == nil {
			continue
		}
		s.resources[kind] = nil
		s.mu.Unlock()
		err := entry.owner.EndSession(s.token, s.reason)
		if kind == Transaction {
			// The transaction callback has returned and released its admission
			// lock. Maintenance never runs inside a SessionResource callback.
			err = errors.Join(err, s.engine.endTemporaryCollations(s))
		}
		s.mu.Lock()
		s.removed[entry.handle] = true
		if err != nil {
			s.cleanupErrors = append(s.cleanupErrors, err)
		}
	}
	s.result = errors.Join(s.cleanupErrors...)
	s.engine.sessionMu.Lock()
	if s.engine.sessions[s.token] == s {
		delete(s.engine.sessions, s.token)
	}
	s.engine.sessionMu.Unlock()
	s.state = ended
	close(s.completed)
	s.mu.Unlock()
}
func (s *Session) WaitEnd(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.completed:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.result
	}
}
func (s *Session) Reuse(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != live {
		return fmt.Errorf("stuntgres: session is not reusable")
	}
	return nil
}
func (s *Session) IsLive() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.state == live }
