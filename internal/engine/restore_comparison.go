package engine

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// The comparison-context hooks forward to control.ComparisonRestore; the root
// keeps only the stage execution of marked statements and the authorization
// capture.

func decodeRestoreComparisonContext(r *restoreRun, comment string, position parser.Position) (bool, error) {
	return r.comparison.Decode(comment, position)
}

func pairRestoreComparisonContext(r *restoreRun) error {
	return r.comparison.Pair(r.source, r.statements)
}

// beforeRestoreComparisonContext runs a marked SET or RESET itself, so no
// later entry sees it.
func beforeRestoreComparisonContext(r *restoreRun, op *session.Operation, stmt parser.Statement) (bool, error) {
	execute, err := r.comparison.Before(r.cur.index, stmt, r.stage.CatalogRead())
	if err != nil || !execute {
		return false, err
	}
	_, err = r.stage.Execute(op, stmt, 0, nil)
	return true, err
}

func afterRestoreComparisonContext(r *restoreRun, op *session.Operation, stmt parser.Statement, _ control.Outcome) error {
	check, ok := r.comparison.After()
	if !ok {
		return nil
	}
	if err := validateComparisonBindingStatement(r.stage, op, stmt, check); err != nil {
		return err
	}
	r.comparison.Bound()
	return nil
}

func finishRestoreComparisonContext(r *restoreRun, _ context.Context, _ *session.Operation) error {
	return r.comparison.Finish()
}

// validateComparisonBindingStatement captures the stage authorization under
// Engine.mu, checks USAGE on the bound namespace with nothing held, then
// checks the definitions stmt published.
func validateComparisonBindingStatement(e *Engine, op *session.Operation, stmt parser.Statement, check control.ComparisonCheck) error {
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if err := auth.Check(access.Requirement{Object: auth.Owner.Domain.Ref(auth.Catalog.Epoch(), check.Namespace), Privileges: authorization.UsagePrivilege}); err != nil {
		return err
	}
	return control.ValidateComparisonBinding(auth.Catalog, check.Before, stmt, check.Namespace, check.Position)
}
