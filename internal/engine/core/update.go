package core

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
	e := s.Engine()
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: ref, Row: row}, mode, policy)
	if request != nil {
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	if s.implicit && !s.ReplacementRead {
		e.ReplacementMu.RLock()
		s.ReplacementRead = true
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := s.ValidateLockedRelation(e); err != nil {
		return err
	}
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return nil
}

// executeUpdate runs the dml UPDATE executor over the scope's host.
func executeUpdate(ctx context.Context, s *StatementScope, b analyze.Binding, source, from *exec.Source, parameters []sqltype.Value, schema string) (Result, error) {
	outcome, err := dml.ExecuteUpdate(s.WriteHost(), dml.UpdateInput{Context: ctx, Binding: b, Source: source, From: from, Parameters: parameters, Schema: schema})
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
	e.Mu.Lock()
	err := s.ValidateLockedRelation(e)
	if err == nil && s.ForeignActionAuth != nil {
		err = s.validateForeignActionLocked(e, b.Ref)
	}
	var auth AuthorizationView
	riOwner := false
	if err == nil {
		auth, err = s.writeAuthorizationLocked(e)
		if err == nil && !suppressEmpty && s.ForeignActionAuth != nil {
			admission := s.foreignActionsOrZero().Admissions[b.Ref.Address]
			riOwner = s.foreignActionsOrZero().Settling && admission != nil && admission.Writable && auth.State.CurrentUser == admission.Relation.Owner && b.Relation.Owner == admission.Relation.Owner
		}
	}
	e.Mu.Unlock()
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
	return dml.RowDiagnosticDetail(auth.View, auth, b, row, s.Output, suppressEmpty)
}
