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
	accumulator, err := sqltype.NewFTSRewriteRules(s.Op.Context(), query, result)
	if err != nil {
		return value, err
	}
	e := s.Engine()
	e.Mu.Lock()
	changes := s.Catalog
	if changes == nil {
		changes = s.Owner.Catalog
	}
	read := e.PrivateCatalogLocked(changes)
	e.Mu.Unlock()
	s.Op.Session.Settings.Mu.Lock()
	standard := s.Op.Session.Settings.Value("standard_conforming_strings") == "on"
	s.Op.Session.Settings.Mu.Unlock()
	statements, err := parser.ParseWithOptions(text, parser.Options{Context: s.Op.Context(), StandardConformingStrings: standard, Notice: s.Op.ParseNotice, ClosedRoutineFormals: true})
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
		return value, SQLError("42P11", "cannot open multi-query plan as cursor")
	}
	statement := statements[0]
	view, refs := s.View, s.rlsDataRefs
	restore := s.activateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	s.View, s.rlsDataRefs = view, refs
	s.sqlRoutine.scalar, s.sqlRoutine.rewrite = false, true
	if err := s.AcquireRelation(statement, true); err != nil {
		return value, exec.RewriteSQLDiagnostic(err, text)
	}
	description, err := e.describeOperation(s, statement)
	if err != nil {
		return value, exec.RewriteSQLDiagnostic(err, text)
	}
	s.Expected = &description
	if !rewriteTupleStatement(statement) {
		return value, SQLError("42P11", "cannot open "+RoutineCommandTag(statement)+" query as cursor")
	}
	if tag := rewriteReadOnlyViolation(statement); tag != "" {
		return value, exec.RewriteSQLDiagnostic(SQLError("0A000", tag+" is not allowed in a non-volatile function"), text)
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
	return exec.ConsumeRewriteRules(s.Op.Context(), rowsResult.Rows, accumulator)
}

func (s *StatementScope) prepareRewriteStatement(statement parser.Statement, read catalog.Read) (err error) {
	view, refs := s.View, s.rlsDataRefs
	restore := s.activateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	s.View, s.rlsDataRefs = view, refs
	s.sqlRoutine.scalar = false
	if err = s.AcquireRelation(statement, true); err != nil {
		return err
	}
	_, err = s.Engine().describeOperation(s, statement)
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
