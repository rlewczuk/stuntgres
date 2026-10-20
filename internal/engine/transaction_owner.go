package engine

import (
	"context"
	"errors"
	"sync"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

type TxState uint8

const (
	TxIdle TxState = iota
	TxImplicit
	TxActive
	TxAborted
)

type Isolation uint8

const (
	DefaultIsolation Isolation = iota
	ReadCommitted
	ReadUncommitted
	RepeatableRead
	Serializable
)

type TxOptions struct {
	Isolation     Isolation
	ReadOnly      bool
	Deferrable    bool
	DeferrableSet bool
}

type transactionOwner struct {
	ids.TxOwnerMarker
	admission       sync.Mutex
	copyLease       *StatementLease // the statement holding the COPY reservation
	copyDone        chan struct{}
	ticket          *lock.Ticket
	mu              sync.Mutex
	session         *Session
	state           TxState
	ref             ids.TxRef
	next            ids.TxID
	command         ids.CommandID
	writes          map[ids.ObjectAddress][]storage.RowVersion
	deletes         map[ids.ObjectAddress]*visibility.PrivateDeletes
	start           time.Time
	isolation       Isolation
	readOnly        bool
	deferrable      bool
	entryIsolation  Isolation
	entryReadOnly   bool
	entryDeferrable bool
	snapshotSet     bool
	frontier        ids.CommitSeq
	roots           map[ids.ObjectAddress]visibility.Root
	// Retained with the existing snapshot; consumed by pg_sequence and pg_sequences.
	sequenceCatalog     *catalog.State
	catalog             *catalogChanges
	savepoints          []savepointMark
	accessedTemporary   bool
	retainedMessage     bool           // Admission spans a direct multi-command message.
	protocolCycle       *ProtocolCycle // Retention only; never owns admission.
	identityEntry       authorization.RoleState
	identityCaptured    bool
	messageBorn         bool
	notificationActions []notificationAction
	notificationEvents  []Notification
	extension           session.OwnerSlot // Root extension; see ownerExtensionOf.
	savepointSerial     uint64            // Advanced by every SAVEPOINT, never reset; names foreign savepoints.
}

func (s *Session) TransactionState() TxState {
	t := s.transactions
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

func (t *transactionOwner) validate(ref ids.TxRef) error {
	if ref.Domain() != t.session.token.Domain() || ref.Session() != t.session.token || ref.ID() == 0 || ref.Attachment() != ids.TxOwner(t) {
		return errors.New("stuntgres: foreign or missing transaction reference")
	}
	if t.state == TxIdle || ref != t.ref {
		return errors.New("stuntgres: stale transaction reference")
	}
	return nil
}

// Admission remains held for ordinary statements. Only a reserved COPY drops
// the mutex across external I/O; every owner mutator waits on its logical lease.
func (t *transactionOwner) acquireAdmission(ctx context.Context, op *Operation) error {
	waited := false
	for {
		t.admission.Lock()
		if t.copyLease == nil {
			if waited && op != nil {
				if err := validateSessionOperation(t.session.token.Domain(), op); err != nil {
					t.admission.Unlock()
					return err
				}
			}
			return nil
		}
		done := t.copyDone
		t.admission.Unlock()
		select {
		case <-done:
			waited = true
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Caller holds the transaction mutex where required by existing gates.
func (t *transactionOwner) inBlock() bool {
	return t.state == TxActive || t.state == TxImplicit && t.retainsTransaction()
}
