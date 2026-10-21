package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// objectGrantLocked runs GRANT or REVOKE on objects: it validates the
// request, plans the ACL changes, emits their warnings and installs the
// candidate catalog with an advanced role record.
func (e *Engine) objectGrantLocked(op *session.Operation, v AuthorizationView, s *parser.ObjectGrant) (Result, error) {
	req, err := role.ObjectGrantRequest(ddlHost{e: e, op: op}, v, s)
	if err != nil {
		return Result{}, err
	}
	plan, warnings, err := role.ObjectGrantPlan(op.Context(), v.View, s, req, e.catalog.revisionLocked)
	for _, warning := range warnings {
		op.EmitNotice(session.LevelWarning, warning)
	}
	if err != nil {
		return Result{}, err
	}
	candidate, changed := plan.Candidate, plan.Changed
	if candidate == nil {
		return Result{}, op.Context().Err()
	}
	if s.Kind == "schema" {
		if err := candidate.AdvanceNamespaceGeneration(); err != nil {
			return Result{}, err
		}
	}
	if v.RoleRecord.Revision == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	roles := v.RoleRecord.Advance()
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	if op.Session.Transactions.RetainsTransaction() {
		changes := op.Session.Transactions.Catalog.Clone()
		// Completed grant utilities retain their consulted DB predecessor in
		// the candidate, not a scope authority with a pre-advance role revision.
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
	e.installOperationRolesLocked(op, roles)
	return Result{}, nil
}
