package core

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

// Called with Engine.mu held and returns with it held. Only immutable metadata
// selection runs under the mutex. Lock waits, provider admission and source
// selection run outside it. The statement owns every partially built program.
func (e *Engine) captureRowSecurityLocked(s *StatementScope, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, proposed, selectRead bool) (result *exec.RLSProgram, err error) {
	s.rlsAdmissionDepth++
	defer func() {
		err = e.endRowSecurityAdmissionLocked(s, err)
		if err != nil {
			result = nil
		}
	}()
	if err := s.Op.Context().Err(); err != nil {
		return nil, err
	}
	if !d.Security.RLSEnabled {
		return nil, nil
	}
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return nil, err
	}
	auth.Recapture(e.PrivateCatalogLocked(s.Owner.Catalog))
	if principal == (authorization.PrincipalRef{}) {
		principal = auth.State.CurrentUser
	}
	auth.State.CurrentUser = principal
	if err := access.RowSecuritySettingGuard(s.RowSecurity, auth.Roles, d, principal); err != nil {
		return nil, err
	}
	selection, err := access.SelectRowSecurity(auth.Roles, d, principal, command, proposed, selectRead)
	if err != nil {
		return nil, err
	}
	admission := &rowSecurityProgram{RLSProgram: exec.RLSProgram{Table: d.Name()}, relation: d, principal: principal, command: command, proposed: proposed, selectRead: selectRead, selection: selection, roles: auth.Roles}
	s.rlsPrograms = append(s.rlsPrograms, admission)
	if selection.Count == 0 {
		admission.complete = true
		return nil, nil
	}
	p := &admission.RLSProgram
	p.Groups = make([]exec.RLSRuntimeGroup, selection.Count)
	// A missing command group makes the entire conjunction false. In particular
	// restrictive-only clauses (including errors/subqueries) are not admitted.
	if selection.Denied() {
		admission.complete = true
		return p, nil
	}
	for _, active := range s.rlsActive {
		if active == d.Address() {
			return nil, SQLError("42P17", fmt.Sprintf(`infinite recursion detected in policy for relation "%s"`, d.Name()))
		}
	}
	s.rlsActive = append(s.rlsActive, d.Address())
	defer func() { s.rlsActive = s.rlsActive[:len(s.rlsActive)-1] }()
	for i := range selection.Count {
		selected := &selection.Groups[i]
		group := &p.Groups[i]
		group.Permissive = make([]exec.RLSRuntimeClause, len(selected.Permissive))
		group.Restrictive = make([]exec.RLSRuntimeClause, len(selected.Restrictive))
		for j, clause := range selected.Permissive {
			if err := e.importPolicyClauseLocked(s, d, auth, clause, &group.Permissive[j], admission); err != nil {
				return nil, err
			}
			p.Volatile = p.Volatile || group.Permissive[j].Expression.Properties().Volatility == expr.Volatile
		}
		for j, clause := range selected.Restrictive {
			if err := e.importPolicyClauseLocked(s, d, auth, clause, &group.Restrictive[j], admission); err != nil {
				return nil, err
			}
			p.Volatile = p.Volatile || group.Restrictive[j].Expression.Properties().Volatility == expr.Volatile
		}
	}
	admission.complete = true
	return p, nil
}

func (e *Engine) importPolicyClauseLocked(s *StatementScope, consumer *catalog.Relation, auth AuthorizationView, clause access.RLSClause, out *exec.RLSRuntimeClause, admission *rowSecurityProgram) error {
	if err := s.Op.Context().Err(); err != nil {
		return err
	}
	policy := stored.PolicyOf(clause.Payload)
	if policy == nil {
		return ids.ErrCatalogReplaced
	}
	read := auth.Catalog
	imported, err := view.ImportPolicyClause(read, e.Catalog.Domain, consumer, policy, auth.State.CurrentUser)
	if err != nil {
		return err
	}
	definition, binding, node, closure, refs := policy.Presentation(), &imported.Binding, imported.Node, imported.Closure, imported.Refs
	for i := range binding.QuerySources {
		binding.QuerySources[i].Storage = e.PrivateStorageLocked(s.Owner.Catalog, binding.QuerySources[i].Ref.Address)
	}
	admitted := make([]exec.Leaf, 0, len(closure.Modes))
	for ref := range closure.Modes {
		admitted = append(admitted, exec.Leaf{Ref: ref, Relation: read.MustRelation(ref.Address), Storage: e.PrivateStorageLocked(s.Owner.Catalog, ref.Address)})
	}
	if len(refs) != 0 {
		roots := binding.QuerySources
		e.Mu.Unlock()
		err = s.acquireBoundViewRelationModes(refs, closure.Modes)
		e.Mu.Lock()
		if err != nil {
			return err
		}
		for _, leaf := range admitted {
			if err := e.validatePolicySourceLocked(s, e.PrivateCatalogLocked(s.Owner.Catalog), leaf); err != nil {
				return err
			}
		}
		e.Mu.Unlock()
		if len(roots) != 0 {
			err = s.acquireInheritanceSources(closure.Modes, roots)
		}
		e.Mu.Lock()
		if err != nil {
			return err
		}
	}
	auth, err = e.refreshPolicyAuthorityLocked(s, consumer, policy, auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if err := checkPolicyImportCommandAuthorityLocked(s, auth); err != nil {
		return err
	}
	for _, leaf := range admitted {
		if err := e.validatePolicySourceLocked(s, auth.Catalog, leaf); err != nil {
			return err
		}
	}
	// Shape/storage identity and the nested definition graph survived. RLS flags,
	// ownership and rights come from grant-time metadata, never the pre-wait leaf.
	for i, leaf := range binding.QuerySources {
		binding.QuerySources[i].Relation = auth.Catalog.MustRelation(leaf.Ref.Address)
	}
	current := auth.Catalog
	evaluation := exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalStatement: exec.EvalStatement{Context: s.Op.Context(), Host: HostOf(s), Catalog: current, Input: s.Input, Output: s.Output, PlanStaticOperators: true}}
	evaluation.RoutineRecipe = true
	evaluation.RoutineConsumer = consumer
	evaluation.RoutineChecks = definition.Dependencies()
	evaluation.RoutineAuthority = &auth
	s.Inquiry.visibilityDemand = s.Inquiry.visibilityDemand || binding.VisibilitySnapshot
	if binding.Prepared.RegclassConcat {
		if err := e.CaptureCommentSnapshotLocked(s); err != nil {
			return err
		}
	}
	if binding.VisibilitySnapshot {
		if err := e.captureVisibilitySnapshotLocked(s); err != nil {
			return err
		}
	}
	e.Mu.Unlock()
	node, _, err = exec.PreflightCoercions(&evaluation, node)
	if err == nil {
		err = exec.AdmitExpression(&evaluation, node, current)
	}
	e.Mu.Lock()
	if err != nil {
		return err
	}
	evaluation.PlanStaticOperators = false
	evaluation.RoutineAuthority = nil
	out.Name, out.Expression, out.Evaluation = clause.Name, node, evaluation
	if !binding.SubqueryDemand && len(binding.QuerySources) == 0 {
		return nil
	}
	// AdmitExpression already validates every nested marked query's ordinary
	// join/shape legality. Extend the existing physical occurrence owner rather
	// than silently dropping supported marks or creating a clause-local arbiter.
	if err := e.InitializeQueryLocksLocked(s, nil, binding.QuerySources); err != nil {
		return err
	}
	if s.Owner.ReadOnly {
		mode, err := view.FirstQueryRowMode(e.PrivateCatalogLocked(s.Owner.Catalog), nil, binding.QuerySources)
		if err != nil {
			return err
		}
		if mode != 0 {
			return SQLError("25006", "cannot execute SELECT "+analyze.RowLockClause(mode)+" in a read-only transaction")
		}
	}
	prepared, err := e.prepareQuerySourcesLocked(s, binding.QuerySources, true, auth.State.CurrentUser, plan.RowMark{})
	if err != nil {
		return err
	}
	admission.sources = append(admission.sources, policySourcePreparation{clause: out, sources: prepared})
	admission.Volatile = admission.Volatile || prepared.volatile()
	return nil
}
