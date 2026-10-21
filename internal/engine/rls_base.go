package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
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
	if principal == (authorization.PrincipalRef{}) {
		principal = auth.State.CurrentUser
	}
	if err := access.RowSecuritySettingGuard(scope.rowSecurity, auth.Roles, d, principal); err != nil {
		return false, err
	}
	decision, err := access.RowSecurityGroups(auth.Roles, d, principal, command, false, selectRead)
	if err != nil {
		return false, err
	}
	if decision == access.RLSNeedsExpressions {
		return false, access.RowSecurityExpressionError()
	}
	return decision == access.RLSDefaultDeny, nil
}

// rowSecurityMutation is the row-level security state of one command,
// written by configureRowSecurityLocked. It is part of statementMutation, so
// command switches save and restore it as one unit.
type rowSecurityMutation struct {
	targetDenied  bool
	proposal      access.RLSDisposition
	mergeUpdate   access.RLSDisposition
	mergeInsert   access.RLSDisposition
	proposalTable string
}

func (e *Engine) configureRowSecurityLocked(scope *StatementScope, b analyze.Binding, stmt parser.Statement) error {
	scope.rls.targetDenied = false
	scope.rls.proposal, scope.rls.mergeInsert, scope.rls.mergeUpdate = access.RLSUnrestricted, access.RLSUnrestricted, access.RLSUnrestricted
	if b.Relation == nil {
		return nil
	}
	command := catalog.PolicyCommand("")
	switch stmt.(type) {
	case *parser.Insert:
		command = catalog.PolicyInsert
	case *parser.Update:
		command = catalog.PolicyUpdate
	case *parser.Delete:
		command = catalog.PolicyDelete
	case *parser.Merge:
		command = catalog.PolicySelect
	default:
		return nil
	}
	scope.rls.proposalTable = b.Relation.Name()
	if !b.Relation.Security.RLSEnabled {
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
	principal := access.RowSecurityPrincipal(auth.State.CurrentUser, RowSecurityLayers(auth.Catalog, b))
	if err := access.RowSecuritySettingGuard(scope.rowSecurity, auth.Roles, b.Relation, principal); err != nil {
		return err
	}
	reads := b.Returning != nil || len(b.UsedColumns) != 0
	if b.ViewWrite != nil {
		reads = reads || len(ViewWriteOf(b.ViewWrite).Logical.UsedColumns) != 0
	}
	if command != catalog.PolicyInsert {
		decision, err := access.RowSecurityGroups(auth.Roles, b.Relation, principal, command, false, reads)
		if err != nil {
			return err
		}
		if decision == access.RLSNeedsExpressions {
			return access.RowSecurityExpressionError()
		}
		scope.rls.targetDenied = decision == access.RLSDefaultDeny
	}
	proposedRead := b.Insert.Conflict != nil && len(b.Insert.Conflict.Reads) != 0
	switch stmt.(type) {
	case *parser.Insert:
		scope.rls.proposal, err = access.RowSecurityGroups(auth.Roles, b.Relation, principal, catalog.PolicyInsert, true, proposedRead)
	case *parser.Update:
		scope.rls.proposal, err = access.RowSecurityGroups(auth.Roles, b.Relation, principal, catalog.PolicyUpdate, true, reads)
	case *parser.Merge:
		scope.rls.mergeInsert, err = access.RowSecurityGroups(auth.Roles, b.Relation, principal, catalog.PolicyInsert, true, false)
		if err == nil {
			scope.rls.mergeUpdate, err = access.RowSecurityGroups(auth.Roles, b.Relation, principal, catalog.PolicyUpdate, true, true)
		}
	}
	return err
}
