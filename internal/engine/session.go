package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"sync"
	"time"
)

type SessionID uint64
type SessionToken struct {
	owner *Engine
	id    SessionID
}

func (t SessionToken) ID() SessionID { return t.id }

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
	AdvisorySession
	resourceKinds
)

type ResourceHandle struct {
	session SessionToken
	kind    ResourceKind
	serial  uint64
}

// SessionResource callbacks release in-process resources outside lifecycle locks.
// They must not synchronously reenter lifecycle operations or perform external I/O.
type SessionResource interface {
	EndSession(SessionToken, EndReason) error
}
type SessionStart struct {
	ID            SessionID
	Defaults      SessionDefaultProvider
	Authenticated PrincipalRef
	DatabaseName  string
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
	mu    sync.Mutex
	cond  *sync.Cond
	token SessionToken
	// Written before registry publication, never changed by identity switches.
	countedAuthenticated PrincipalRef
	state                sessionState
	completed            chan struct{}
	leases               int
	operations           map[*Operation]struct{}
	detaching            bool
	serial               uint64
	resources            [resourceKinds]*resourceEntry
	removed              map[ResourceHandle]bool
	reason               EndReason
	cleanupErrors        []error
	result               error
	settings             *sessionSettings
	transactions         *transactionOwner
	advisory             sessionAdvisory
	cursors              *cursorRegistry
	authorization        *sessionAuthorization
	sequences            map[ObjectAddress]*sequenceMemory
	lastSequence         ObjectAddress
	temporaryNamespace   ObjectAddress
	sequencePath         sequencePathCache
	random               randomState
	uuid                 uuidClock
	databaseName         string
}

// Operation is pointer-owned; copying it is invalid. Done releases its lease once.
type Operation struct {
	self                    *Operation
	session                 *Session
	ctx                     context.Context
	cancel                  context.CancelFunc
	done                    bool
	private                 bool
	notices                 []sqlstate.Error
	noticeThreshold         noticeLevel
	timerCancel             context.CancelFunc
	lockTimeout             time.Duration
	statementDeadline       time.Time
	commandStart            time.Time
	sourceContext           context.Context
	commandAdmission        bool
	nonstandardStrings      bool
	restoreTableDestination bool
	restoreInsertAuthority  bool
	restoreCommentAuthority bool
}

func (e *Engine) StartSession(ctx context.Context, start SessionStart) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if start.ID == 0 {
		return nil, errors.New("stuntgres: zero session identity")
	}
	e.mu.Lock()
	authenticated := start.Authenticated
	if authenticated == (PrincipalRef{}) {
		authenticated = e.roles.names["postgres"]
	}
	role, authErr := (RoleRead{e, e.roles}).Descriptor(authenticated)
	nameType, _ := (CatalogRead{e, e.state}).LookupType(sqltype.Name)
	database, nameErr := nameType.Input(ctx, start.DatabaseName, -1)
	e.mu.Unlock()
	if authErr != nil {
		return nil, authErr
	}
	if nameErr != nil {
		return nil, nameErr
	}
	if !role.Login {
		return nil, roleLoginError(role.Name)
	}
	s := &Session{token: SessionToken{e, start.ID}, countedAuthenticated: authenticated, state: initializing, completed: make(chan struct{}), leases: 1, removed: make(map[ResourceHandle]bool)}
	s.databaseName, _ = database.Text()
	s.cond = sync.NewCond(&s.mu)
	op := s.operation(ctx, true)
	auth := &sessionAuthorization{session: s, state: RoleState{Authenticated: authenticated, SessionUser: authenticated, CurrentUser: authenticated}, revision: 1}
	_, authErr = op.Attach(Authorization, auth)
	if authErr == nil {
		s.authorization = auth
	}
	owner := &sessionSettings{values: make(map[string]string), defaults: make(map[string]string)}
	_, err := op.Attach(Settings, owner)
	if err == nil {
		s.settings = owner
		err = owner.initialize(op.Context(), e.settingDefinitions(), e.settingsOrder, start.Defaults)
	}
	if err == nil {
		err = authErr
	}
	if err == nil {
		transactions := &transactionOwner{session: s}
		_, err = op.Attach(Transaction, transactions)
		if err == nil {
			s.transactions = transactions
		}
	}
	if err == nil {
		var ticket *relationTicket
		ticket, err = e.locks.registerSession(s.token)
		if err == nil {
			s.advisory.ticket = ticket
			_, err = op.Attach(AdvisorySession, &s.advisory)
			if err != nil {
				e.locks.end(ticket)
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
	e.mu.Lock()
	if err == nil {
		role, err = (RoleRead{e, e.roles}).Descriptor(authenticated)
		if err == nil && !role.Login {
			err = roleLoginError(role.Name)
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
			e.sessions = make(map[SessionToken]*Session)
		}
		if e.sessions[s.token] != nil {
			err = errors.New("stuntgres: duplicate session identity")
		} else if !role.Superuser && role.ConnectionLimit >= 0 && e.roleSessionCountLocked(authenticated) >= int(role.ConnectionLimit) {
			err = roleConnectionLimitError(role.Name)
		} else if err = ctx.Err(); err == nil {
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
func (s *Session) Engine() *Engine     { return s.token.owner }
func (s *Session) Token() SessionToken { return s.token }
func (s *Session) operation(ctx context.Context, private bool) *Operation {
	// Caller cancellation propagates through this child context. RequestEnd
	// cancels every admitted child under the same admission lock; Done removes it.
	combined, cancel := context.WithCancel(ctx)
	op := &Operation{session: s, ctx: combined, sourceContext: combined, cancel: cancel, private: private}
	op.self = op
	if s.operations == nil {
		s.operations = make(map[*Operation]struct{})
	}
	s.operations[op] = struct{}{}
	return op
}
func (s *Session) Acquire(ctx context.Context) (*Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != live || s.detaching {
		return nil, errors.New("stuntgres: session unavailable")
	}
	s.leases++
	return s.operation(ctx, false), nil
}
func (op *Operation) Context() context.Context { return op.ctx }
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
	delete(s.operations, op)
	op.cancel()
	if op.timerCancel != nil {
		op.timerCancel()
	}
	s.leases--
	s.cond.Broadcast()
}
func (e *Engine) validateOperation(op *Operation) error {
	if op == nil || op.self != op || op.session == nil || op.session.token.owner != e {
		return errors.New("stuntgres: foreign or missing operation")
	}
	s := op.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.done || s.state != live {
		return errors.New("stuntgres: inactive operation")
	}
	return op.ctx.Err()
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
	if s.state == ending || s.state == ended {
		return
	}
	s.state = ending
	s.reason = reason
	for op := range s.operations {
		op.cancel()
	}
	go s.end()
}
func (s *Session) end() {
	s.mu.Lock()
	for s.leases != 0 || s.detaching {
		s.cond.Wait()
	}
	for _, kind := range [...]ResourceKind{Transaction, Cursors, PreparedSQL, Temporary, AdvisorySession, Settings, Authorization} {
		entry := s.resources[kind]
		if entry == nil {
			continue
		}
		s.resources[kind] = nil
		s.mu.Unlock()
		err := entry.owner.EndSession(s.token, s.reason)
		s.mu.Lock()
		s.removed[entry.handle] = true
		if err != nil {
			s.cleanupErrors = append(s.cleanupErrors, err)
		}
	}
	s.result = errors.Join(s.cleanupErrors...)
	s.token.owner.sessionMu.Lock()
	if s.token.owner.sessions[s.token] == s {
		delete(s.token.owner.sessions, s.token)
	}
	s.token.owner.sessionMu.Unlock()
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
