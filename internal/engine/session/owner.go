package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
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

type TransactionOwner struct {
	ids.TxOwnerMarker
	Admission       sync.Mutex
	CopyLease       *StatementLease // the statement holding the COPY reservation
	CopyDone        chan struct{}
	Ticket          *lock.Ticket
	Mu              sync.Mutex
	Session         *Session
	State           TxState
	Ref             ids.TxRef
	Next            ids.TxID
	Command         ids.CommandID
	Writes          map[ids.ObjectAddress][]storage.RowVersion
	Deletes         map[ids.ObjectAddress]*visibility.PrivateDeletes
	Start           time.Time
	Isolation       Isolation
	ReadOnly        bool
	Deferrable      bool
	EntryIsolation  Isolation
	EntryReadOnly   bool
	EntryDeferrable bool
	SnapshotSet     bool
	Frontier        ids.CommitSeq
	Roots           map[ids.ObjectAddress]visibility.Root
	// Retained with the existing snapshot; consumed by pg_sequence and pg_sequences.
	SequenceCatalog     *catalog.State
	Catalog             *Journal
	Savepoints          []SavepointMark
	AccessedTemporary   bool
	RetainedMessage     bool           // Admission spans a direct multi-command message.
	ProtocolCycle       *ProtocolCycle // Retention only; never owns admission.
	IdentityEntry       authorization.RoleState
	IdentityCaptured    bool
	MessageBorn         bool
	NotificationActions []NotificationAction
	NotificationEvents  []Notification
	Extension           OwnerSlot // Root extension; see ownerExtensionOf.
	SavepointSerial     uint64    // Advanced by every SAVEPOINT, never reset; names foreign savepoints.
}

func (s *Session) TransactionState() TxState {
	t := s.Transactions
	t.Mu.Lock()
	defer t.Mu.Unlock()
	return t.State
}

func (t *TransactionOwner) Validate(ref ids.TxRef) error {
	if ref.Domain() != t.Session.SessionToken.Domain() || ref.Session() != t.Session.SessionToken || ref.ID() == 0 || ref.Attachment() != ids.TxOwner(t) {
		return errors.New("stuntgres: foreign or missing transaction reference")
	}
	if t.State == TxIdle || ref != t.Ref {
		return errors.New("stuntgres: stale transaction reference")
	}
	return nil
}

// Admission remains held for ordinary statements. Only a reserved COPY drops
// the mutex across external I/O; every owner mutator waits on its logical lease.
func (t *TransactionOwner) AcquireAdmission(ctx context.Context, op *Operation) error {
	waited := false
	for {
		t.Admission.Lock()
		if t.CopyLease == nil {
			if waited && op != nil {
				if err := ValidateOperation(t.Session.SessionToken.Domain(), op); err != nil {
					t.Admission.Unlock()
					return err
				}
			}
			return nil
		}
		done := t.CopyDone
		t.Admission.Unlock()
		select {
		case <-done:
			waited = true
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Caller holds the transaction mutex where required by existing gates.
func (t *TransactionOwner) InBlock() bool {
	return t.State == TxActive || t.State == TxImplicit && t.RetainsTransaction()
}
