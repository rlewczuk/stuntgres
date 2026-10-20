package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
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
	Defaults      settings.SessionDefaultProvider
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
	mu    sync.Mutex
	cond  *sync.Cond
	host  session.Host // Immutable after construction; engineOf recovers the engine.
	token ids.SessionToken
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
	advisory               sessionAdvisory
	authorization          *sessionAuthorization
	discardPlansGeneration uint64 // serialized by physical-owner statement admission
	temporaryNamespace     ids.ObjectAddress
	random                 randomState
	arrayRandom            randomState // independent of random()/setseed()
	uuid                   uuidClock
	databaseName           string
	timeouts               sessionTimeouts
	payload                session.PayloadSlot // Root payload; set before publication, never changed. See payloadOf.
}

// Operation is pointer-owned; copying it is invalid. Done releases its lease once.
type Operation struct {
	Self               *Operation
	Session            *Session
	Ctx                context.Context
	CopyInput          protocolCopyInputContext
	CommandContext     context.Context // Session-cancelable parent of per-statement budgets.
	Cancel             context.CancelCauseFunc
	Released           bool
	private            bool
	Notices            []sqlstate.Error
	NoticeThreshold    noticeLevel
	TimerCancel        context.CancelFunc
	LockTimeout        time.Duration
	ParallelWorkers    int // Immutable prior-setting snapshot at command admission.
	StatementDeadline  time.Time
	CommandStart       time.Time
	SourceContext      context.Context
	CommandAdmission   bool
	sqlActivity        bool
	SQLFinished        bool // Session.mu: ERROR/completion fence, not transaction state
	Lease              *StatementLease
	Definition         catalog.DefinitionReader // Scoped once-only DDL cooking, restored before evaluation.
	LockWait           lock.TimeoutSlot         // queued lock request's SQL-deadline projection
	NonstandardStrings bool
	DiscardRoleReset   authorization.RoleState // nonzero Authenticated only during DISCARD ALL publication
	ReplaySlot         session.ReplaySlot      // Restore and materialized population only; see operationReplay.
	Analysis           session.AnalysisSlot    // Immutable after creation; see newSessionOperation.
}

func (s *Session) Token() ids.SessionToken { return s.token }
func (s *Session) operation(ctx context.Context, private bool) *Operation {
	// Caller cancellation propagates through this child context. RequestEnd
	// cancels every admitted child under the same admission lock; Done removes it.
	combined, cancel := context.WithCancelCause(ctx)
	op := newSessionOperation(s)
	op.Ctx, op.CommandContext, op.SourceContext, op.Cancel, op.private = combined, combined, ctx, cancel, private
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
	s.observeTimeoutsLocked(time.Now())
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
	if op.CopyInput.held() {
		op.Session.mu.Lock()
		ctx := op.CopyInput.effectiveLocked(op)
		op.Session.mu.Unlock()
		return ctx
	}
	return op.Ctx
}
func (op *Operation) Done() {
	if op == nil || op.Self != op || op.Session == nil {
		return
	}
	s := op.Session
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.Released {
		return
	}
	op.Released = true
	op.LockWait.Reset()
	delete(s.operations, op)
	op.Cancel(nil)
	if op.TimerCancel != nil {
		op.TimerCancel()
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

// validateSessionOperation reports whether op is a live operation of a session
// in domain. A session's token carries its engine's domain, so session code
// passes its own token's domain.
func validateSessionOperation(domain *ids.Domain, op *Operation) error {
	if op == nil || op.Self != op || op.Session == nil || op.Session.token.Domain() != domain {
		return errors.New("stuntgres: foreign or missing operation")
	}
	s := op.Session
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.Released || s.state != live {
		if s.timeouts.fatal != nil {
			copy := *s.timeouts.fatal
			return &copy
		}
		return errors.New("stuntgres: inactive operation")
	}
	if op.CopyInput.held() {
		return op.FatalErrorLocked(op.CopyInput.effectiveLocked(op).Err())
	}
	return op.FatalErrorLocked(op.Ctx.Err())
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
	if op == nil || op.Self != op || op.Session == nil || kind >= resourceKinds || nilResource(owner) {
		return ResourceHandle{}, errors.New("stuntgres: invalid session attachment")
	}
	s := op.Session
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := op.Ctx.Err(); err != nil {
		return ResourceHandle{}, err
	}
	if op.Released || (s.state != live && !(op.private && s.state == initializing)) || s.detaching {
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
		op.Cancel(cause)
		if op.CopyInput.cancel != nil {
			op.CopyInput.cancel(cause)
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
			err = errors.Join(err, s.host.EndMaintenance(s))
		}
		s.mu.Lock()
		s.removed[entry.handle] = true
		if err != nil {
			s.cleanupErrors = append(s.cleanupErrors, err)
		}
	}
	s.result = errors.Join(s.cleanupErrors...)
	s.host.Unregister(s.token, s)
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
