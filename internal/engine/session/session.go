package session

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
	Session ids.SessionToken
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
type State uint8

const (
	Initializing State = iota
	Live
	Ending
	Ended
)

type resourceEntry struct {
	Handle ResourceHandle
	Owner  SessionResource
}
type Session struct {
	ids.SessionOwnerMarker
	Mu           sync.Mutex
	Cond         *sync.Cond
	Host         Host // Immutable after construction; engineOf recovers the engine.
	SessionToken ids.SessionToken
	// Written before registry publication, never changed by identity switches.
	CountedAuthenticated   authorization.PrincipalRef
	State                  State
	Completed              chan struct{}
	endRequested           chan struct{} // Lazy wake projection of ending, not a lease or completion.
	Leases                 int
	Operations             map[*Operation]struct{}
	detaching              bool
	Serial                 uint64
	Resources              [resourceKinds]*resourceEntry
	Removed                map[ResourceHandle]bool
	Reason                 EndReason
	cleanupErrors          []error
	result                 error
	Settings               *SettingsStore
	Transactions           *TransactionOwner
	Advisory               sessionAdvisory
	Authorization          *AuthorizationState
	DiscardPlansGeneration uint64 // serialized by physical-owner statement admission
	TemporaryNamespace     ids.ObjectAddress
	Random                 RandomState
	ArrayRandom            RandomState // independent of random()/setseed()
	UUID                   uuidClock
	DatabaseName           string
	Timeouts               sessionTimeouts
	Payload                PayloadSlot // Root payload; set before publication, never changed. See payloadOf.
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
	NoticeThreshold    NoticeLevel
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
	ReplaySlot         ReplaySlot              // Restore and materialized population only; see operationReplay.
	Analysis           AnalysisSlot            // Immutable after creation; see Host.NewOperation.
}

func (s *Session) Token() ids.SessionToken { return s.SessionToken }
func (s *Session) Operation(ctx context.Context, private bool) *Operation {
	// Caller cancellation propagates through this child context. RequestEnd
	// cancels every admitted child under the same admission lock; Done removes it.
	combined, cancel := context.WithCancelCause(ctx)
	op := s.newOperation()
	op.Ctx, op.CommandContext, op.SourceContext, op.Cancel, op.private = combined, combined, ctx, cancel, private
	if s.Operations == nil {
		s.Operations = make(map[*Operation]struct{})
	}
	s.Operations[op] = struct{}{}
	return op
}

// newOperation allocates an operation of s through its host. A host-less
// session (unit tests) gets a plain operation without an analysis slot.
func (s *Session) newOperation() *Operation {
	if s.Host != nil {
		return s.Host.NewOperation(s)
	}
	op := &Operation{Session: s}
	op.Self = op
	return op
}
func (s *Session) Acquire(ctx context.Context) (*Operation, error) {
	return s.acquire(ctx, false)
}
func (s *Session) acquire(ctx context.Context, activity bool) (*Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.observeTimeoutsLocked(time.Now())
	if s.State != Live || s.detaching {
		if s.Timeouts.Fatal != nil {
			copy := *s.Timeouts.Fatal
			return nil, &copy
		}
		return nil, errors.New("stuntgres: session unavailable")
	}
	s.Leases++
	op := s.Operation(ctx, false)
	op.sqlActivity = activity
	if activity {
		s.Timeouts.SQLLeases++
		s.Timeouts.Idle.Stop()
	}
	return op, nil
}
func (op *Operation) Context() context.Context {
	if op.CopyInput.Held() {
		op.Session.Mu.Lock()
		ctx := op.CopyInput.effectiveLocked(op)
		op.Session.Mu.Unlock()
		return ctx
	}
	return op.Ctx
}
func (op *Operation) Done() {
	if op == nil || op.Self != op || op.Session == nil {
		return
	}
	s := op.Session
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if op.Released {
		return
	}
	op.Released = true
	op.LockWait.Reset()
	delete(s.Operations, op)
	op.Cancel(nil)
	if op.TimerCancel != nil {
		op.TimerCancel()
	}
	s.Leases--
	if op.sqlActivity {
		s.Timeouts.SQLLeases--
	}
	if op.sqlActivity || op.private {
		s.ReadyTimeoutLocked(time.Now())
	}
	s.Cond.Broadcast()
}

// ValidateOperation reports whether op is a live operation of a session
// in domain. A session's token carries its engine's domain, so session code
// passes its own token's domain.
func ValidateOperation(domain *ids.Domain, op *Operation) error {
	if op == nil || op.Self != op || op.Session == nil || op.Session.SessionToken.Domain() != domain {
		return errors.New("stuntgres: foreign or missing operation")
	}
	s := op.Session
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if op.Released || s.State != Live {
		if s.Timeouts.Fatal != nil {
			copy := *s.Timeouts.Fatal
			return &copy
		}
		return errors.New("stuntgres: inactive operation")
	}
	if op.CopyInput.Held() {
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
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if err := op.Ctx.Err(); err != nil {
		return ResourceHandle{}, err
	}
	if op.Released || (s.State != Live && !(op.private && s.State == Initializing)) || s.detaching {
		return ResourceHandle{}, errors.New("stuntgres: inactive attachment lease")
	}
	if s.Resources[kind] != nil {
		return ResourceHandle{}, errors.New("stuntgres: duplicate session attachment")
	}
	if s.Serial == ^uint64(0) {
		return ResourceHandle{}, errors.New("stuntgres: resource identity exhausted")
	}
	s.Serial++
	h := ResourceHandle{s.SessionToken, kind, s.Serial}
	s.Resources[kind] = &resourceEntry{h, owner}
	return h, nil
}
func (s *Session) Detach(h ResourceHandle, reason EndReason) error {
	s.Mu.Lock()
	if h.Session != s.SessionToken || h.kind >= resourceKinds || h.serial == 0 {
		s.Mu.Unlock()
		return errors.New("stuntgres: foreign resource handle")
	}
	if s.Removed[h] {
		s.Mu.Unlock()
		return nil
	}
	entry := s.Resources[h.kind]
	if entry == nil || entry.Handle != h {
		s.Mu.Unlock()
		return errors.New("stuntgres: stale resource handle")
	}
	if s.State != Live || s.Leases != 0 || s.detaching {
		s.Mu.Unlock()
		return errors.New("stuntgres: session detach requires quiescence")
	}
	s.detaching = true
	s.Resources[h.kind] = nil
	s.Mu.Unlock()
	err := entry.Owner.EndSession(s.SessionToken, reason)
	s.Mu.Lock()
	s.Removed[h] = true
	if err != nil {
		s.cleanupErrors = append(s.cleanupErrors, err)
	}
	s.detaching = false
	s.Cond.Broadcast()
	s.Mu.Unlock()
	return err
}
func (s *Session) RequestEnd(reason EndReason) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.requestEndLocked(reason)
}

// EndRequested wakes observers as soon as ending is requested, before leases
// drain. Check reports the authoritative reason; WaitEnd still joins cleanup.
func (s *Session) EndRequested() <-chan struct{} {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.endRequested == nil {
		s.endRequested = make(chan struct{})
		if s.State == Ending || s.State == Ended {
			close(s.endRequested)
		}
	}
	return s.endRequested
}

func (s *Session) requestEndLocked(reason EndReason) {
	if s.State == Ending || s.State == Ended {
		return
	}
	s.State = Ending
	if s.endRequested != nil {
		close(s.endRequested)
	}
	s.Reason = reason
	s.Timeouts.Transaction.Stop()
	s.Timeouts.Idle.Stop()
	var cause error
	if s.Timeouts.Fatal != nil {
		cause = s.Timeouts.Fatal
	}
	for op := range s.Operations {
		op.Cancel(cause)
		if op.CopyInput.cancel != nil {
			op.CopyInput.cancel(cause)
		}
	}
	go s.end()
}
func (s *Session) end() {
	s.Mu.Lock()
	for s.Leases != 0 || s.detaching {
		s.Cond.Wait()
	}
	for _, kind := range [...]ResourceKind{Transaction, Notifications, Cursors, PreparedSQL, Temporary, AdvisorySession, Settings, Authorization} {
		entry := s.Resources[kind]
		if entry == nil {
			continue
		}
		s.Resources[kind] = nil
		s.Mu.Unlock()
		err := entry.Owner.EndSession(s.SessionToken, s.Reason)
		if kind == Transaction {
			// The transaction callback has returned and released its admission
			// lock. Maintenance never runs inside a SessionResource callback.
			err = errors.Join(err, s.Host.EndMaintenance(s))
		}
		s.Mu.Lock()
		s.Removed[entry.Handle] = true
		if err != nil {
			s.cleanupErrors = append(s.cleanupErrors, err)
		}
	}
	s.result = errors.Join(s.cleanupErrors...)
	s.Host.Unregister(s.SessionToken, s)
	s.State = Ended
	close(s.Completed)
	s.Mu.Unlock()
}
func (s *Session) WaitEnd(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.Completed:
		s.Mu.Lock()
		defer s.Mu.Unlock()
		return s.result
	}
}
func (s *Session) Reuse(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.State != Live {
		return fmt.Errorf("stuntgres: session is not reusable")
	}
	return nil
}
func (s *Session) IsLive() bool { s.Mu.Lock(); defer s.Mu.Unlock(); return s.State == Live }
