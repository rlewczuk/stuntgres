package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// The caller holds Engine.mu; this never captures row data or mutates identity.
func (e *Engine) rowSecurityInputLocked(scope *StatementScope, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, selectRead bool) (bool, error) {
	if !d.Security.RLSEnabled {
		return false, nil
	}
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return false, err
	}
	auth.Catalog = e.privateCatalogLocked(scope.owner.Catalog)
	auth.Recapture(auth.Catalog)
	return view.RowSecurityInputDenied(auth.View, scope.rowSecurity, d, principal, command, selectRead)
}

// configureRowSecurityLocked writes the command's row-level security
// (scope.rls) under the statement authority, as the rule action's user when
// a rule action runs, over the statement's private catalog. Like the
// selection, it keeps the fields written before an error.
func (e *Engine) configureRowSecurityLocked(scope *StatementScope, b analyze.Binding, stmt parser.Statement) error {
	rls, command, applies := view.RowSecurityTarget(scope.rls, b, stmt)
	scope.rls = rls
	if !applies {
		return nil
	}
	auth, err := e.statementAuthorizationLocked(scope.op, stmt)
	if err != nil {
		return err
	}
	if scope.ruleAction.auth != nil {
		auth.State.CurrentUser = scope.ruleAction.auth.State.CurrentUser
	}
	auth.Catalog = e.privateCatalogLocked(scope.owner.Catalog)
	auth.Recapture(auth.Catalog)
	scope.rls, err = view.SelectRowSecurity(scope.rls, auth.View, scope.rowSecurity, b, stmt, command)
	return err
}
