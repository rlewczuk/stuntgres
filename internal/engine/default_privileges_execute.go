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
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	publishedRoles := e.RoleStore.Published
	var updates map[catalog.DefaultACLKey]*catalog.DefaultACLRecord
	if err == nil {
		updates, err = role.DefaultPrivilegesPlan(scope.Op.Context(), auth.View, statement)
	}
	if err == nil {
		// Capture honest committed predecessors in the planning window, not
		// after releasing Engine.mu and accidentally adopting a peer's update.
		for key := range updates {
			claims = append(claims, defaultPrivilegeClaim{key: key, original: e.Catalog.State.DefaultACL(key)})
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if len(updates) == 0 {
		return Result{}, scope.Op.Context().Err()
	}
	if err = scope.admitDefaultPrivileges(claims); err != nil {
		return Result{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	fresh, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return Result{}, err
	}
	if fresh.Revision != auth.Revision || !fresh.RoleRecord.SameAuthority(auth.RoleRecord) || !e.RoleStore.Published.SameAuthority(publishedRoles) {
		return Result{}, catalog.Conflict{}
	}
	for _, claim := range claims {
		if e.Catalog.State.DefaultACL(claim.key) != claim.original {
			return Result{}, catalog.Conflict{}
		}
	}
	result, err := e.stageDefaultPrivilegesLocked(scope, fresh, updates, publishedRoles)
	if err == nil && scope.CatalogAuthority != nil {
		scope.CatalogAuthority.DatabasePredecessor = auth.DatabasePredecessor
		scope.CatalogAuthority.DatabaseAuthority = auth.Catalog.Get(auth.Owner.DatabaseAddress())
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
			oid, err := e.Catalog.AllocateOIDLocked()
			if err != nil {
				return Result{}, err
			}
			record.OID = oid
		}
	}
	candidate := *auth.Catalog.State()
	candidate.OverlayDefaultACLs(auth.Catalog.State(), updates)
	candidateCatalog := e.Catalog.Read(&candidate)
	candidateRoles := e.RoleStore.Read(&e.Catalog.Owner, candidateCatalog, auth.RoleRecord)
	if err := catalog.ValidateDefaultACLs(candidateRoles, candidateCatalog); err != nil {
		return Result{}, err
	}
	if auth.RoleRecord.Revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	if _, err := e.RoleStore.RevisionCandidateLocked(); err != nil {
		return Result{}, err
	}
	changes := scope.Owner.Catalog.Clone()
	for key, record := range updates {
		e.defaultACLChangeLocked(changes, key, record)
	}
	if changes.RolesBase == nil {
		changes.RolesBase = e.RoleStore.Published
	}
	changes.Roles = auth.RoleRecord.Advance()
	if err := scope.AcquireJournalRoleDependenciesLocked(changes); err != nil {
		return Result{}, err
	}
	fresh, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return Result{}, err
	}
	if fresh.Revision != auth.Revision || !fresh.RoleRecord.SameAuthority(auth.RoleRecord) || !e.RoleStore.Published.SameAuthority(publishedRoles) {
		return Result{}, catalog.Conflict{}
	}
	if err := e.CatalogPreflightLocked(changes); err != nil {
		return Result{}, err
	}
	if err := scope.Op.Context().Err(); err != nil {
		return Result{}, err
	}
	if err := e.reserveMixedACLRevisionLocked(changes); err != nil {
		return Result{}, err
	}
	if err := e.AssignTransactionXIDLocked(scope.TxRef); err != nil {
		return Result{}, err
	}
	scope.Catalog = changes
	scope.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
	scope.CatalogAuthority.ACLRoles = auth.RoleRecord
	scope.CatalogAuthority.ACLPublishedRoles = publishedRoles
	return Result{}, nil
}
