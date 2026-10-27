package core

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) ValidateTablefuncCrosstab(n plan.InstalledTablefuncCrosstabExpr) error {
	s, e := h.scope(), h.scope().Engine()
	if err := s.Active(); err != nil {
		return err
	}
	e.Mu.Lock()
	auth, err := s.writeAuthorizationLocked(e)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	current, ok := auth.Catalog.Procedure(n.Ref.Address)
	if !ok || n.RoutineReference != n.Procedure || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return fmt.Errorf("stuntgres: stale installed crosstab declaration")
	}
	n.Procedure, n.RoutineReference = current, current
	return stored.ValidateInstalledTablefuncCrosstab(n, auth.Catalog)
}

func (h *statementHost) TablefuncCrosstab(text string, result *sqltype.Descriptor) (values []sqltype.Value, err error) {
	s := h.scope()
	if err := s.enterSQLRoutine(); err != nil {
		return nil, err
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
	settings := s.Op.EffectiveSettings()
	settings.Mu.Lock()
	standard := settings.Value("standard_conforming_strings") == "on"
	settings.Mu.Unlock()
	statements, err := parser.ParseWithOptions(text, parser.Options{Context: s.Op.Context(), StandardConformingStrings: standard, Notice: s.Op.ParseNotice, ClosedRoutineFormals: true})
	if err != nil {
		return nil, exec.RewriteSQLDiagnostic(err, text)
	}
	// SPI prepares the entire string before executing any of its plans.
	for _, statement := range statements {
		if err := s.prepareRewriteStatement(statement, read); err != nil {
			return nil, exec.RewriteSQLDiagnostic(err, text)
		}
	}
	for i, statement := range statements {
		values, err = h.tablefuncCrosstabStatement(statement, read, result, text, i == len(statements)-1)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (h *statementHost) tablefuncCrosstabStatement(statement parser.Statement, read catalog.Read, result *sqltype.Descriptor, text string, last bool) (values []sqltype.Value, err error) {
	s, e := h.scope(), h.scope().Engine()
	view, refs := s.View, s.rlsDataRefs
	// Admission, expected analysis and source authority belong to one command,
	// not the entire SPI string. In particular, the next SELECT must not reuse
	// the preceding SELECT's prepared/source captures.
	restore := s.ActivateSQLRoutine(nil, read)
	defer func() {
		err = restore(err)
		if err != nil {
			values = nil
		}
	}()
	s.View, s.rlsDataRefs = view, refs
	s.sqlRoutine.scalar, s.sqlRoutine.rewrite = false, true
	if err := s.AcquireRelation(statement, true); err != nil {
		return nil, exec.RewriteSQLDiagnostic(err, text)
	}
	description, e2 := e.describeOperation(s, statement)
	if e2 != nil {
		return nil, exec.RewriteSQLDiagnostic(e2, text)
	}
	s.Expected = &description
	tag := rewriteReadOnlyViolation(statement)
	if tag == "" {
		if _, ok := statement.(*parser.Select); !ok {
			tag = RoutineCommandTag(statement)
		}
	}
	if tag != "" {
		return nil, exec.RewriteSQLDiagnostic(SQLError("0A000", tag+" is not allowed in a non-volatile function"), text)
	}
	execution, e2 := e.executeStatement(s, statement, 0, func(_ context.Context, info analyze.ParameterInfo, _ []sqltype.Value, _ sqltype.InputEnvironment) error {
		if info.Count != 0 {
			return fmt.Errorf("stuntgres: crosstab SQL parameter frame is not empty")
		}
		return nil
	})
	if e2 != nil {
		return nil, exec.RewriteSQLDiagnostic(e2, text)
	}
	if execution.Rows != nil {
		if last {
			input, output := h.Environments()
			// Match the shared routine-result diagnostic path: successful pivots
			// never capture a catalog/path display snapshot.
			var name func(sqltype.ID) string
			var namingErr error
			display := func(id sqltype.ID) string {
				if name == nil && namingErr == nil {
					e.Mu.Lock()
					auth, captureErr := s.RoutineAuthorizationLocked()
					if captureErr == nil {
						name, captureErr = CaptureTypeDiagnosticName(auth)
					}
					e.Mu.Unlock()
					namingErr = captureErr
				}
				if name != nil {
					return name(id)
				}
				return ""
			}
			values, e2 = exec.ConsumeCrosstab(s.Op.Context(), execution.Rows, result, input, output, text, display)
			if namingErr != nil {
				e2 = namingErr
			}
		} else {
			scratch := make([]sqltype.Value, len(execution.Rows.Schema()))
			for {
				present, nextErr := execution.Rows.Next(s.Op.Context(), scratch)
				if nextErr != nil {
					e2 = exec.RewriteSQLDiagnostic(nextErr, text)
					break
				}
				if !present {
					break
				}
			}
		}
		if !last {
			if closeErr := execution.Rows.Close(); e2 == nil {
				e2 = closeErr
			}
		}
	}
	for _, rows := range execution.RowSets {
		if closeErr := rows.Close(); e2 == nil {
			e2 = closeErr
		}
	}
	if e2 != nil {
		return nil, e2
	}
	return values, nil
}
