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

func (h *statementHost) RewriteQuerySQL(query sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	return h.scope().rewriteQuerySQL(query, text, result)
}

func (s *StatementScope) rewriteQuerySQL(query sqltype.Value, text string, result *sqltype.Descriptor) (value sqltype.Value, err error) {
	accumulator, err := sqltype.NewFTSRewriteRules(s.Op.Context(), query, result)
	if err != nil {
		return value, err
	}
	err = s.consumeTextSearchSQL(text, false, func(rows exec.RowCursor) error {
		var failure error
		value, failure = exec.ConsumeRewriteRules(s.Op.Context(), rows, accumulator)
		return failure
	})
	if err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}

// One lexical nested-SQL owner is shared by rewrite and statistics consumers.
// Consumers close the cursor before scratch restoration; FETCH errors pass through.
func (s *StatementScope) consumeTextSearchSQL(text string, readOnlySPI bool, consume func(exec.RowCursor) error) (err error) {
	if err := s.enterSQLRoutine(); err != nil {
		return err
	}
	defer s.leaveSQLRoutine()
	e := s.Engine()
	e.Mu.Lock()
	changes := s.Catalog
	if changes == nil {
		changes = s.Owner.Catalog
	}
	read := e.PrivateCatalogLocked(changes)
	e.Mu.Unlock()
	s.Op.EffectiveSettings().Mu.Lock()
	standard := s.Op.EffectiveSettings().Value("standard_conforming_strings") == "on"
	s.Op.EffectiveSettings().Mu.Unlock()
	statements, err := parser.ParseWithOptions(text, parser.Options{Context: s.Op.Context(), StandardConformingStrings: standard, Notice: s.Op.ParseNotice, ClosedRoutineFormals: true})
	if err != nil {
		return exec.RewriteSQLDiagnostic(err, text)
	}
	if len(statements) != 1 {
		// SPI prepares every statement before checking cursor plan count.
		for _, statement := range statements {
			if err := s.prepareNestedStatement(statement, read); err != nil {
				return exec.RewriteSQLDiagnostic(err, text)
			}
		}
		return SQLError("42P11", "cannot open multi-query plan as cursor")
	}
	statement := statements[0]
	tuple := nestedTupleStatement(statement)
	view, refs := s.View, s.rlsDataRefs
	restore := s.ActivateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	s.View, s.rlsDataRefs = view, refs
	s.sqlRoutine.scalar, s.sqlRoutine.nestedCursor = false, true
	// SPI rejects non-cursor command kinds before its read-only plan check.
	// Tuple-producing stat queries use canonical executable binding admission;
	// rewrite retains its independently established syntax-gate contract.
	s.sqlRoutine.readOnlySPI = readOnlySPI && tuple
	if err := s.AcquireRelation(statement, true); err != nil {
		return exec.RewriteSQLDiagnostic(err, text)
	}
	description, err := e.describeOperation(s, statement)
	if err != nil {
		return exec.RewriteSQLDiagnostic(err, text)
	}
	s.Expected = &description
	if !tuple {
		return SQLError("42P11", "cannot open "+RoutineCommandTag(statement)+" query as cursor")
	}
	if tag := nestedReadOnlyViolation(statement); tag != "" {
		return exec.RewriteSQLDiagnostic(SQLError("0A000", tag+" is not allowed in a non-volatile function"), text)
	}
	rowsResult, err := e.executeStatement(s, statement, 0, func(_ context.Context, info analyze.ParameterInfo, _ []sqltype.Value, _ sqltype.InputEnvironment) error {
		if info.Count != 0 {
			return fmt.Errorf("stuntgres: text search SQL parameter frame is not empty")
		}
		return nil
	})
	if err != nil {
		return exec.RewriteSQLDiagnostic(err, text)
	}
	defer func() {
		for _, rows := range rowsResult.RowSets {
			if closeErr := rows.Close(); err == nil {
				err = closeErr
			}
		}
	}()
	if rowsResult.Rows == nil {
		return fmt.Errorf("stuntgres: tuple-producing text search SQL returned no row cursor")
	}
	return consume(rowsResult.Rows)
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
