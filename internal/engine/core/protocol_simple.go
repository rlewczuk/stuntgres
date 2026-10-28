package core

import (
	"errors"
	"io"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// ExecuteProtocolSimple uses the canonical Execute unwrapping/binder. A plain
// SELECT transfers its initialized executor to the same live producer used by
// wire portals. Released RETURNING/modifying-CTE/utility store strategies keep
// their existing finalized rows and emit metadata when those rows are fetched.
// It consumes/closes all result rows before returning; the caller owns Finish,
// notices and CommandComplete, never a post-Finish presentation owner.
func (s *StatementScope) ExecuteProtocolSimple(stmt parser.Statement, describe func([]catalog.Column) error, row func([]session.ProtocolField) error, source io.Reader, destination io.Writer, hooks CopyProtocolHooks) (result Result, err error) {
	s.protocol().simple = true
	s.protocol().simpleStreaming = true
	defer func() {
		s.protocol().simpleStreaming = false
		s.protocol().bind = nil
		if p := s.protocolOrZero().simplePortal; p != nil {
			if closeErr := s.protocolTerminalIO(func() error { return p.close(s.Op.Context()) }); err == nil {
				err = closeErr
			}
			s.protocol().simplePortal = nil
		}
	}()
	stream := protocolCopyExecution{source: source, destination: destination, hooks: hooks}
	result, err = s.execute(stmt, 0, nil, &stream)
	s.protocol().bind = nil
	p := s.protocolOrZero().simplePortal
	if err != nil {
		if result.Rows != nil {
			rows := result.Rows
			result.Rows = nil
			_ = s.protocolTerminalIO(rows.Close)
		}
		return result, err
	}
	if p == nil {
		if result.Rows == nil {
			return result, nil
		}
		rows := result.Rows
		result.Rows = nil
		defer func() {
			if closeErr := s.protocolTerminalIO(rows.Close); err == nil {
				err = closeErr
			}
		}()
		if err = s.protocolSimpleIO(func() error { return describe(rows.Schema()) }); err != nil {
			return result, err
		}
		values := make([]sqltype.Value, len(rows.Schema()))
		var fields []session.ProtocolField
		var scratch []byte
		for {
			present, nextErr := rows.Next(s.Op.Context(), values)
			if nextErr != nil {
				return result, nextErr
			}
			if !present {
				return result, nil
			}
			env := rows.OutputEnvironment()
			env.Transport = s.Op.EffectiveSettings().ClientEncoding()
			env.XMLNotice = func(d sqlstate.Error) { s.Op.EmitNotice(session.LevelWarning, d) }
			fields, scratch, err = session.EncodeProtocolRow(s.Op.Context(), values, nil, env, fields, scratch)
			if err != nil {
				return result, err
			}
			if err = s.protocolSimpleIO(func() error { return row(fields) }); err != nil {
				return result, err
			}
		}
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

// Called only at actual portal creation: raw parsing and required planning have
// already succeeded. Marking a simple message never closes an execution owner.
func (s *StatementScope) beginProtocolSimplePortal() error {
	if !s.protocolOrZero().simple || s.protocolOrZero().simpleCreated {
		return nil
	}
	s.protocol().simpleCreated = true
	if protocolPortalOf(PayloadOf(s.Op.Session).PreparedSQL.Binding("")) == nil {
		return nil
	}
	// A non-retained transaction control gates without admitting an incarnation.
	// Its pre-execution disposal still borrows the current owner's exact fence;
	// it must not start a new transaction or relax copyIO's resume validation.
	if s.TxRef == (ids.TxRef{}) {
		s.Owner.Mu.Lock()
		s.attachTransactionLocked()
		s.Owner.Mu.Unlock()
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

// ProtocolNotices submits fallible active-command notices before Finish. The
// existing reservation releases admission and replacement fences during I/O,
// then revalidates the exact owner before statement publication can proceed.
func (s *StatementScope) ProtocolNotices(write func() error) error {
	s.Owner.Mu.Lock()
	s.attachTransactionLocked()
	s.Owner.Mu.Unlock()
	if s.TxRef != (ids.TxRef{}) {
		if s.Owner.CopyLease == &s.lease {
			// COPY retains its reservation through Finish; notices borrow
			// that exact active owner rather than reserving it twice.
			_, err := s.copyIO(func() (int, error) { return 0, write() })
			return err
		}
		return s.protocolSimpleIO(write)
	}
	// An idle transaction-control warning has no transaction incarnation.
	// Reserve its actual idle owner without manufacturing a transaction or
	// relaxing COPY's transaction-epoch resume check.
	if err := s.reserveCopy(); err != nil {
		return err
	}
	defer s.releaseCopyReservation()
	e := s.Engine()
	epoch := e.CatalogRead().Epoch()
	if err := s.protocolTerminalIO(write); err != nil {
		return err
	}
	if err := s.Active(); err != nil {
		return err
	}
	s.Owner.Mu.Lock()
	defer s.Owner.Mu.Unlock()
	if s.Owner.Ref != s.TxRef || s.Owner.Command != s.CommandID {
		return errors.New("stuntgres: stale notice owner")
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if epoch != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return nil
}
