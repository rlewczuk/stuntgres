package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// ExecuteObjectGrant runs GRANT and REVOKE on objects other than databases
// and foreign-data objects.
//
// Object grants own only their statement candidate. Finish decides whether to
// retain it privately or publish it with the transaction's other effects.
func ExecuteObjectGrant(e *core.Engine, scope *core.StatementScope, statement *parser.ObjectGrant, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	publishedRoles := e.RoleStore.Published
	var request role.GrantRequest
	if err == nil {
		request, err = role.ObjectGrantRequest(RoleHost{core.DDLHost{E: e, Op: scope.Op}}, auth, statement)
	}
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if err = admitObjectGrant(scope, request, statement); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	fresh, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return core.Result{}, err
	}
	if fresh.Revision != auth.Revision || !fresh.RoleRecord.SameAuthority(auth.RoleRecord) || !e.RoleStore.Published.SameAuthority(publishedRoles) {
		return core.Result{}, catalog.Conflict{}
	}
	for _, target := range request.Targets() {
		installed := fresh.Catalog.Get(target.Address())
		if installed != target && (installed != nil || target.Address().ClassID != ids.ParameterClassID) {
			return core.Result{}, catalog.Conflict{}
		}
	}
	result, err := stageObjectGrantLocked(e, scope, fresh, statement, request, publishedRoles)
	if err == nil && scope.CatalogAuthority != nil {
		scope.CatalogAuthority.DatabasePredecessor = auth.DatabasePredecessor
		scope.CatalogAuthority.DatabaseAuthority = auth.Catalog.Get(auth.Owner.DatabaseAddress())
	}
	return result, err
}

func stageObjectGrantLocked(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, statement *parser.ObjectGrant, request role.GrantRequest, publishedRoles *access.RoleRecord) (core.Result, error) {
	plan, warnings, err := role.ObjectGrantPlan(scope.Op.Context(), auth.View, statement, request, e.Catalog.RevisionLocked)
	for _, warning := range warnings {
		scope.Op.EmitNotice(session.LevelWarning, warning)
	}
	if err != nil {
		return core.Result{}, err
	}
	if plan.Candidate == nil {
		return core.Result{}, scope.Op.Context().Err()
	}
	if statement.Kind == "schema" {
		// Preserve the early overflow diagnostic. The combined publisher owns
		// the actual namespace invalidation event, including peer-root rebasing.
		if err := plan.Candidate.AdvanceNamespaceGeneration(); err != nil {
			return core.Result{}, err
		}
	}
	if auth.RoleRecord.Revision == ^uint64(0) {
		return core.Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	if _, err := e.RoleStore.RevisionCandidateLocked(); err != nil {
		return core.Result{}, err
	}
	changes := scope.Owner.Catalog.Clone()
	for address, change := range plan.Changed {
		e.CatalogChangeLocked(changes, address, change.Descriptor)
	}
	if changes.RolesBase == nil {
		changes.RolesBase = e.RoleStore.Published
	}
	changes.Roles = auth.RoleRecord.Advance()
	if err := scope.AcquireJournalRoleDependenciesLocked(changes); err != nil {
		return core.Result{}, err
	}
	fresh, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return core.Result{}, err
	}
	if fresh.Revision != auth.Revision || !fresh.RoleRecord.SameAuthority(auth.RoleRecord) || !e.RoleStore.Published.SameAuthority(publishedRoles) {
		return core.Result{}, catalog.Conflict{}
	}
	if err := e.CatalogPreflightLocked(changes); err != nil {
		return core.Result{}, err
	}
	if err := scope.Op.Context().Err(); err != nil {
		return core.Result{}, err
	}
	if err := reserveMixedACLRevisionLocked(e, changes); err != nil {
		return core.Result{}, err
	}
	if err := e.AssignTransactionXIDLocked(scope.TxRef); err != nil {
		return core.Result{}, err
	}
	scope.Catalog = changes
	scope.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
	scope.CatalogAuthority.ACLRoles = auth.RoleRecord
	scope.CatalogAuthority.ACLPublishedRoles = publishedRoles
	return core.Result{}, nil
}
