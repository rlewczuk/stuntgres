package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Object grants own only their statement candidate. Finish decides whether to
// retain it privately or publish it with the transaction's other effects.
func (e *Engine) executeObjectGrant(scope *StatementScope, statement *parser.ObjectGrant, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(scope.op)
	publishedRoles := e.roles.published
	var request role.GrantRequest
	if err == nil {
		request, err = role.ObjectGrantRequest(ddlHost{e: e, op: scope.op}, auth, statement)
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = scope.admitObjectGrant(request, statement); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fresh, err := e.authorizationLocked(scope.op)
	if err != nil {
		return Result{}, err
	}
	if fresh.Revision != auth.Revision || !fresh.RoleRecord.SameAuthority(auth.RoleRecord) || !e.roles.published.SameAuthority(publishedRoles) {
		return Result{}, catalog.Conflict{}
	}
	for _, target := range request.Targets() {
		installed := fresh.Catalog.Get(target.Address())
		if installed != target && (installed != nil || target.Address().ClassID != ids.ParameterClassID) {
			return Result{}, catalog.Conflict{}
		}
	}
	result, err := e.stageObjectGrantLocked(scope, fresh, statement, request, publishedRoles)
	if err == nil && scope.catalogAuthority != nil {
		scope.catalogAuthority.databasePredecessor = auth.DatabasePredecessor
		scope.catalogAuthority.databaseAuthority = auth.Catalog.Get(auth.Owner.DatabaseAddress())
	}
	return result, err
}

func (e *Engine) stageObjectGrantLocked(scope *StatementScope, auth AuthorizationView, statement *parser.ObjectGrant, request role.GrantRequest, publishedRoles *access.RoleRecord) (Result, error) {
	plan, warnings, err := role.ObjectGrantPlan(scope.op.Context(), auth.View, statement, request, e.catalog.revisionLocked)
	for _, warning := range warnings {
		scope.op.EmitNotice(session.LevelWarning, warning)
	}
	if err != nil {
		return Result{}, err
	}
	if plan.Candidate == nil {
		return Result{}, scope.op.Context().Err()
	}
	if statement.Kind == "schema" {
		// Preserve the early overflow diagnostic. The combined publisher owns
		// the actual namespace invalidation event, including peer-root rebasing.
		if err := plan.Candidate.AdvanceNamespaceGeneration(); err != nil {
			return Result{}, err
		}
	}
	if auth.RoleRecord.Revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	if _, err := e.roles.revisionCandidateLocked(); err != nil {
		return Result{}, err
	}
	changes := scope.owner.Catalog.Clone()
	for address, change := range plan.Changed {
		e.catalogChangeLocked(changes, address, change.Descriptor)
	}
	if changes.RolesBase == nil {
		changes.RolesBase = e.roles.published
	}
	changes.Roles = auth.RoleRecord.Advance()
	if err := scope.acquireJournalRoleDependenciesLocked(changes); err != nil {
		return Result{}, err
	}
	fresh, err := e.authorizationLocked(scope.op)
	if err != nil {
		return Result{}, err
	}
	if fresh.Revision != auth.Revision || !fresh.RoleRecord.SameAuthority(auth.RoleRecord) || !e.roles.published.SameAuthority(publishedRoles) {
		return Result{}, catalog.Conflict{}
	}
	if err := e.catalogPreflightLocked(changes); err != nil {
		return Result{}, err
	}
	if err := scope.op.Context().Err(); err != nil {
		return Result{}, err
	}
	if err := e.reserveMixedACLRevisionLocked(changes); err != nil {
		return Result{}, err
	}
	if err := e.assignTransactionXIDLocked(scope.txRef); err != nil {
		return Result{}, err
	}
	scope.catalog = changes
	scope.catalogAuthority = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
	scope.catalogAuthority.aclRoles = auth.RoleRecord
	scope.catalogAuthority.aclPublishedRoles = publishedRoles
	return Result{}, nil
}
