package engine

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
)

// foreignTransaction is the transaction owner's FDW state, held in the owner's
// root extension. Foreign savepoints are named after the owner's generic
// savepointSerial. Enrollment and disposal keep their existing admission and
// owner-lock rules.
type foreignTransaction struct {
	participants []*foreignParticipant
	cleanup      []*foreignParticipant
}

type foreignParticipantKey struct{ Handler, Server, Mapping, Role uint32 }

type foreignParticipant struct {
	Key          foreignParticipantKey
	Tx           catalog.ForeignTransaction
	Capabilities catalog.ForeignTransactionCapabilities
	Closed       bool // Commit has been attempted, or Rollback has disposed the handle.
}

func ForeignCapabilityError() error {
	return sqlError("0A000", "foreign writes require statement and transaction rollback support")
}

func RollbackForeign(ctx context.Context, participants []*foreignParticipant) error {
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

func ForeignMarkName(id uint64) string { return fmt.Sprintf("stuntgres_sql_%d", id) }

// participant returns the enlisted participant for key, or nil.
func (f *foreignTransaction) Participant(key foreignParticipantKey) *foreignParticipant {
	for _, p := range f.participants {
		if p.Key == key {
			return p
		}
	}
	return nil
}

func (f *foreignTransaction) Enlist(p *foreignParticipant) {
	f.participants = append(f.participants, p)
}

func (f *foreignTransaction) HasParticipants() bool { return len(f.participants) != 0 }

// retire moves the participants to cleanup.
func (f *foreignTransaction) Retire() {
	f.cleanup = append(f.cleanup, f.participants...)
	f.participants = nil
}

// takeCleanup returns and clears the retired participants.
func (f *foreignTransaction) TakeCleanup() []*foreignParticipant {
	cleanup := f.cleanup
	f.cleanup = nil
	return cleanup
}

// commit commits every open participant once. A failed Commit has an unknown
// outcome: it is never retried or compensated, and the rest roll back.
func (f *foreignTransaction) Commit(ctx context.Context) error {
	for _, p := range f.participants {
		if p.Closed {
			continue
		}
		p.Closed = true
		if err := p.Tx.Commit(ctx); err != nil {
			_ = RollbackForeign(ctx, f.participants)
			return exec.ForeignCallbackError(err)
		}
	}
	return nil
}

func (f *foreignTransaction) Pending() bool {
	for _, p := range f.participants {
		if !p.Closed {
			return true
		}
	}
	return false
}

// savepoint sets (kind 0), rolls back to (1) or releases (2) mark id in every
// participant; a failure rolls every participant back.
func (f *foreignTransaction) Savepoint(ctx context.Context, kind int, id uint64) error {
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
			err = sp.Savepoint(ctx, ForeignMarkName(id))
		case 1:
			err = sp.RollbackTo(context.WithoutCancel(ctx), ForeignMarkName(id))
		case 2:
			err = sp.Release(ctx, ForeignMarkName(id))
		}
		if err != nil {
			_ = RollbackForeign(ctx, f.participants)
			return exec.ForeignCallbackError(err)
		}
	}
	return nil
}
