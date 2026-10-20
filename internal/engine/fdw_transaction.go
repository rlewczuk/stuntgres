package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

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

// A live cursor's producer spans requests, but each FETCH/ReScan touches the
// current statement's participant mark. Do not cache an old request context or
// allocate another mark for each row in the same eager batch.
func (s *StatementScope) touchForeignScan(ctx context.Context, source *foreignQuerySource) error {
	if _, ok := source.wrapper.(catalog.ForeignTransactionProvider); !ok {
		return nil
	}
	r := source.request
	key := foreignParticipantKey{r.HandlerOID, r.ServerOID, r.MappingOID, r.RoleOID}
	for _, p := range s.foreignTablesOrZero().statements {
		if p.key == key {
			if p.closed {
				return errors.New("stuntgres: foreign transaction is already disposed")
			}
			return nil
		}
	}
	_, err := s.enlistForeign(ctx, source.wrapper, r, false)
	return err
}
func (s *StatementScope) foreignUnlocked() {
	if s.replacementRead {
		s.op.session.Engine().replacementMu.RUnlock()
		s.replacementRead = false
	}
}

// Owner admission serializes this slice; mutexes protect only state capture and
// fences, never application I/O. No registry or context outlives this owner.
func (s *StatementScope) enlistForeign(ctx context.Context, wrapper catalog.ForeignDataWrapper, request catalog.ForeignRequest, writing bool) (catalog.ForeignTransaction, error) {
	s.foreignUnlocked()
	provider, ok := wrapper.(catalog.ForeignTransactionProvider)
	if !ok {
		if writing {
			return nil, foreignCapabilityError()
		}
		return nil, nil
	}
	t := s.owner
	key := foreignParticipantKey{request.HandlerOID, request.ServerOID, request.MappingOID, request.RoleOID}
	t.mu.Lock()
	if err := t.validate(s.txRef); err != nil {
		t.mu.Unlock()
		return nil, err
	}
	var participant *foreignParticipant
	for _, p := range t.foreignParticipants {
		if p.key == key {
			participant = p
			break
		}
	}
	marks := t.savepoints // Borrowed immutable roots under retained owner admission.
	t.mu.Unlock()
	if participant == nil {
		capabilities, err := provider.ForeignTransactionCapabilities(ctx, copyForeignRequest(request))
		if err != nil {
			return nil, foreignCallbackError(err)
		}
		if !capabilities.StatementRollback || !capabilities.TransactionRollback {
			return nil, foreignCapabilityError()
		}
		if len(marks) != 0 && !capabilities.Savepoints {
			return nil, sqlError("0A000", "foreign transaction does not support savepoints")
		}
		tx, err := provider.BeginForeignTransaction(ctx, copyForeignRequest(request))
		if err != nil {
			if tx != nil {
				_ = tx.Rollback(context.WithoutCancel(ctx))
			}
			return nil, foreignCallbackError(err)
		}
		if tx == nil {
			return nil, errors.New("stuntgres: foreign provider returned no transaction")
		}
		participant = &foreignParticipant{key: key, tx: tx, capabilities: capabilities}
		if capabilities.Savepoints {
			sp, ok := tx.(catalog.ForeignTransactionSavepoints)
			if !ok {
				_ = tx.Rollback(context.WithoutCancel(ctx))
				return nil, errors.New("stuntgres: foreign savepoint capability has no implementation")
			}
			for _, mark := range marks {
				if err := sp.Savepoint(ctx, foreignMarkName(mark.foreignID)); err != nil {
					_ = tx.Rollback(context.WithoutCancel(ctx))
					return nil, foreignCallbackError(err)
				}
			}
		}
		t.mu.Lock()
		err = t.validate(s.txRef)
		if err == nil {
			t.foreignParticipants = append(t.foreignParticipants, participant)
		}
		t.mu.Unlock()
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			return nil, err
		}
	}
	if participant.closed {
		return nil, errors.New("stuntgres: foreign transaction is already disposed")
	}
	if len(marks) != 0 && !participant.capabilities.Savepoints {
		return nil, sqlError("0A000", "foreign transaction does not support savepoints")
	}
	if !slices.Contains(s.foreignTablesOrZero().statements, participant) {
		// Record before entry: a partial BeginStatement must be rolled back.
		s.foreignTables().statements = append(s.foreignTablesOrZero().statements, participant)
		if err := participant.tx.BeginStatement(ctx); err != nil {
			return nil, foreignCallbackError(err)
		}
	}
	return participant.tx, nil
}

func (s *StatementScope) finishForeignModifications(primary error) error {
	if len(s.foreignTablesOrZero().modifications) != 0 {
		s.foreignUnlocked()
	}
	for _, modification := range s.foreignTablesOrZero().modifications {
		if err := modification.end(s.op.Context()); primary == nil {
			primary = err
		}
	}
	if s.foreignTablesSlot != nil {
		s.foreignTablesSlot.modifications = nil
	}
	return primary
}
func (s *StatementScope) finishForeignStatements(primary error) error {
	if len(s.foreignTablesOrZero().statements) != 0 {
		s.foreignUnlocked()
	}
	ctx := context.WithoutCancel(s.op.Context())
	for _, p := range s.foreignTablesOrZero().statements {
		if p.closed {
			continue
		}
		if err := p.tx.EndStatement(ctx, primary == nil); err != nil {
			if primary == nil {
				primary = foreignCallbackError(err)
			}
			// A failed release is not a reusable transaction; abort it, once.
			p.closed = true
			_ = p.tx.Rollback(ctx)
		}
	}
	s.foreignTablesSlot.clearStatements()
	return primary
}
func rollbackForeign(ctx context.Context, participants []*foreignParticipant) error {
	var first error
	for _, p := range participants {
		if p.closed {
			continue
		}
		p.closed = true
		if err := p.tx.Rollback(context.WithoutCancel(ctx)); first == nil {
			first = foreignCallbackError(err)
		}
	}
	return first
}
func (t *transactionOwner) commitForeign(ctx context.Context, ref ids.TxRef) error {
	t.mu.Lock()
	err := t.validate(ref)
	participants := t.foreignParticipants // Admission excludes enrollment/disposal.
	t.mu.Unlock()
	if err != nil {
		return err
	}
	for _, p := range participants {
		if p.closed {
			continue
		}
		// A failed Commit has unknown outcome, never retry or compensate it.
		p.closed = true
		if err := p.tx.Commit(ctx); err != nil {
			_ = rollbackForeign(ctx, participants)
			return foreignCallbackError(err)
		}
	}
	return nil
}
func (t *transactionOwner) foreignPending() bool {
	for _, p := range t.foreignParticipants {
		if !p.closed {
			return true
		}
	}
	return false
}
func foreignMarkName(id uint64) string { return fmt.Sprintf("stuntgres_sql_%d", id) }

func (t *transactionOwner) foreignSavepoint(ctx context.Context, kind int, id uint64) error {
	for _, p := range t.foreignParticipants {
		if p.closed {
			return errors.New("stuntgres: foreign transaction is already disposed")
		}
		if !p.capabilities.Savepoints {
			return sqlError("0A000", "foreign transaction does not support savepoints")
		}
	}
	for _, p := range t.foreignParticipants {
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
			_ = rollbackForeign(ctx, t.foreignParticipants)
			return foreignCallbackError(err)
		}
	}
	return nil
}
