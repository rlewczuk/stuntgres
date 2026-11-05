package core

import (
	"context"
	"errors"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func (s *StatementScope) foreignCharacteristics() (*catalog.ForeignTransactionCharacteristics, error) {
	t := s.Owner
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if err := t.Validate(s.TxRef); err != nil {
		return nil, err
	}
	isolation := "read committed"
	switch t.Isolation {
	case session.ReadUncommitted:
		isolation = "read uncommitted"
	case session.RepeatableRead:
		isolation = "repeatable read"
	case session.Serializable:
		isolation = "serializable"
	}
	return &catalog.ForeignTransactionCharacteristics{Isolation: isolation, ReadOnly: t.ReadOnly, Deferrable: t.Deferrable}, nil
}

func (h *statementHost) ForeignCharacteristics() (*catalog.ForeignTransactionCharacteristics, error) {
	return h.scope().foreignCharacteristics()
}

func foreignParticipantKey(handler *catalog.ForeignProcedure, r catalog.ForeignRequest) fdw.ParticipantKey {
	if extension.StockPostgresFDW.HandlerToken(handler) {
		return fdw.ParticipantKey{Server: r.ServerOID, Mapping: r.MappingOID}
	}
	return fdw.ParticipantKey{Handler: r.HandlerOID, Server: r.ServerOID, Mapping: r.MappingOID, Role: r.RoleOID}
}

// A live producer spans requests; each current FETCH/ReScan touches its
// participant without retaining an old context or allocating per-row marks.
func (h *statementHost) TouchForeignScan(ctx context.Context, source *exec.ForeignQuerySource) error {
	s := h.scope()
	if _, ok := source.Wrapper.(catalog.ForeignTransactionProvider); !ok {
		return nil
	}
	if f := s.foreignTablesSlot; f != nil {
		for _, admitted := range f.admissions {
			if admitted.source != source || admitted.role != source.Request.RoleOID {
				continue
			}
			if admitted.participant.Closed {
				return errors.New("stuntgres: foreign transaction is already disposed")
			}
			if slices.Contains(f.statements, admitted.participant) {
				s.foreignUnlocked()
				return nil
			}
			break
		}
	}
	r := source.Request
	_, err := s.enlistForeign(ctx, source, &r, false)
	return err
}
func (s *StatementScope) foreignUnlocked() {
	if s.ReplacementRead {
		s.Engine().ReplacementMu.RUnlock()
		s.ReplacementRead = false
	}
}

// Owner admission serializes this slice; mutexes protect only state capture and
// fences, never application I/O. No registry or context outlives this owner.
func (s *StatementScope) enlistForeign(ctx context.Context, source *exec.ForeignQuerySource, request *catalog.ForeignRequest, writing bool) (catalog.ForeignTransaction, error) {
	s.foreignUnlocked()
	if err := s.externalSQLCapability(source.Handler, request); err != nil {
		return nil, err
	}
	wrapper := source.Wrapper
	ctx = catalog.ExternalSQLExecutionContext(ctx, request.ExternalSQL)
	characteristics, err := s.foreignCharacteristics()
	if err != nil {
		return nil, err
	}
	request.Characteristics = characteristics
	provider, ok := wrapper.(catalog.ForeignTransactionProvider)
	if !ok {
		if writing {
			return nil, fdw.CapabilityError()
		}
		return nil, nil
	}
	t := s.Owner
	key := foreignParticipantKey(source.Handler, *request)
	t.Mu.Lock()
	if err := t.Validate(s.TxRef); err != nil {
		t.Mu.Unlock()
		return nil, err
	}
	foreign := ownerExtensionOrZero(t).foreign
	participant := foreign.Participant(key)
	marks := t.Savepoints // Borrowed immutable roots under retained owner admission.
	t.Mu.Unlock()
	if participant == nil {
		var err error
		participant, err = fdw.BeginParticipant(ctx, provider, key, *request, marks)
		if err != nil {
			return nil, err
		}
		t.Mu.Lock()
		err = t.Validate(s.TxRef)
		if err == nil {
			ownerExtensionOf(t).foreign.Enlist(participant)
		}
		t.Mu.Unlock()
		if err != nil {
			_ = participant.Tx.Rollback(context.WithoutCancel(ctx))
			return nil, err
		}
	}
	if participant.Closed {
		return nil, errors.New("stuntgres: foreign transaction is already disposed")
	}
	if len(marks) != 0 && !participant.Capabilities.Savepoints {
		return nil, SQLError("0A000", "foreign transaction does not support savepoints")
	}
	if !slices.Contains(s.foreignTablesOrZero().statements, participant) {
		// Record before entry: a partial BeginStatement must be rolled back.
		s.foreignTables().statements = append(s.foreignTablesOrZero().statements, participant)
		if err := participant.Tx.BeginStatement(ctx); err != nil {
			return nil, exec.ForeignCallbackError(err)
		}
	}
	f := s.foreignTables()
	for i, admitted := range f.admissions {
		if admitted.source == source && admitted.role == request.RoleOID {
			f.admissions[i].participant = participant
			return participant.Tx, nil
		}
	}
	f.admissions = append(f.admissions, foreignSourceAdmission{source: source, role: request.RoleOID, participant: participant})
	return participant.Tx, nil
}

func (s *StatementScope) finishForeignModifications(primary error) error {
	if len(s.foreignTablesOrZero().modifications) != 0 {
		s.foreignUnlocked()
	}
	for _, modification := range s.foreignTablesOrZero().modifications {
		if err := modification.End(s.Op.Context()); primary == nil {
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
	primary = fdw.EndStatements(s.Op.Context(), s.foreignTablesOrZero().statements, primary)
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
