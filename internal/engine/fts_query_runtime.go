package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) RewriteQuerySQL(query sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	return h.scope().rewriteQuerySQL(query, text, result)
}

func (s *StatementScope) rewriteQuerySQL(query sqltype.Value, text string, result *sqltype.Descriptor) (value sqltype.Value, err error) {
	if err := s.enterSQLRoutine(); err != nil {
		return value, err
	}
	defer s.leaveSQLRoutine()
	accumulator, err := sqltype.NewFTSRewriteRules(s.op.Context(), query, result)
	if err != nil {
		return value, err
	}
	e := s.engine()
	e.mu.Lock()
	changes := s.catalog
	if changes == nil {
		changes = s.owner.Catalog
	}
	read := e.privateCatalogLocked(changes)
	e.mu.Unlock()
	s.op.Session.Settings.Mu.Lock()
	standard := s.op.Session.Settings.Value("standard_conforming_strings") == "on"
	s.op.Session.Settings.Mu.Unlock()
	statements, err := parser.ParseWithOptions(text, parser.Options{Context: s.op.Context(), StandardConformingStrings: standard, Notice: s.op.ParseNotice, ClosedRoutineFormals: true})
	if err != nil {
		return value, exec.RewriteSQLDiagnostic(err, text)
	}
	if len(statements) != 1 {
		// SPI prepares every statement before checking cursor plan count.
		for _, statement := range statements {
			if err := s.prepareRewriteStatement(statement, read); err != nil {
				return value, exec.RewriteSQLDiagnostic(err, text)
			}
		}
		return value, sqlError("42P11", "cannot open multi-query plan as cursor")
	}
	statement := statements[0]
	view, refs := s.view, s.rlsDataRefs
	restore := s.activateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	s.view, s.rlsDataRefs = view, refs
	s.sqlRoutine.scalar, s.sqlRoutine.rewrite = false, true
	if err := s.acquireRelation(statement, true); err != nil {
		return value, exec.RewriteSQLDiagnostic(err, text)
	}
	description, err := e.describeOperation(s, statement)
	if err != nil {
		return value, exec.RewriteSQLDiagnostic(err, text)
	}
	s.Expected = &description
	if !rewriteTupleStatement(statement) {
		return value, sqlError("42P11", "cannot open "+routineCommandTag(statement)+" query as cursor")
	}
	if tag := rewriteReadOnlyViolation(statement); tag != "" {
		return value, exec.RewriteSQLDiagnostic(sqlError("0A000", tag+" is not allowed in a non-volatile function"), text)
	}
	rowsResult, err := e.executeStatement(s, statement, 0, func(_ context.Context, info analyze.ParameterInfo, _ []sqltype.Value, _ sqltype.InputEnvironment) error {
		if info.Count != 0 {
			return fmt.Errorf("stuntgres: rewrite SQL parameter frame is not empty")
		}
		return nil
	})
	if err != nil {
		return value, exec.RewriteSQLDiagnostic(err, text)
	}
	defer func() {
		for _, rows := range rowsResult.RowSets {
			if closeErr := rows.Close(); err == nil {
				err = closeErr
			}
		}
	}()
	if rowsResult.Rows == nil {
		return value, fmt.Errorf("stuntgres: tuple-producing rewrite SQL returned no row cursor")
	}
	return exec.ConsumeRewriteRules(s.op.Context(), rowsResult.Rows, accumulator)
}

func (s *StatementScope) prepareRewriteStatement(statement parser.Statement, read catalog.Read) (err error) {
	view, refs := s.view, s.rlsDataRefs
	restore := s.activateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	s.view, s.rlsDataRefs = view, refs
	s.sqlRoutine.scalar = false
	if err = s.acquireRelation(statement, true); err != nil {
		return err
	}
	_, err = s.engine().describeOperation(s, statement)
	return err
}

func rewriteTupleStatement(statement parser.Statement) bool {
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
func rewriteReadOnlyViolation(statement parser.Statement) string {
	if parser.ModifyingStatement(statement) {
		return routineCommandTag(statement)
	}
	if parser.HasModifyingCTE(statement) {
		return "SELECT"
	}
	if s, ok := statement.(*parser.Select); ok && len(s.Locking) != 0 {
		return "SELECT " + s.Locking[0].Mode
	}
	return ""
}
