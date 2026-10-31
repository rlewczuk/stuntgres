package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// statementHost implements the nested-SQL capability (rule 4: dispatch
// re-entry is core's). Each nested command borrows the statement journal with
// fresh lexical command scratch (ActivateSQLRoutine), the canonical binder and
// the executor; ts_rewrite, ts_stat, crosstab and connectby reach it only
// through exec.NestedSQL.
var _ exec.NestedSQL = (*statementHost)(nil)

func (h *statementHost) EnterNestedSQL() error { return h.scope().enterSQLRoutine() }
func (h *statementHost) LeaveNestedSQL()       { h.scope().leaveSQLRoutine() }

func (h *statementHost) ParseNestedSQL(ctx context.Context, text string) (catalog.Read, []parser.Statement, error) {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	changes := s.Catalog
	if changes == nil {
		changes = s.Owner.Catalog
	}
	read := e.PrivateCatalogLocked(changes)
	e.Mu.Unlock()
	settings := s.Op.EffectiveSettings()
	settings.Mu.Lock()
	standard := settings.Value("standard_conforming_strings") == "on"
	settings.Mu.Unlock()
	statements, err := parser.ParseWithOptions(text, parser.Options{Context: ctx, StandardConformingStrings: standard, Notice: s.Op.ParseNotice, ClosedRoutineFormals: true})
	return read, statements, err
}

func (h *statementHost) PrepareNestedSQL(statement parser.Statement, read catalog.Read) error {
	return h.scope().prepareNestedStatement(statement, read)
}

func (h *statementHost) ExecuteNestedSQL(statement parser.Statement, read catalog.Read, request exec.NestedRequest, consumer exec.NestedConsumer) (err error) {
	s := h.scope()
	e := s.Engine()
	var restore func(error) error
	if request.BorrowSource {
		restore = s.activateNestedSource(read)
	} else {
		view, refs := s.View, s.rlsDataRefs
		restore = s.ActivateSQLRoutine(nil, read)
		s.View, s.rlsDataRefs = view, refs
	}
	defer func() { err = restore(err) }()
	s.sqlRoutine.scalar, s.sqlRoutine.nestedCursor, s.sqlRoutine.readOnlySPI = false, true, request.ReadOnly
	diagnose := func(err error) error {
		if request.Diagnose {
			return exec.RewriteSQLDiagnostic(err, request.Text)
		}
		return err
	}
	if stop, err := consumer.NestedCommand(statement); stop || err != nil {
		return err
	}
	if err := s.AcquireRelation(statement, true); err != nil {
		return diagnose(err)
	}
	description, err := e.describeOperation(s, statement)
	if err != nil {
		return diagnose(err)
	}
	s.Expected = &description
	if err := consumer.NestedAnalyzed(statement); err != nil {
		return err
	}
	result, err := e.executeStatement(s, statement, 0, func(_ context.Context, info analyze.ParameterInfo, _ []sqltype.Value, _ sqltype.InputEnvironment) error {
		if info.Count != 0 {
			return fmt.Errorf("stuntgres: %s SQL parameter frame is not empty", request.Caller)
		}
		return nil
	})
	if err != nil {
		if request.CloseFailed {
			if result.Rows != nil {
				result.Rows.Close()
			}
			for _, rows := range result.RowSets {
				rows.Close()
			}
		}
		return diagnose(err)
	}
	defer func() {
		for _, rows := range result.RowSets {
			if closeErr := rows.Close(); err == nil {
				err = closeErr
			}
		}
	}()
	return consumer.NestedRows(statement, result.Rows)
}

func (h *statementHost) NestedCursorStatement(statement parser.Statement) bool {
	return nestedTupleStatement(statement)
}

func (h *statementHost) NestedReadOnlyViolation(statement parser.Statement) string {
	return nestedReadOnlyViolation(statement)
}

func (h *statementHost) NestedCommandTag(statement parser.Statement) string {
	return RoutineCommandTag(statement)
}

// activateNestedSource borrows the saved outer frontier, not its fixed set
// of admitted source members. Activation's restoration owns the local View.
func (s *StatementScope) activateNestedSource(read catalog.Read) func(error) error {
	outer := s.View
	restore := s.ActivateSQLRoutine(nil, read)
	if outer != nil {
		s.sqlRoutine.sourceBase = outer.Load()
	}
	return restore
}

func (s *StatementScope) prepareNestedStatement(statement parser.Statement, read catalog.Read) (err error) {
	view, refs := s.View, s.rlsDataRefs
	restore := s.ActivateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	s.View, s.rlsDataRefs = view, refs
	s.sqlRoutine.scalar = false
	if err = s.AcquireRelation(statement, true); err != nil {
		return err
	}
	_, err = s.Engine().describeOperation(s, statement)
	return err
}

func nestedTupleStatement(statement parser.Statement) bool {
	switch s := statement.(type) {
	case *parser.Select:
		return s.Into == nil
	case *parser.Insert:
		return s.Returning != nil
	case *parser.Update:
		return s.Returning != nil
	case *parser.Delete:
		return s.Returning != nil
	case *parser.Merge:
		return s.Returning != nil
	}
	return false
}
func nestedReadOnlyViolation(statement parser.Statement) string {
	if parser.ModifyingStatement(statement) {
		return RoutineCommandTag(statement)
	}
	if parser.HasModifyingCTE(statement) {
		return "SELECT"
	}
	if s, ok := statement.(*parser.Select); ok && len(s.Locking) != 0 {
		return "SELECT " + s.Locking[0].Mode
	}
	return ""
}
