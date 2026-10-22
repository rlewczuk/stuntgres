package engine

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

// SQL waits never retain replacement admission or state/manager mutexes.
func (s *StatementScope) acquireUpdateRowMode(ref ids.ObjectRef, row ids.RowID, mode lock.Mode) error {
	e := s.engine()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref, Row: row}, mode, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	if s.implicit && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return err
	}
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return nil
}

// executeUpdate runs the dml UPDATE executor over the scope's host.
func executeUpdate(ctx context.Context, s *StatementScope, b analyze.Binding, source, from *exec.Source, parameters []sqltype.Value, schema string) (Result, error) {
	outcome, err := dml.ExecuteUpdate(s.writeHost(), dml.UpdateInput{Context: ctx, Binding: b, Source: source, From: from, Parameters: parameters, Schema: schema})
	if err != nil {
		return Result{}, err
	}
	return outcomeResult(outcome), nil
}

// Capture only immutable authority under metadata locks. Provider output runs
// unlocked; disclosure uses the named target's supplied-column manifest.
func (e *Engine) rowConstraintDetail(s *StatementScope, b *analyze.Binding, row []sqltype.Value) (string, error) {
	if s != nil && s.PartitionInsert != nil && b != nil && b.Relation.Address() == s.PartitionInsert.Target.Address() {
		row = s.PartitionInsert.NamedRow
	}
	return e.rowDiagnosticDetail(s, b, row, false)
}

func (e *Engine) rowDiagnosticDetail(s *StatementScope, b *analyze.Binding, row []sqltype.Value, suppressEmpty bool) (string, error) {
	e.mu.Lock()
	err := s.validateLockedRelation(e)
	if err == nil && s.foreignActionAuth != nil {
		err = s.validateForeignActionLocked(e, b.Ref)
	}
	var auth AuthorizationView
	riOwner := false
	if err == nil {
		auth, err = s.writeAuthorizationLocked(e)
		if err == nil && !suppressEmpty && s.foreignActionAuth != nil {
			admission := s.foreignActionsOrZero().Admissions[b.Ref.Address]
			riOwner = s.foreignActionsOrZero().Settling && admission != nil && admission.Writable && auth.State.CurrentUser == admission.Relation.Owner && b.Relation.Owner == admission.Relation.Owner
		}
	}
	e.mu.Unlock()
	if err != nil {
		return "", err
	}
	// Ordinary RLS eligibility withholds values even under table SELECT.
	// SPI-local RI constraints instead use the validated, writable child-owner
	// action context (including FORCE); a mere acting-authority marker is not
	// sufficient. This does not change after-event FK key disclosure.
	if !riOwner {
		applies, err := access.RowSecurityApplies(auth.Roles, b.Relation, auth.State.CurrentUser)
		if err != nil || applies {
			return "", err
		}
	}
	return dml.RowDiagnosticDetail(auth.View, auth, b, row, s.output, suppressEmpty)
}
