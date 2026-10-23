package fdw

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
)

// Transaction is the transaction owner's FDW state, held in the owner's
// root extension. Foreign savepoints are named after the owner's generic
// savepointSerial. Enrollment and disposal keep their existing admission and
// owner-lock rules.
type Transaction struct {
	participants []*Participant
	cleanup      []*Participant
}

type ParticipantKey struct{ Handler, Server, Mapping, Role uint32 }

type Participant struct {
	Key          ParticipantKey
	Tx           catalog.ForeignTransaction
	Capabilities catalog.ForeignTransactionCapabilities
	Closed       bool // Commit has been attempted, or Rollback has disposed the handle.
}

func CapabilityError() error {
	return sqlError("0A000", "foreign writes require statement and transaction rollback support")
}

func Rollback(ctx context.Context, participants []*Participant) error {
	var first error
	for _, p := range participants {
		if p.Closed {
			continue
		}
		p.Closed = true
		if err := p.Tx.Rollback(context.WithoutCancel(ctx)); first == nil {
			first = exec.ForeignCallbackError(err)
		}
	}
	return first
}

func MarkName(id uint64) string { return fmt.Sprintf("stuntgres_sql_%d", id) }

// BeginParticipant runs a provider's handshake for a new participant: it
// checks the capabilities, begins the transaction and replays the owner's
// open savepoints marks. A failure after Begin returned a transaction rolls it
// back. The caller holds no lock and enlists the participant after
// revalidating the owner.
func BeginParticipant(ctx context.Context, provider catalog.ForeignTransactionProvider, key ParticipantKey, request catalog.ForeignRequest, marks []session.SavepointMark) (*Participant, error) {
	capabilities, err := provider.ForeignTransactionCapabilities(ctx, exec.CopyForeignRequest(request))
	if err != nil {
		return nil, exec.ForeignCallbackError(err)
	}
	if !capabilities.StatementRollback || !capabilities.TransactionRollback {
		return nil, CapabilityError()
	}
	if len(marks) != 0 && !capabilities.Savepoints {
		return nil, sqlError("0A000", "foreign transaction does not support savepoints")
	}
	tx, err := provider.BeginForeignTransaction(ctx, exec.CopyForeignRequest(request))
	if err != nil {
		if tx != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
		return nil, exec.ForeignCallbackError(err)
	}
	if tx == nil {
		return nil, errors.New("stuntgres: foreign provider returned no transaction")
	}
	if capabilities.Savepoints {
		sp, ok := tx.(catalog.ForeignTransactionSavepoints)
		if !ok {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			return nil, errors.New("stuntgres: foreign savepoint capability has no implementation")
		}
		for _, mark := range marks {
			if err := sp.Savepoint(ctx, MarkName(mark.Serial)); err != nil {
				_ = tx.Rollback(context.WithoutCancel(ctx))
				return nil, exec.ForeignCallbackError(err)
			}
		}
	}
	return &Participant{Key: key, Tx: tx, Capabilities: capabilities}, nil
}

// EndStatements ends the statement in each open participant, reporting
// success while primary is nil. A failed release is not a reusable
// transaction: that participant closes and rolls back, once, and its error
// becomes primary when primary is nil. Cancellation of ctx is ignored.
func EndStatements(ctx context.Context, participants []*Participant, primary error) error {
	ctx = context.WithoutCancel(ctx)
	for _, p := range participants {
		if p.Closed {
			continue
		}
		if err := p.Tx.EndStatement(ctx, primary == nil); err != nil {
			if primary == nil {
				primary = exec.ForeignCallbackError(err)
			}
			p.Closed = true
			_ = p.Tx.Rollback(ctx)
		}
	}
	return primary
}

// Participant returns the enlisted participant for key, or nil.
func (f *Transaction) Participant(key ParticipantKey) *Participant {
	for _, p := range f.participants {
		if p.Key == key {
			return p
		}
	}
	return nil
}

func (f *Transaction) Enlist(p *Participant) {
	f.participants = append(f.participants, p)
}

func (f *Transaction) HasParticipants() bool { return len(f.participants) != 0 }

// Retire moves the participants to cleanup.
func (f *Transaction) Retire() {
	f.cleanup = append(f.cleanup, f.participants...)
	f.participants = nil
}

// TakeCleanup returns and clears the retired participants.
func (f *Transaction) TakeCleanup() []*Participant {
	cleanup := f.cleanup
	f.cleanup = nil
	return cleanup
}

// Commit commits every open participant once. A failed Commit has an unknown
// outcome: it is never retried or compensated, and the rest roll back.
func (f *Transaction) Commit(ctx context.Context) error {
	for _, p := range f.participants {
		if p.Closed {
			continue
		}
		p.Closed = true
		if err := p.Tx.Commit(ctx); err != nil {
			_ = Rollback(ctx, f.participants)
			return exec.ForeignCallbackError(err)
		}
	}
	return nil
}

func (f *Transaction) Pending() bool {
	for _, p := range f.participants {
		if !p.Closed {
			return true
		}
	}
	return false
}

// Savepoint sets (kind 0), rolls back to (1) or releases (2) mark id in every
// participant; a failure rolls every participant back.
func (f *Transaction) Savepoint(ctx context.Context, kind int, id uint64) error {
	for _, p := range f.participants {
		if p.Closed {
			return errors.New("stuntgres: foreign transaction is already disposed")
		}
		if !p.Capabilities.Savepoints {
			return sqlError("0A000", "foreign transaction does not support savepoints")
		}
	}
	for _, p := range f.participants {
		sp := p.Tx.(catalog.ForeignTransactionSavepoints)
		var err error
		switch kind {
		case 0:
			err = sp.Savepoint(ctx, MarkName(id))
		case 1:
			err = sp.RollbackTo(context.WithoutCancel(ctx), MarkName(id))
		case 2:
			err = sp.Release(ctx, MarkName(id))
		}
		if err != nil {
			_ = Rollback(ctx, f.participants)
			return exec.ForeignCallbackError(err)
		}
	}
	return nil
}
