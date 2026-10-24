package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) alterDefaultPrivilegesLocked(op *session.Operation, v AuthorizationView, s *parser.AlterDefaultPrivileges) (Result, error) {
	for {
		updates, err := role.DefaultPrivilegesPlan(op.Context(), v.View, s)
		if err != nil {
			return Result{}, err
		}
		if len(updates) == 0 {
			return Result{}, op.Context().Err()
		}
		if scope := scopeOfLease(op.Lease); scope != nil && !scope.finished {
			catalogBase, journalBase, rolesBase := e.catalog.state, op.Session.Transactions.Catalog, e.roles.published
			references := roleDependencyReferences(v.Catalog, func(func(ids.ObjectAddress, catalog.Descriptor) bool) {}, func(yield func(catalog.DefaultACLKey, *catalog.DefaultACLRecord) bool) {
				for key, record := range updates {
					if !yield(key, record) {
						return
					}
				}
			})
			e.mu.Unlock()
			lockErr := scope.acquireRoleDependencies(references)
			e.mu.Lock()
			if lockErr != nil {
				return Result{}, lockErr
			}
			fresh, err := e.authorizationLocked(op)
			if err != nil {
				return Result{}, err
			}
			if e.catalog.state != catalogBase || op.Session.Transactions.Catalog != journalBase || e.roles.published != rolesBase || fresh.RoleRecord != v.RoleRecord {
				for key := range updates {
					if fresh.Catalog.State().DefaultACL(key) != v.Catalog.State().DefaultACL(key) {
						return Result{}, catalog.Conflict{}
					}
				}
				v = fresh
				continue
			}
		}
		// Reserve identities only for final surviving changes, after every logical
		// target succeeded. Published records are never modified in place.
		for key, record := range updates {
			if record == nil {
				continue
			}
			if old := v.Catalog.State().DefaultACL(key); old != nil {
				record.OID = old.OID
			} else {
				oid, err := e.catalog.allocateOIDLocked()
				if err != nil {
					return Result{}, err
				}
				record.OID = oid
			}
		}
		candidate := *v.Catalog.State()
		candidate.OverlayDefaultACLs(v.Catalog.State(), updates)
		candidateCatalog := e.catalog.read(&candidate)
		candidateRoles := e.roles.read(e.catalog.owner(), candidateCatalog, v.RoleRecord)
		if err := catalog.ValidateDefaultACLs(candidateRoles, candidateCatalog); err != nil {
			return Result{}, err
		}
		if _, err := e.roles.revisionCandidateLocked(); err != nil {
			return Result{}, err
		}
		nextRoles := v.RoleRecord.Advance()
		op.Session.Mu.Lock()
		defer op.Session.Mu.Unlock()
		if err := publicationOperationLocked(e, op); err != nil {
			return Result{}, err
		}
		if op.Session.Transactions.RetainsTransaction() {
			changes := op.Session.Transactions.Catalog.Clone()
			// Default utilities use the selected private DB authority, but their
			// retained effects must fence its exact committed predecessor.
			if err := e.catalogAuthorityPreflightLocked(v.catalogAuthorityLocked(ids.ObjectRef{}, nil), changes); err != nil {
				return Result{}, err
			}
			for key, record := range updates {
				e.defaultACLChangeLocked(changes, key, record)
			}
			op.Session.Transactions.Catalog = changes
		} else {
			e.catalog.state = &candidate
		}
		return Result{}, e.installOperationRolesLocked(op, nextRoles)
	}
}
