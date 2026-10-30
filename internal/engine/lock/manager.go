package lock

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type Policy struct {
	NoWait            bool
	Timeout           time.Duration
	StatementDeadline time.Time
	Source            context.Context
	Provenance        ids.TxRef         // current statement owner for a persistent session request
	TimeoutSlot       *TimeoutSlot      // admitted command's pending-timeout projection; nil when untracked
	CompletionPurpose CompletionPurpose // observational identity; never changes arbitration
}

type SourceError struct{ error }

func (e SourceError) Unwrap() error { return e.error }

// WaitError is the outcome a wait has at now: absolute SQL deadlines, not
// callback scheduling, decide it. Nil means the wait may continue.
func WaitError(ctx context.Context, policy Policy, lockDeadline, now time.Time) error {
	if cause := relationTimeoutSourceCause(policy, lockDeadline, now); cause != nil {
		return SourceError{cause}
	}
	if err := relationTimeoutError(relationTimeoutWinner(policy, lockDeadline, now)); err != nil {
		return err
	}
	if cause := context.Cause(ctx); cause != nil {
		var fatal *sqlstate.Error
		if errors.As(cause, &fatal) && fatal.Severity == "FATAL" {
			copy := *fatal
			return &copy
		}
	}
	return ctx.Err()
}

type Mode uint32

const (
	AccessShare Mode = 1 << iota
	RowExclusive
	AccessExclusive
	ShareUpdateExclusive
	ForUpdate
	ForNoKeyUpdate
	ShareRowExclusive
	ForKeyShare
	ForShare
	RowShare
	Share
	Exclusive
	UniqueClaim
	TransactionCompletion
	AdvisoryShare
	AdvisoryExclusive
	TypeExclusive
)

type ResourceKind uint8

const (
	OrdinaryResource ResourceKind = iota
	AdvisoryBigintResource
	AdvisoryPairResource
	CatalogACLResource
	DefaultACLResource
	RoleSettingResource
)

// Row zero is the relation resource; logical rows use their stable RowID.
// Keep the comparable key inline (at most 128 bytes); oversized map keys make
// even ordinary immediate requests escape. Default claims share High/Low only
// under their distinct kind, and keep their class in the tag's existing padding.
type Resource struct {
	ids.ObjectRef
	// Keep the discriminators adjacent: Resource must remain small enough for
	// by-value closure capture on the allocation-free immediate-acquire path.
	Kind         ResourceKind
	RoleTuple    bool // Internal authid tuple claim, distinct from a role object lock.
	DefaultClass uint8
	Database     *ids.Domain // Advisory keys are scoped to their originating database.
	// Name is an exact catalog binding claim. ObjectRef carries its real
	// domain/epoch and namespace (zero address for database-wide schema names).
	// It is not a relation lock or a fabricated catalog object identity.
	Name       ids.NameKey
	High, Low  uint32
	Row        ids.RowID
	key        *UniqueKey
	completion *Ticket
}
type Ticket struct {
	manager            *Manager
	ref                ids.TxRef
	session            ids.SessionToken
	persistent         bool
	detached           bool // prepared transaction: independent of its former physical session
	resources          map[Resource]struct{}
	assignedXID        uint32 // sync/atomic only: canonical TX mirror; end/completed utility phase retires ownership
	fullXID            uint64 // sync/atomic only: exact canonical phase identity, not an allocator.
	ended              uint32 // sync/atomic only: ownership retirement, readable without manager.mu.
	completion         uint32 // sync/atomic only: transactionIdentityState before ownership release.
	multiContributions map[MultiTuple]uint8
	multiCache         []uint32
	uniqueDeltaActive  bool
	uniqueDelta        []Resource
}

// Ref is the transaction a ticket belongs to; zero for a session ticket.
func (t *Ticket) Ref() ids.TxRef { return t.ref }

// Session is the session that owns the ticket.
func (t *Ticket) Session() ids.SessionToken { return t.session }

// Persistent reports a session-lifetime (advisory) ticket.
func (t *Ticket) Persistent() bool { return t.persistent }

func (t *Ticket) AssignedPublicXID() uint32 { return atomic.LoadUint32(&t.assignedXID) }
func (t *Ticket) MirrorFullXID(xid uint64) {
	atomic.StoreUint64(&t.fullXID, xid)
	atomic.StoreUint32(&t.assignedXID, uint32(xid))
}
func (t *Ticket) AssignedFullXID() uint64 { return atomic.LoadUint64(&t.fullXID) }
func (t *Ticket) OwnershipEnded() bool    { return atomic.LoadUint32(&t.ended) != 0 }
func (t *Ticket) retireOwnership()        { atomic.StoreUint32(&t.ended, 1) }
func (t *Ticket) CompletionState() ids.TxCompletion {
	return ids.TxCompletion(atomic.LoadUint32(&t.completion))
}
func (t *Ticket) MirrorCompletion(state ids.TxCompletion) {
	atomic.StoreUint32(&t.completion, uint32(state))
}

// Request is one queued acquisition. Its defining type keeps the name
// relationRequest: the reference suite recognizes a parked lock wait by the
// frame (*relationRequest).wait.
type Request = relationRequest

type relationRequest struct {
	ticket       *Ticket
	provenance   ids.TxRef
	ctx          context.Context
	mode         Mode
	contribution *RelationContribution
	done         chan struct{}
	err          error
	terminal     bool
	notifyNext   *relationRequest
	stop         func() bool
	timer        *time.Timer
	policy       Policy
	deadline     time.Time
	enqueuedAt   time.Time
	ref          Resource
	entry        *relationEntry
	timeoutState atomic.Uint32
}

// Mode is the visible union of ordinary and scoped ownership. Ordinary grants
// remain idempotent; advisory and scoped relation contributions are counted.
type Hold struct {
	Mode              Mode
	Shared, Exclusive uint64
	ordinary          Mode
	scoped            [2]uint64
}

type relationEntry struct {
	holders map[*Ticket]Hold
	pending []*relationRequest
	pins    int
}

// Manager owns heavyweight lock state: transaction and session tickets,
// resource queues and grants, unique-key buckets, and MultiXact rows and
// history. mu guards all of it; engine code reaches it only through Manager
// methods. mu is innermost in the engine's lock order (see README.md): it is
// never taken under Engine.mu or a session mutex, and while it is held no
// other mutex is taken, no callback runs and nothing blocks.
type Manager struct {
	mu         sync.Mutex
	domains    map[*ids.Domain]*databaseState
	roleDomain *ids.Domain
	roleEpoch  ids.CatalogEpoch
	owners     map[ids.TxRef]*Ticket
	sessions   map[ids.SessionToken]*Ticket
	entries    map[Resource]*relationEntry
	keyBuckets map[uniqueLockBucket]*uniqueLockBucketState
	multiRows  map[MultiTuple]multiRow
}

type databaseState struct {
	epoch        ids.CatalogEpoch
	multiNext    uint32
	multiHistory map[uint32][]MultiMember
}

// New admits the initial database into the cluster's canonical graph.
func New(domain *ids.Domain) *Manager {
	m := &Manager{domains: make(map[*ids.Domain]*databaseState), owners: make(map[ids.TxRef]*Ticket), entries: make(map[Resource]*relationEntry)}
	if domain != nil {
		m.domains[domain] = &databaseState{multiNext: 1}
	}
	return m
}

// AdmitDomain registers database provenance before any of its owners are admitted.
func (m *Manager) AdmitDomain(domain *ids.Domain) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if domain == nil || domain == m.roleDomain {
		return errors.New("stuntgres: invalid lock database domain")
	}
	if m.domains[domain] == nil {
		m.domains[domain] = &databaseState{multiNext: 1}
	}
	return nil
}

// SetRoleDomain installs the cluster's stable shared-role resource authority.
func (m *Manager) SetRoleDomain(domain *ids.Domain, epoch ids.CatalogEpoch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if domain == nil || epoch == 0 || m.domains[domain] != nil || m.roleDomain != nil {
		return errors.New("stuntgres: invalid shared role domain")
	}
	m.roleDomain, m.roleEpoch = domain, epoch
	return nil
}

// Register admits a transaction ticket. The engine has already checked that
// ref is attached to its transaction owner and session (session.TxOwnerAttached).
func (m *Manager) Register(ref ids.TxRef) (*Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.domains[ref.Domain()]
	if state == nil || ref.ID() == 0 || ref.Epoch() == 0 || ref.Attachment() == nil || ref.Session().Domain() != ref.Domain() || ref.Session().ID() == 0 && ref.Session().Private() == nil {
		return nil, errors.New("stuntgres: invalid lock owner")
	}
	if ref.Epoch() < state.epoch {
		return nil, ids.ErrCatalogReplaced
	}
	if _, ok := m.owners[ref]; ok {
		return nil, errors.New("stuntgres: duplicate lock owner")
	}
	t := &Ticket{manager: m, ref: ref, session: ref.Session(), resources: make(map[Resource]struct{})}
	m.owners[ref] = t
	return t, nil
}
func Conflicts(a, b Mode) bool {
	if a&TypeExclusive != 0 || b&TypeExclusive != 0 {
		return a&TypeExclusive != 0 && b&TypeExclusive != 0
	}
	if a&(AdvisoryShare|AdvisoryExclusive) != 0 || b&(AdvisoryShare|AdvisoryExclusive) != 0 {
		return a&AdvisoryExclusive != 0 && b&(AdvisoryShare|AdvisoryExclusive) != 0 ||
			b&AdvisoryExclusive != 0 && a&(AdvisoryShare|AdvisoryExclusive) != 0
	}
	if a&TransactionCompletion != 0 || b&TransactionCompletion != 0 {
		return a&TransactionCompletion != 0 && b&TransactionCompletion != 0
	}
	if a&UniqueClaim != 0 || b&UniqueClaim != 0 {
		return a&UniqueClaim != 0 && b&UniqueClaim != 0
	}
	rowModes := ForKeyShare | ForShare | ForNoKeyUpdate | ForUpdate
	if a&rowModes != 0 || b&rowModes != 0 {
		return a&ForUpdate != 0 && b&rowModes != 0 || b&ForUpdate != 0 && a&rowModes != 0 ||
			a&ForNoKeyUpdate != 0 && b&(ForShare|ForNoKeyUpdate) != 0 ||
			b&ForNoKeyUpdate != 0 && a&(ForShare|ForNoKeyUpdate) != 0
	}
	if a&AccessExclusive != 0 || b&AccessExclusive != 0 {
		return a != 0 && b != 0
	}
	exclusiveConflicts := RowShare | RowExclusive | ShareUpdateExclusive | Share | ShareRowExclusive | Exclusive
	shareConflicts := RowExclusive | ShareUpdateExclusive | ShareRowExclusive | Exclusive
	srxConflicts := RowExclusive | ShareUpdateExclusive | Share | ShareRowExclusive | Exclusive
	return a&Exclusive != 0 && b&exclusiveConflicts != 0 ||
		b&Exclusive != 0 && a&exclusiveConflicts != 0 ||
		a&Share != 0 && b&shareConflicts != 0 ||
		b&Share != 0 && a&shareConflicts != 0 ||
		a&ShareRowExclusive != 0 && b&srxConflicts != 0 ||
		b&ShareRowExclusive != 0 && a&srxConflicts != 0 ||
		a&ShareUpdateExclusive != 0 && b&ShareUpdateExclusive != 0
}
func (m *Manager) valid(t *Ticket) bool {
	return t != nil && t.manager == m && (t.persistent && m.sessions[t.session] == t || !t.persistent && m.owners[t.ref] == t)
}

// A checkpoint contains only held contributions, never pending requests.
type CheckpointHold struct {
	Mode               Mode
	Multi              map[ids.VersionID]uint8
	InheritedSingleton map[ids.VersionID]bool
}
type Checkpoint map[Resource]CheckpointHold

func (m *Manager) Checkpoint(t *Ticket) Checkpoint {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(t) || t.persistent {
		return nil
	}
	held := make(Checkpoint, len(t.resources))
	for ref := range t.resources {
		if ref.completion != nil {
			continue
		}
		if entry := m.entries[ref]; entry != nil && entry.holders[t].Mode != 0 {
			held[ref] = CheckpointHold{Mode: entry.holders[t].ordinary}
		}
	}
	for tuple, bits := range t.multiContributions {
		h := held[tuple.Resource]
		if h.Multi == nil {
			h.Multi = make(map[ids.VersionID]uint8)
		}
		h.Multi[tuple.Version] = bits
		if m.multiRows[tuple].inheritedSingleton {
			if h.InheritedSingleton == nil {
				h.InheritedSingleton = make(map[ids.VersionID]bool)
			}
			h.InheritedSingleton[tuple.Version] = true
		}
		held[tuple.Resource] = h
	}
	return held
}

// Caller serializes the owner but holds no catalog/session/publication locks.
// Intersecting prevents a stale checkpoint from resurrecting ownership.
func (m *Manager) Restore(t *Ticket, held Checkpoint) {
	var notify *relationRequest
	m.mu.Lock()
	if m.valid(t) && !t.persistent {
		m.restoreMultiContributionsLocked(t, held)
		for ref := range t.resources {
			if ref.completion != nil {
				continue
			}
			entry := m.entries[ref]
			if entry == nil {
				continue
			}
			hold := entry.holders[t]
			ordinary := hold.ordinary & held[ref].Mode
			if ordinary == hold.ordinary {
				continue
			}
			hold.ordinary = ordinary
			hold.Mode = ordinary | hold.scopedModes()
			if hold.Mode == 0 {
				delete(entry.holders, t)
				delete(t.resources, ref)
			} else {
				entry.holders[t] = hold
			}
			m.settle(entry, &notify)
			m.retire(ref, entry)
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
}

// Candidate omission changes only key contributions, not source row/relation
// grants. Reuse caller-owned scratch and preserve preexisting stronger modes.
func (m *Manager) CheckpointUnique(t *Ticket, held Checkpoint) Checkpoint {
	m.mu.Lock()
	defer m.mu.Unlock()
	if held == nil {
		held = make(Checkpoint)
	}
	clear(held)
	if m.valid(t) && !t.persistent {
		for ref := range t.resources {
			if ref.key != nil && ref.Kind == OrdinaryResource && ref.completion == nil {
				if entry := m.entries[ref]; entry != nil && entry.holders[t].Mode != 0 {
					held[ref] = CheckpointHold{Mode: entry.holders[t].Mode}
				}
			}
		}
	}
	return held
}
func (m *Manager) RestoreUnique(t *Ticket, held Checkpoint) {
	var notify *relationRequest
	m.mu.Lock()
	if m.valid(t) && !t.persistent {
		for ref := range t.resources {
			if ref.key == nil || ref.Kind != OrdinaryResource || ref.completion != nil {
				continue
			}
			entry := m.entries[ref]
			if entry == nil {
				continue
			}
			hold := entry.holders[t]
			mode := hold.Mode & held[ref].Mode
			if mode == hold.Mode {
				continue
			}
			if mode == 0 {
				delete(entry.holders, t)
				delete(t.resources, ref)
			} else {
				hold.Mode = mode
				hold.ordinary &= mode
				entry.holders[t] = hold
			}
			m.settle(entry, &notify)
			m.retire(ref, entry)
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
}
func terminalRequest(r *relationRequest, err error, notify **relationRequest) {
	err = r.finishTimeout(err)
	r.err = err
	r.terminal = true
	if (err != nil || r.ref.completion != nil) && r.entry.holders[r.ticket].Mode == 0 {
		pending := false
		for _, other := range r.entry.pending {
			if other != r && !other.terminal && other.ticket == r.ticket {
				pending = true
				break
			}
		}
		if !pending {
			delete(r.ticket.resources, r.ref)
		}
	}
	if r.ticket.persistent {
		if source := r.ticket.manager.owners[r.provenance]; source != nil {
			pending := false
			for _, other := range r.entry.pending {
				if other != r && !other.terminal && other.provenance == r.provenance {
					pending = true
					break
				}
			}
			if !pending && r.entry.holders[source].Mode == 0 {
				delete(source.resources, r.ref)
			}
		}
	}
	if r.stop != nil {
		r.stop()
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	r.notifyNext = *notify
	*notify = r
}
func notifyRequests(r *relationRequest) {
	for r != nil {
		next := r.notifyNext
		close(r.done)
		r = next
	}
}
func (m *Manager) settle(e *relationEntry, notify **relationRequest) {
	remaining := e.pending[:0]
	for _, r := range e.pending {
		if r.terminal {
			continue
		}
		if !m.validRequest(r) {
			terminalRequest(r, ids.ErrCatalogReplaced, notify)
			continue
		}
		if err := r.timeoutWaitError(time.Now()); err != nil {
			terminalRequest(r, err, notify)
			continue
		}
		blocked, participantHeld := false, false
		for t, held := range e.holders {
			if sameLockSession(t, r.ticket) {
				if (r.ref.Kind == AdvisoryBigintResource || r.ref.Kind == AdvisoryPairResource) && held.Mode != 0 {
					participantHeld = true
				}
			} else if Conflicts(r.mode, held.Mode) {
				blocked = true
			}
		}
		if !blocked && !participantHeld {
			for _, earlier := range remaining {
				if r.ref.completion == nil && (r.ref.Row == 0 || r.mode != ForNoKeyUpdate) && !sameLockSession(earlier.ticket, r.ticket) && Conflicts(r.mode, earlier.mode) {
					blocked = true
					break
				}
			}
		}
		if blocked {
			remaining = append(remaining, r)
			continue
		}
		if err := r.timeoutWaitError(time.Now()); err != nil {
			terminalRequest(r, err, notify)
			continue
		}
		err := r.claimTimeoutTerminal(nil)
		if err == nil {
			err = m.grant(r.ticket, r.ref, e, r.mode, r.contribution)
		}
		terminalRequest(r, err, notify)
	}
	clear(e.pending[len(remaining):])
	e.pending = remaining
}
func (m *Manager) Acquire(ctx context.Context, t *Ticket, ref Resource, mode Mode) error {
	request, err := m.StartAcquire(ctx, t, ref, mode, Policy{})
	if err != nil || request == nil {
		return err
	}
	return request.Wait()
}

// Wait blocks until the request is terminal and returns its outcome.
func (r *relationRequest) Wait() error { return r.wait() }

// wait does the blocking. The reference suite recognizes a parked lock wait
// by this frame's name, (*relationRequest).wait, so keep both names.
func (r *relationRequest) wait() error {
	<-r.done
	return r.err
}

// Done is closed once the request is terminal; Err is valid after that.
func (r *relationRequest) Done() <-chan struct{} { return r.done }

// Err is the terminal outcome. Read it only after Done is closed.
func (r *relationRequest) Err() error { return r.err }

// StartAcquire grants immediately or returns the exact journaled request.
// The caller can relinquish replacement admission before waiting, without
// allowing replacement to interrupt an uncontended implicit statement.
func (m *Manager) StartAcquire(ctx context.Context, t *Ticket, ref Resource, mode Mode, policy Policy) (*relationRequest, error) {
	return m.startAcquire(ctx, t, ref, mode, policy, nil)
}

func (m *Manager) startAcquire(ctx context.Context, t *Ticket, ref Resource, mode Mode, policy Policy, contribution *RelationContribution) (*relationRequest, error) {
	if err := WaitError(ctx, policy, time.Time{}, time.Now()); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if contribution != nil {
		if contribution.started {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: reused scoped relation contribution")
		}
		contribution.started = true
	}
	if !m.valid(t) || !t.persistent && t.ref.Epoch() < m.domains[t.ref.Domain()].epoch {
		m.mu.Unlock()
		return nil, ids.ErrCatalogReplaced
	}
	advisory := ref.Kind == AdvisoryBigintResource || ref.Kind == AdvisoryPairResource
	catalogACL := ref.Kind == CatalogACLResource || ref.Kind == DefaultACLResource
	roleSetting := ref.Kind == RoleSettingResource
	if advisory {
		if ref.Database != t.session.Domain() || ref.ObjectRef != (ids.ObjectRef{}) || ref.RoleTuple || ref.Name != (ids.NameKey{}) || ref.Row != 0 || ref.key != nil || ref.completion != nil || ref.DefaultClass != 0 ||
			mode != AdvisoryShare && mode != AdvisoryExclusive || t.detached {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid advisory lock request")
		}
		if t.persistent {
			source := m.owners[policy.Provenance]
			if source == nil || source.detached || source.session != t.session || policy.Provenance.Epoch() < m.domains[source.ref.Domain()].epoch {
				m.mu.Unlock()
				return nil, ids.ErrCatalogReplaced
			}
		}
	} else if t.persistent || ref.Database != nil || !catalogACL && !roleSetting && (ref.Kind != OrdinaryResource || ref.High != 0 || ref.Low != 0 || ref.DefaultClass != 0) || ref.RoleTuple && (catalogACL || roleSetting || ref.Address.ClassID != ids.RoleClassID) {
		m.mu.Unlock()
		return nil, errors.New("stuntgres: invalid ordinary lock resource")
	}
	if roleSetting {
		if ref.Domain() != m.roleDomain || ref.Epoch != m.roleEpoch || mode != AccessExclusive ||
			ref.Address != (ids.ObjectAddress{}) && (ref.Address.ClassID != ids.DatabaseClassID || ref.Address.ObjectID == 0 || ref.Address.SubID != 0) ||
			ref.Low != 0 || ref.DefaultClass != 0 || ref.RoleTuple || ref.Name != (ids.NameKey{}) || ref.Row != 0 || ref.key != nil || ref.completion != nil {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid role settings lock request")
		}
	} else if catalogACL {
		if !m.validACLResource(t, ref, mode) {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid catalog ACL lock request")
		}
	} else if advisory {
		// Advisory tags have no catalog ObjectRef, for either lifetime.
	} else if ref.Name != (ids.NameKey{}) {
		key := ref.Name
		validNamespace := key.Namespace.ClassID == ids.NamespaceClassID && key.Namespace.ObjectID != 0 && key.Namespace.SubID == 0
		overloaded := key.ClassID == ids.ProcedureClassID || key.ClassID == ids.OperatorClassID
		validSignature := overloaded || key.Signature == ""
		validClass := (key.ClassID == ids.RelationClassID || key.ClassID == ids.TypeClassID || overloaded ||
			key.ClassID == ids.StatisticExtClassID || key.ClassID == ids.CollationClassID || key.ClassID == ids.ConversionClassID ||
			key.ClassID == ids.TSTemplateClassID || key.ClassID == ids.TSDictionaryClassID || key.ClassID == ids.TSConfigurationClassID || key.ClassID == ids.TSParserClassID) && validNamespace ||
			key.ClassID == ids.NamespaceClassID && key.Namespace == (ids.ObjectAddress{}) ||
			(key.ClassID == ids.CastClassID || key.ClassID == ids.TransformClassID) && key.Namespace == (ids.ObjectAddress{}) && validCatalogPairName(key.Name)
		switch key.ClassID {
		case ids.PublicationClassID, ids.AccessMethodClassID, ids.AccessMethodOperatorClassID, ids.AccessMethodProcedureClassID, ids.EventTriggerClassID, ids.LanguageClassID:
			validClass = key.Namespace == (ids.ObjectAddress{})
		case ids.OperatorFamilyClassID, ids.OperatorClassCatalogID:
			validClass = validNamespace
			validSignature = key.Signature != ""
		}
		roleName := key.ClassID == ids.RoleClassID && key.Namespace == (ids.ObjectAddress{}) && ref.Domain() == m.roleDomain && ref.Epoch == m.roleEpoch
		subscriptionName := key.ClassID == ids.SubscriptionClassID && key.Namespace.ClassID == ids.DatabaseClassID && key.Namespace.ObjectID != 0 && key.Namespace.SubID == 0 && ref.Domain() == m.roleDomain && ref.Epoch == m.roleEpoch
		parameterName := key.ClassID == ids.ParameterClassID && key.Namespace == (ids.ObjectAddress{}) && ref.Domain() == m.roleDomain && ref.Epoch == m.roleEpoch
		localName := validClass && ref.Domain() == t.ref.Domain() && ref.Epoch == t.ref.Epoch()
		if (!localName && !roleName && !subscriptionName && !parameterName) || key.Name == "" || !validSignature ||
			ref.Address != key.Namespace || ref.RoleTuple || ref.Row != 0 || ref.key != nil || ref.completion != nil || mode != AccessExclusive {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid catalog name lock request")
		}
	} else if ref.completion != nil {
		owner := ref.completion
		if mode != TransactionCompletion || owner == t || owner.manager != m || owner.persistent || ref.ObjectRef != (ids.ObjectRef{}) || ref.RoleTuple || ref.Row != 0 || ref.key != nil {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid transaction completion request")
		}
		if !m.valid(owner) {
			if !owner.OwnershipEnded() {
				m.mu.Unlock()
				return nil, errors.New("stuntgres: invalid transaction completion owner")
			}
			m.mu.Unlock()
			return nil, nil
		}
	} else if ref.Address.ClassID == ids.SubscriptionClassID {
		if ref.Domain() != m.roleDomain || ref.Epoch != m.roleEpoch || ref.Address.ObjectID == 0 || ref.Address.SubID != 0 || ref.RoleTuple || ref.Row != 0 || ref.key != nil || mode != AccessShare && mode != AccessExclusive {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid shared subscription lock request")
		}
	} else if ref.Address.ClassID == ids.RoleClassID {
		if ref.Domain() != m.roleDomain || ref.Epoch != m.roleEpoch || ref.Address.ObjectID == 0 || ref.Address.SubID != 0 || ref.Row != 0 || ref.key != nil ||
			ref.RoleTuple && mode != AccessExclusive || !ref.RoleTuple && mode != AccessShare && mode != ShareUpdateExclusive && mode != AccessExclusive {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid shared role lock request")
		}
	} else if ref.Address.ClassID == ids.NamespaceClassID {
		if ref.Domain() != t.ref.Domain() || ref.Epoch != t.ref.Epoch() || ref.Address.SubID != 0 || ref.Address.ObjectID == 0 ||
			ref.Row != 0 || ref.key != nil || mode != AccessShare && mode != AccessExclusive {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid namespace lock request")
		}
	} else if ref.Address.ClassID == ids.ProcedureClassID {
		if ref.Domain() != t.ref.Domain() || ref.Epoch != t.ref.Epoch() || ref.Address.SubID != 0 || ref.Address.ObjectID == 0 ||
			ref.Row != 0 || ref.key != nil || mode != AccessShare && mode != AccessExclusive {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid procedure lock request")
		}
	} else if ref.Address.ClassID == ids.AccessMethodClassID || ref.Address.ClassID == ids.OperatorFamilyClassID || ref.Address.ClassID == ids.OperatorClassCatalogID {
		if ref.Domain() != t.ref.Domain() || ref.Epoch != t.ref.Epoch() || ref.Address.SubID != 0 || ref.Address.ObjectID == 0 || ref.Row != 0 || ref.key != nil || mode != AccessShare && mode != AccessExclusive {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid definition object lock request")
		}
	} else if ref.Address.ClassID == ids.TypeClassID {
		if ref.Domain() != t.ref.Domain() || ref.Epoch != t.ref.Epoch() || ref.Address.SubID != 0 || ref.Address.ObjectID == 0 || ref.Row != 0 || ref.key != nil || mode != TypeExclusive {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid type lock request")
		}
	} else if ref.Address.ClassID == ids.StatisticExtClassID || ref.Address.ClassID == ids.CollationClassID || ref.Address.ClassID == ids.ConversionClassID ||
		ref.Address.ClassID == ids.PublicationClassID || ref.Address.ClassID == ids.CastClassID || ref.Address.ClassID == ids.TransformClassID || ref.Address.ClassID == ids.EventTriggerClassID || ref.Address.ClassID == ids.LanguageClassID ||
		ref.Address.ClassID == ids.TSTemplateClassID || ref.Address.ClassID == ids.TSDictionaryClassID || ref.Address.ClassID == ids.TSConfigurationClassID || ref.Address.ClassID == ids.TSParserClassID {
		if ref.Domain() != t.ref.Domain() || ref.Epoch != t.ref.Epoch() || ref.Address.SubID != 0 || ref.Address.ObjectID == 0 ||
			ref.RoleTuple || ref.Row != 0 || ref.key != nil || mode != AccessShare && mode != AccessExclusive {
			m.mu.Unlock()
			return nil, errors.New("stuntgres: invalid definition lock request")
		}
	} else if ref.Domain() != t.ref.Domain() || ref.Epoch != t.ref.Epoch() || ref.Address.ClassID != ids.RelationClassID || ref.Address.SubID != 0 || ref.Address.ObjectID == 0 || (ref.key != nil && (ref.Row != 0 || mode != UniqueClaim || ref.key.bucket.ObjectRef != ref.ObjectRef)) || (ref.key == nil && ((ref.Row != 0 && mode != ForUpdate && mode != ForNoKeyUpdate && mode != ForKeyShare && mode != ForShare) || (ref.Row == 0 && mode != AccessShare && mode != RowExclusive && mode != AccessExclusive && mode != ShareUpdateExclusive && mode != ShareRowExclusive && mode != RowShare && mode != Share && mode != Exclusive))) {
		m.mu.Unlock()
		return nil, errors.New("stuntgres: invalid relation lock request")
	}
	e := m.entries[ref]
	if e == nil {
		e = &relationEntry{holders: make(map[*Ticket]Hold)}
		if ref.completion != nil {
			e.holders[ref.completion] = Hold{Mode: TransactionCompletion}
			ref.completion.resources[ref] = struct{}{}
		}
		m.entries[ref] = e
	}
	if e.holders[t].Mode&mode != 0 {
		err := WaitError(ctx, policy, time.Time{}, time.Now())
		if err == nil {
			err = m.grant(t, ref, e, mode, contribution)
		}
		m.mu.Unlock()
		return nil, err
	}
	eligible, participantHeld := true, false
	for other, held := range e.holders {
		if advisory && sameLockSession(other, t) && held.Mode != 0 {
			participantHeld = true
		}
		if !sameLockSession(other, t) && Conflicts(mode, held.Mode) {
			eligible = false
		}
	}
	// Self reentry bypasses requests blocked by this owner's existing hold.
	// A compatible tuple NO KEY UPDATE also need not wait behind a pending
	// UPDATE blocked by KEY SHARE; actual granted tuple modes still conflict.
	if eligible && e.holders[t].Mode == 0 && !participantHeld {
		for _, earlier := range e.pending {
			if ref.completion == nil && (ref.Row == 0 || mode != ForNoKeyUpdate) && !earlier.terminal && !sameLockSession(earlier.ticket, t) && Conflicts(mode, earlier.mode) {
				eligible = false
				break
			}
		}
	}
	if eligible {
		err := WaitError(ctx, policy, time.Time{}, time.Now())
		if err == nil {
			err = m.grant(t, ref, e, mode, contribution)
		}
		m.retire(ref, e)
		m.mu.Unlock()
		return nil, err
	}
	if err := WaitError(ctx, policy, time.Time{}, time.Now()); err != nil {
		m.retire(ref, e)
		m.mu.Unlock()
		return nil, err
	}
	if policy.NoWait {
		m.retire(ref, e)
		m.mu.Unlock()
		return nil, ErrUnavailable
	}
	if t.resources == nil {
		t.resources = make(map[Resource]struct{})
	}
	t.resources[ref] = struct{}{}
	provenance := t.ref
	if t.persistent {
		provenance = policy.Provenance
		m.owners[provenance].resources[ref] = struct{}{}
	}
	r := &relationRequest{ticket: t, provenance: provenance, ctx: ctx, mode: mode, contribution: contribution, done: make(chan struct{}), policy: policy, ref: ref, entry: e}
	if policy.Timeout > 0 {
		r.deadline = time.Now().Add(policy.Timeout)
	}
	if r.tracksTimeout() {
		policy.TimeoutSlot.request.Store(r)
	}
	interrupt := func() {
		var notify *relationRequest
		m.mu.Lock()
		if !r.terminal {
			if err := r.timeoutWaitError(time.Now()); err != nil {
				terminalRequest(r, err, &notify)
			}
			m.settle(e, &notify)
			m.retire(ref, e)
		}
		m.mu.Unlock()
		notifyRequests(notify)
	}
	r.stop = context.AfterFunc(ctx, interrupt)
	if !r.deadline.IsZero() {
		r.timer = time.AfterFunc(time.Until(r.deadline), interrupt)
	}
	var notify *relationRequest
	r.enqueuedAt = time.Now().UTC().Truncate(time.Microsecond)
	e.pending = append(e.pending, r)
	m.settle(e, &notify)
	if !r.terminal {
		m.detectDeadlock(r, &notify)
	}
	m.retire(ref, e)
	m.mu.Unlock()
	notifyRequests(notify)
	return r, nil
}
func (m *Manager) retire(ref Resource, e *relationEntry) {
	if len(e.holders) == 0 && len(e.pending) == 0 && e.pins == 0 {
		delete(m.entries, ref)
		if ref.key != nil {
			m.retireUniqueKey(ref.key)
		}
	}
}

// Transaction tickets end through Engine.endLockOwner, which first retires
// engine-held exclusion state; only session tickets end here directly.
func (m *Manager) End(t *Ticket) {
	if t == nil {
		return
	}
	var notify *relationRequest
	m.mu.Lock()
	m.endLocked(t, ErrOwnerEnded, &notify)
	m.mu.Unlock()
	notifyRequests(notify)
}
func (m *Manager) endLocked(t *Ticket, err error, notify **relationRequest) {
	if !m.valid(t) {
		return
	}
	t.retireOwnership()
	if t.persistent {
		delete(m.sessions, t.session)
	} else {
		delete(m.owners, t.ref)
	}
	for ref := range t.resources {
		e := m.entries[ref]
		if e == nil {
			continue
		}
		delete(e.holders, t)
		for _, r := range e.pending {
			if (r.ticket == t || !t.persistent && r.provenance == t.ref) && !r.terminal {
				terminalRequest(r, err, notify)
			}
		}
		m.settle(e, notify)
		m.retire(ref, e)
	}
	clear(t.resources)
	m.retireMultiOwnerLocked(t)
	t.multiContributions = nil
	t.multiCache = nil
}

// Fence retires only replaced database owners, including their shared-role holds.
func (m *Manager) Fence(domain *ids.Domain, epoch ids.CatalogEpoch) {
	var notify *relationRequest
	m.mu.Lock()
	if state := m.domains[domain]; state != nil && epoch > state.epoch {
		state.epoch = epoch
		for ref, owner := range m.owners {
			if ref.Domain() == domain && ref.Epoch() < epoch {
				m.endLocked(owner, ids.ErrCatalogReplaced, &notify)
			}
		}
		m.retireMultiXactsLocked(domain)
		for ref, entry := range m.entries {
			if (ref.Kind == OrdinaryResource || ref.Kind == CatalogACLResource || ref.Kind == DefaultACLResource) && ref.Domain() == domain && ref.Epoch < epoch {
				for _, request := range entry.pending {
					if !request.terminal {
						terminalRequest(request, ids.ErrCatalogReplaced, &notify)
					}
				}
				delete(m.entries, ref)
				if ref.key != nil {
					m.retireUniqueKey(ref.key)
				}
			}
		}
	}
	m.mu.Unlock()
	notifyRequests(notify)
}

type CompletionPurpose uint8

const (
	CompletionVirtual CompletionPurpose = iota
	CompletionTransaction
)
