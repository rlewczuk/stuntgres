package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// validateTypeIOCallback proves the selected original type/helper association
// against its captured read, then the same generation/current helper against the
// actual operation. A numeric type/procedure OID is never a codec capability.
func validateTypeIOCallback(view catalog.Read, callback catalog.TypeIOCallback) error {
	if callback.Type == nil || callback.Helper == nil || callback.Read.Owner() != view.Owner() || callback.Read.Domain() != view.Domain() || callback.Read.Epoch() != view.Epoch() {
		return ids.ErrCatalogReplaced
	}
	original, err := catalog.CaptureTypeIO(callback.Read, callback.Type.SQLType, callback.Direction)
	if err != nil {
		return err
	}
	if original.Type != callback.Type || original.Helper != callback.Helper {
		return fmt.Errorf("stuntgres: foreign installed type I/O callback")
	}
	current, err := catalog.CaptureTypeIO(view, callback.Type.SQLType, callback.Direction)
	if err != nil {
		return err
	}
	if current.Type != callback.Type || current.Helper != callback.Helper {
		return ids.ErrCatalogReplaced
	}
	return nil
}

// EnterTypeIOCallback is a direct fmgr callback, without a fictional SQL EXECUTE
// check. The real kernel executes only after Mu is released and its lexical
// token is left by the immediate codec owner on success or error.
func (h *statementHost) EnterTypeIOCallback(callback catalog.TypeIOCallback) (sqltype.RoutineBodyToken, error) {
	s := h.scope()
	if err := s.Active(); err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	e := s.Engine()
	if e.Sepgsql == nil || e.ImportTargets != nil {
		return sqltype.RoutineBodyToken{}, nil
	}
	mark := s.MarkSepgsqlObservations()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err == nil {
		err = validateTypeIOCallback(auth.Catalog, callback)
	}
	target, transition := "", false
	if err == nil {
		target, transition, err = s.sepgsqlBodyDecision(auth, callback.Helper, false, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		s.RestoreSepgsqlObservations(mark)
		return sqltype.RoutineBodyToken{}, err
	}
	if !transition {
		return sqltype.RoutineBodyToken{}, nil
	}
	return s.Op.Session.Sepgsql.Enter(target), nil
}

// plValueText is a reached PL output-function operation. Its borrowed callback
// is attached only while formatting this value, never to s.Output or a retained
// PL program. Ordinary scalar output remains the allocation-free inactive path.
func (s *StatementScope) plValueText(value sqltype.Value) (string, error) {
	output := s.Output
	e := s.Engine()
	if e.Sepgsql == nil || value.IsNull() || !sqltype.ContainsInstalledScalarIO(value.Type()) {
		return value.Type().OutputWithEnvironment(s.executionContext(), value, output)
	}
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	e.Mu.Unlock()
	if err != nil {
		return "", err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.executionContext(), Host: HostOf(s), Catalog: auth.Catalog}}
	output.TypeIO = &evaluation
	return value.Type().OutputWithEnvironment(s.executionContext(), value, output)
}

// validateCodecOutput is the last admission before exposing an encoded datum
// to a protocol/COPY sink. Detached codec work may have observed a predecessor
// which a peer replaced. No lock is held across the following external I/O.
func (s *StatementScope) validateCodecOutput() error {
	e := s.Engine()
	if e.Sepgsql == nil {
		return nil
	}
	if err := s.Active(); err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if _, err := s.RoutineAuthorizationLocked(); err != nil {
		return err
	}
	return s.validateSepgsqlObservations(e.Catalog.State)
}
