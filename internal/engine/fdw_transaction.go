package engine

import (
	"context"
	"errors"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// A live cursor's producer spans requests, but each FETCH/ReScan touches the
// current statement's participant mark. Do not cache an old request context or
// allocate another mark for each row in the same eager batch.
func (h *statementHost) TouchForeignScan(ctx context.Context, source *exec.ForeignQuerySource) error {
	s := h.scope()
	if _, ok := source.Wrapper.(catalog.ForeignTransactionProvider); !ok {
		return nil
	}
	r := source.Request
	key := foreignParticipantKey{r.HandlerOID, r.ServerOID, r.MappingOID, r.RoleOID}
	for _, p := range s.foreignTablesOrZero().statements {
		if p.Key == key {
			if p.Closed {
				return errors.New("stuntgres: foreign transaction is already disposed")
			}
			return nil
		}
	}
	_, err := s.enlistForeign(ctx, source.Wrapper, r, false)
	return err
}
func (s *StatementScope) foreignUnlocked() {
	if s.replacementRead {
		s.engine().replacementMu.RUnlock()
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
			return nil, ForeignCapabilityError()
		}
		return nil, nil
	}
	t := s.owner
	key := foreignParticipantKey{request.HandlerOID, request.ServerOID, request.MappingOID, request.RoleOID}
	t.Mu.Lock()
	if err := t.Validate(s.txRef); err != nil {
		t.Mu.Unlock()
		return nil, err
	}
	foreign := ownerExtensionOrZero(t).foreign
	participant := foreign.Participant(key)
	marks := t.Savepoints // Borrowed immutable roots under retained owner admission.
	t.Mu.Unlock()
	if participant == nil {
		capabilities, err := provider.ForeignTransactionCapabilities(ctx, exec.CopyForeignRequest(request))
		if err != nil {
			return nil, exec.ForeignCallbackError(err)
		}
		if !capabilities.StatementRollback || !capabilities.TransactionRollback {
			return nil, ForeignCapabilityError()
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
		participant = &foreignParticipant{Key: key, Tx: tx, Capabilities: capabilities}
		if capabilities.Savepoints {
			sp, ok := tx.(catalog.ForeignTransactionSavepoints)
			if !ok {
				_ = tx.Rollback(context.WithoutCancel(ctx))
				return nil, errors.New("stuntgres: foreign savepoint capability has no implementation")
			}
			for _, mark := range marks {
				if err := sp.Savepoint(ctx, ForeignMarkName(mark.Serial)); err != nil {
					_ = tx.Rollback(context.WithoutCancel(ctx))
					return nil, exec.ForeignCallbackError(err)
				}
			}
		}
		t.Mu.Lock()
		err = t.Validate(s.txRef)
		if err == nil {
			ownerExtensionOf(t).foreign.Enlist(participant)
		}
		t.Mu.Unlock()
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			return nil, err
		}
	}
	if participant.Closed {
		return nil, errors.New("stuntgres: foreign transaction is already disposed")
	}
	if len(marks) != 0 && !participant.Capabilities.Savepoints {
		return nil, sqlError("0A000", "foreign transaction does not support savepoints")
	}
	if !slices.Contains(s.foreignTablesOrZero().statements, participant) {
		// Record before entry: a partial BeginStatement must be rolled back.
		s.foreignTables().statements = append(s.foreignTablesOrZero().statements, participant)
		if err := participant.Tx.BeginStatement(ctx); err != nil {
			return nil, exec.ForeignCallbackError(err)
		}
	}
	return participant.Tx, nil
}

func (s *StatementScope) finishForeignModifications(primary error) error {
	if len(s.foreignTablesOrZero().modifications) != 0 {
		s.foreignUnlocked()
	}
	for _, modification := range s.foreignTablesOrZero().modifications {
		if err := modification.End(s.op.Context()); primary == nil {
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
		if p.Closed {
			continue
		}
		if err := p.Tx.EndStatement(ctx, primary == nil); err != nil {
			if primary == nil {
				primary = exec.ForeignCallbackError(err)
			}
			// A failed release is not a reusable transaction; abort it, once.
			p.Closed = true
			_ = p.Tx.Rollback(ctx)
		}
	}
	s.foreignTablesSlot.clearStatements()
	return primary
}

// commitForeign commits the owner's participants after validating ref.
func commitForeign(t *session.TransactionOwner, ctx context.Context, ref ids.TxRef) error {
	t.Mu.Lock()
	err := t.Validate(ref)
	foreign := ownerExtensionOrZero(t).foreign // Admission excludes enrollment/disposal.
	t.Mu.Unlock()
	if err != nil {
		return err
	}
	return foreign.Commit(ctx)
}
func foreignPending(t *session.TransactionOwner) bool {
	foreign := ownerExtensionOrZero(t).foreign
	return foreign.Pending()
}
func foreignSavepoint(t *session.TransactionOwner, ctx context.Context, kind int, id uint64) error {
	foreign := ownerExtensionOrZero(t).foreign
	return foreign.Savepoint(ctx, kind, id)
}
