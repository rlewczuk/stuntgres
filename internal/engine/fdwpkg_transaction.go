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

type foreignParticipantKey struct{ handler, server, mapping, role uint32 }

type foreignParticipant struct {
	key          foreignParticipantKey
	tx           catalog.ForeignTransaction
	capabilities catalog.ForeignTransactionCapabilities
	closed       bool // Commit has been attempted, or Rollback has disposed the handle.
}

func foreignCapabilityError() error {
	return sqlError("0A000", "foreign writes require statement and transaction rollback support")
}

func rollbackForeign(ctx context.Context, participants []*foreignParticipant) error {
	var first error
	for _, p := range participants {
		if p.closed {
			continue
		}
		p.closed = true
		if err := p.tx.Rollback(context.WithoutCancel(ctx)); first == nil {
			first = exec.ForeignCallbackError(err)
		}
	}
	return first
}

func foreignMarkName(id uint64) string { return fmt.Sprintf("stuntgres_sql_%d", id) }

// participant returns the enlisted participant for key, or nil.
func (f *foreignTransaction) participant(key foreignParticipantKey) *foreignParticipant {
	for _, p := range f.participants {
		if p.key == key {
			return p
		}
	}
	return nil
}

func (f *foreignTransaction) enlist(p *foreignParticipant) {
	f.participants = append(f.participants, p)
}

func (f *foreignTransaction) hasParticipants() bool { return len(f.participants) != 0 }

// retire moves the participants to cleanup.
func (f *foreignTransaction) retire() {
	f.cleanup = append(f.cleanup, f.participants...)
	f.participants = nil
}

// takeCleanup returns and clears the retired participants.
func (f *foreignTransaction) takeCleanup() []*foreignParticipant {
	cleanup := f.cleanup
	f.cleanup = nil
	return cleanup
}

// commit commits every open participant once. A failed Commit has an unknown
// outcome: it is never retried or compensated, and the rest roll back.
func (f *foreignTransaction) commit(ctx context.Context) error {
	for _, p := range f.participants {
		if p.closed {
			continue
		}
		p.closed = true
		if err := p.tx.Commit(ctx); err != nil {
			_ = rollbackForeign(ctx, f.participants)
			return exec.ForeignCallbackError(err)
		}
	}
	return nil
}

func (f *foreignTransaction) pending() bool {
	for _, p := range f.participants {
		if !p.closed {
			return true
		}
	}
	return false
}

// savepoint sets (kind 0), rolls back to (1) or releases (2) mark id in every
// participant; a failure rolls every participant back.
func (f *foreignTransaction) savepoint(ctx context.Context, kind int, id uint64) error {
	for _, p := range f.participants {
		if p.closed {
			return errors.New("stuntgres: foreign transaction is already disposed")
		}
		if !p.capabilities.Savepoints {
			return sqlError("0A000", "foreign transaction does not support savepoints")
		}
	}
	for _, p := range f.participants {
		sp := p.tx.(catalog.ForeignTransactionSavepoints)
		var err error
		switch kind {
		case 0:
			err = sp.Savepoint(ctx, foreignMarkName(id))
		case 1:
			err = sp.RollbackTo(context.WithoutCancel(ctx), foreignMarkName(id))
		case 2:
			err = sp.Release(ctx, foreignMarkName(id))
		}
		if err != nil {
			_ = rollbackForeign(ctx, f.participants)
			return exec.ForeignCallbackError(err)
		}
	}
	return nil
}
