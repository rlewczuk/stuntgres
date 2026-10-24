package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// objectGrantLocked runs GRANT or REVOKE on objects: it validates the
// request, plans the ACL changes, emits their warnings and installs the
// candidate catalog with an advanced role record.
func (e *Engine) objectGrantLocked(op *session.Operation, v AuthorizationView, s *parser.ObjectGrant) (Result, error) {
	for {
		req, err := role.ObjectGrantRequest(ddlHost{e: e, op: op}, v, s)
		if err != nil {
			return Result{}, err
		}
		plan, warnings, err := role.ObjectGrantPlan(op.Context(), v.View, s, req, e.catalog.revisionLocked)
		if err != nil {
			for _, warning := range warnings {
				op.EmitNotice(session.LevelWarning, warning)
			}
			return Result{}, err
		}
		candidate, changed := plan.Candidate, plan.Changed
		if candidate == nil {
			for _, warning := range warnings {
				op.EmitNotice(session.LevelWarning, warning)
			}
			return Result{}, op.Context().Err()
		}
		if scope := scopeOfLease(op.Lease); scope != nil && !scope.finished {
			catalogBase, journalBase, rolesBase := e.catalog.state, op.Session.Transactions.Catalog, e.roles.published
			references := roleDependencyReferences(v.Catalog, func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
				for address, change := range changed {
					if !yield(address, change.Descriptor) {
						return
					}
				}
			}, func(func(catalog.DefaultACLKey, *catalog.DefaultACLRecord) bool) {})
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
				for address := range changed {
					if fresh.Catalog.Get(address) != v.Catalog.Get(address) {
						return Result{}, catalog.Conflict{}
					}
				}
				v = fresh
				continue
			}
		}
		for _, warning := range warnings {
			op.EmitNotice(session.LevelWarning, warning)
		}
		if s.Kind == "schema" {
			if err := candidate.AdvanceNamespaceGeneration(); err != nil {
				return Result{}, err
			}
		}
		if _, err := e.roles.revisionCandidateLocked(); err != nil {
			return Result{}, err
		}
		roles := v.RoleRecord.Advance()
		op.Session.Mu.Lock()
		defer op.Session.Mu.Unlock()
		if err := publicationOperationLocked(e, op); err != nil {
			return Result{}, err
		}
		if op.Session.Transactions.RetainsTransaction() {
			changes := op.Session.Transactions.Catalog.Clone()
			if err := e.catalogAuthorityPreflightLocked(v.catalogAuthorityLocked(ids.ObjectRef{}, nil), changes); err != nil {
				return Result{}, err
			}
			for address, change := range changed {
				e.catalogChangeLocked(changes, address, change.Descriptor)
			}
			op.Session.Transactions.Catalog = changes
		} else {
			e.catalog.state = candidate
		}
		return Result{}, e.installOperationRolesLocked(op, roles)
	}
}
