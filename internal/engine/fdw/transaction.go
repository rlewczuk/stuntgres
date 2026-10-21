package fdw

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
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
