package engine

import (
	"io"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// ExecuteProtocolSimple uses the canonical Execute unwrapping/binder. A plain
// SELECT transfers its initialized executor to the same live producer used by
// wire portals. Released RETURNING/modifying-CTE/utility store strategies keep
// their existing finalized rows and emit metadata when those rows are fetched.
// The caller still owns Finish, notices and CommandComplete.
func (s *StatementScope) ExecuteProtocolSimple(stmt parser.Statement, describe func([]catalog.Column) error, row func([]ProtocolField) error, source io.Reader, destination io.Writer, hooks CopyProtocolHooks) (result Result, err error) {
	s.protocolSimple = true
	s.protocolSimpleStreaming = true
	defer func() {
		s.protocolSimpleStreaming = false
		s.protocolBind = nil
		if p := s.protocolSimplePortal; p != nil {
			if closeErr := s.protocolTerminalIO(func() error { return p.close(s.op.Context()) }); err == nil {
				err = closeErr
			}
			s.protocolSimplePortal = nil
		}
	}()
	stream := protocolCopyExecution{source: source, destination: destination, hooks: hooks}
	result, err = s.execute(stmt, 0, nil, &stream)
	s.protocolBind = nil
	p := s.protocolSimplePortal
	if err != nil || p == nil {
		return result, err
	}
	// Planning/ExecutorStart succeeded and produced this actual schema. No
	// additional Describe, execution replay or synthetic error descriptor exists.
	if err = s.protocolSimpleIO(func() error { return describe(p.columns) }); err != nil {
		return result, err
	}
	execution, err := s.executeProtocolPortal(p, "", 0, row, nil, nil, CopyProtocolHooks{}, false)
	if err != nil {
		return Result{}, err
	}
	return Result{CommandTag: execution.CommandTag, Count: execution.Count}, nil
}

func protocolPlannedStatement(stmt parser.Statement) bool {
	if preparableBody(stmt) {
		return true
	}
	switch stmt.(type) {
	case *parser.DeclareCursor, *parser.CreateQueryTable:
		return true
	}
	return false
}

// Called only at actual portal creation: raw parsing and required planning have
// already succeeded. Marking a simple message never closes an execution owner.
func (s *StatementScope) beginProtocolSimplePortal() error {
	if !s.protocolSimple || s.protocolSimpleCreated {
		return nil
	}
	s.protocolSimpleCreated = true
	if s.op.session.preparedSQL.portals[""] == nil {
		return nil
	}
	// A non-retained transaction control gates without admitting an incarnation.
	// Its pre-execution disposal still borrows the current owner's exact fence;
	// it must not start a new transaction or relax copyIO's resume validation.
	if s.txRef == (ids.TxRef{}) {
		s.owner.mu.Lock()
		s.attachTransactionLocked()
		s.owner.mu.Unlock()
	}
	return s.CloseProtocolPortal("")
}

func (s *StatementScope) protocolSimpleIO(call func() error) error {
	if err := s.reserveCopy(); err != nil {
		return err
	}
	defer s.releaseCopyReservation()
	_, err := s.copyIO(func() (int, error) { return 0, call() })
	return err
}
