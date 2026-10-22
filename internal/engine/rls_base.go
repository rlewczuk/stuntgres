package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// The caller holds Engine.mu; capture returns with it held after unlocked
// admission of the selected expression graph and nested sources.
func (e *Engine) rowSecurityInputLocked(scope *StatementScope, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, selectRead bool) (*exec.RLSProgram, error) {
	return e.captureRowSecurityLocked(scope, d, principal, command, false, selectRead)
}

// configureRowSecurityLocked writes the command's row-level security
// (scope.rls) under the statement authority, as the rule action's user when
// a rule action runs, over the statement's private catalog. Like the
// selection, it keeps the fields written before an error.
func (e *Engine) configureRowSecurityLocked(scope *StatementScope, b analyze.Binding, stmt parser.Statement) (err error) {
	scope.rlsAdmissionDepth++
	defer func() {
		if err == nil {
			err = e.prepareCommandSourcesLocked(scope, b.Ref)
		}
		err = e.endRowSecurityAdmissionLocked(scope, err)
	}()
	rls, command, applies := view.RowSecurityTarget(scope.RLS, b, stmt)
	scope.RLS = rls
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
	principal := access.RowSecurityPrincipal(auth.State.CurrentUser, view.RowSecurityLayers(auth.Catalog, b))
	if _, merge := stmt.(*parser.Merge); merge || b.Insert.Conflict != nil {
		scope.RLS, err = view.SelectAdjacentRowSecurity(scope.RLS, auth.View, scope.rowSecurity, b, stmt, command)
		return err
	}
	reads := b.Returning != nil || len(b.UsedColumns) != 0
	if b.ViewWrite != nil {
		reads = reads || len(view.ViewWriteOf(b.ViewWrite).Logical.UsedColumns) != 0
	}
	if command != catalog.PolicyInsert {
		scope.RLS.Old, err = e.captureRowSecurityLocked(scope, b.Relation, principal, command, false, reads)
		if err != nil {
			return err
		}
		scope.RLS.TargetDenied = scope.RLS.Old.Denied()
	}
	if command == catalog.PolicyInsert || command == catalog.PolicyUpdate {
		proposedRead := reads
		if command == catalog.PolicyInsert {
			proposedRead = b.Returning != nil
		}
		scope.RLS.New, err = e.captureRowSecurityLocked(scope, b.Relation, principal, command, true, proposedRead)
	}
	return err
}
