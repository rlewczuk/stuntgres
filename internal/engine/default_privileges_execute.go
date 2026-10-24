package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

func (e *Engine) executeDefaultPrivileges(scope *StatementScope, statement *parser.AlterDefaultPrivileges, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	var scratch [8]defaultPrivilegeClaim
	claims := scratch[:0]
	e.mu.Lock()
	auth, err := e.authorizationLocked(scope.op)
	publishedRoles := e.roles.published
	var updates map[catalog.DefaultACLKey]*catalog.DefaultACLRecord
	if err == nil {
		updates, err = role.DefaultPrivilegesPlan(scope.op.Context(), auth.View, statement)
	}
	if err == nil {
		// Capture honest committed predecessors in the planning window, not
		// after releasing Engine.mu and accidentally adopting a peer's update.
		for key := range updates {
			claims = append(claims, defaultPrivilegeClaim{key: key, original: e.catalog.state.DefaultACL(key)})
		}
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if len(updates) == 0 {
		return Result{}, scope.op.Context().Err()
	}
	if err = scope.admitDefaultPrivileges(claims); err != nil {
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
	for _, claim := range claims {
		if e.catalog.state.DefaultACL(claim.key) != claim.original {
			return Result{}, catalog.Conflict{}
		}
	}
	result, err := e.stageDefaultPrivilegesLocked(scope, fresh, updates, publishedRoles)
	if err == nil && scope.catalogAuthority != nil {
		scope.catalogAuthority.DatabasePredecessor = auth.DatabasePredecessor
		scope.catalogAuthority.DatabaseAuthority = auth.Catalog.Get(auth.Owner.DatabaseAddress())
	}
	return result, err
}

func (e *Engine) stageDefaultPrivilegesLocked(scope *StatementScope, auth AuthorizationView, updates map[catalog.DefaultACLKey]*catalog.DefaultACLRecord, publishedRoles *access.RoleRecord) (Result, error) {
	// Reserve identities only after logical actions and ACL writer admissions.
	// Existing private/default records keep their actual row identity.
	for key, record := range updates {
		if record == nil {
			continue
		}
		if old := auth.Catalog.State().DefaultACL(key); old != nil {
			record.OID = old.OID
		} else {
			oid, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return Result{}, err
			}
			record.OID = oid
		}
	}
	candidate := *auth.Catalog.State()
	candidate.OverlayDefaultACLs(auth.Catalog.State(), updates)
	candidateCatalog := e.catalog.read(&candidate)
	candidateRoles := e.roles.read(e.catalog.owner(), candidateCatalog, auth.RoleRecord)
	if err := catalog.ValidateDefaultACLs(candidateRoles, candidateCatalog); err != nil {
		return Result{}, err
	}
	if auth.RoleRecord.Revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	if _, err := e.roles.revisionCandidateLocked(); err != nil {
		return Result{}, err
	}
	changes := scope.owner.Catalog.Clone()
	for key, record := range updates {
		e.defaultACLChangeLocked(changes, key, record)
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
	scope.catalogAuthority.ACLRoles = auth.RoleRecord
	scope.catalogAuthority.ACLPublishedRoles = publishedRoles
	return Result{}, nil
}
