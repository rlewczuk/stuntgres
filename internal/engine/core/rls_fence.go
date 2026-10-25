package core

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

// The engine owns admission identity; exec sources borrow only the embedded
// runtime. Even enabled-RLS bypasses retain a metadata record, since membership
// can revoke bypass while a later sibling imports and waits.
type rowSecurityProgram struct {
	exec.RLSProgram
	relation                       *catalog.Relation
	principal                      authorization.PrincipalRef
	command                        catalog.PolicyCommand
	proposed, selectRead, complete bool
	selection                      access.RLSSelection
	roles                          authorization.Read        // Statement-local applicability proof, never row truth or privilege.
	sources                        []policySourcePreparation // Metadata-only until the complete shared snapshot exists.
}

// All three admission entry points share this depth: target OLD/NEW, program
// expansion and whole source bundles. Only a completed outer scope may fence
// the entire graph; partial imports never become executable.
func (e *Engine) endRowSecurityAdmissionLocked(s *StatementScope, err error) error {
	s.rlsAdmissionDepth--
	if err != nil || s.rlsAdmissionDepth != 0 {
		return err
	}
	return e.fenceRowSecurityProgramsLocked(s)
}

// refreshPolicyAuthorityLocked checks the admitted immutable source identities
// against current role/schema/table authority, never re-resolving SQL names.
func (e *Engine) refreshPolicyAuthorityLocked(s *StatementScope, consumer *catalog.Relation, policy *stored.Policy, principal authorization.PrincipalRef) (AuthorizationView, error) {
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return auth, err
	}
	auth.Recapture(e.PrivateCatalogLocked(s.Owner.Catalog))
	auth.State.CurrentUser = principal
	return auth, view.CheckPolicyAuthority(auth.View, auth, consumer, policy)
}

// A nested view cannot replace its admitted body/definer graph across a wait.
// Ordinary same-shape table ACL/RLS metadata is refreshed after this identity
// proof; no source is ever reopened by a current SQL name.
func (e *Engine) validatePolicySourceLocked(s *StatementScope, read catalog.Read, leaf exec.Leaf) error {
	live, err := CurrentQuerySourceLocked(e, read, s.Owner.Catalog, leaf)
	if err != nil {
		return err
	}
	if live.RelationKind == catalog.ViewRelation && (live.View != leaf.Relation.View || !catalog.RelationDefiningAuthorityCompatible(leaf.Relation, live)) {
		return ids.ErrCatalogReplaced
	}
	return nil
}

// Fence every selected dependency, not just the last parent: a later NEW or
// sibling import can wait after an earlier helper/bypass was admitted. One fresh
// authority snapshot compares all actual groups before provider admission.
func (e *Engine) fenceRowSecurityProgramsLocked(s *StatementScope) error {
	if len(s.rlsPrograms) == 0 {
		return nil
	}
	if err := s.Op.Context().Err(); err != nil {
		return err
	}
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return err
	}
	auth.Recapture(e.PrivateCatalogLocked(s.Owner.Catalog))
	for _, p := range s.rlsPrograms {
		if !p.complete {
			return ids.ErrCatalogReplaced
		}
		current, ok := auth.Catalog.Relation(p.relation.Address())
		if !ok || current != p.relation {
			return ids.ErrCatalogReplaced
		}
		if p.roles != auth.Roles {
			next, err := access.SelectRowSecurity(auth.Roles, p.relation, p.principal, p.command, p.proposed, p.selectRead)
			if err != nil {
				return err
			}
			if !access.SameRLSSelection(p.selection, next) {
				return ids.ErrCatalogReplaced
			}
		}
	}
	if err := checkPolicyImportCommandAuthorityLocked(s, auth); err != nil {
		return err
	}
	// Preserve the original live invoker/rule principal, never a policy owner.
	routineAuth := auth
	if s.RoutineAuth.live() {
		routineAuth.State.CurrentUser = s.RoutineAuth.State.CurrentUser
	}
	if err := routineAuth.Roles.ValidatePrincipal(routineAuth.State.CurrentUser); err != nil {
		return err
	}
	s.RoutineAuth = routineAuth
	for _, p := range s.rlsPrograms {
		if p.selection.Count == 0 || p.selection.Denied() {
			continue
		}
		readmit := func(selected []access.RLSClause, runtime []exec.RLSRuntimeClause) error {
			for i, clause := range selected {
				fresh := auth
				fresh.State.CurrentUser = p.principal
				if err := view.CheckPolicyAuthority(fresh.View, fresh, p.relation, stored.PolicyOf(clause.Payload)); err != nil {
					return err
				}
				local := runtime[i].Evaluation
				local.Context = s.Op.Context()
				local.RoutineAuthority = &fresh
				e.Mu.Unlock()
				err = exec.AdmitExpression(&local, runtime[i].Expression, fresh.Catalog)
				e.Mu.Lock()
				if err != nil {
					return err
				}
			}
			return nil
		}
		for i := range p.selection.Count {
			if err := readmit(p.selection.Groups[i].Permissive, p.Groups[i].Permissive); err != nil {
				return err
			}
			if err := readmit(p.selection.Groups[i].Restrictive, p.Groups[i].Restrictive); err != nil {
				return err
			}
		}
	}
	for _, p := range s.rlsPrograms {
		p.roles = auth.Roles
	}
	return nil
}

// Only additional policy-import waits dirty the active original command.
// No no-wait recheck, historical ACL sweep, or cursor FETCH reauthorization.
func checkPolicyImportCommandAuthorityLocked(s *StatementScope, auth AuthorizationView) error {
	command := s.rlsCommand
	if command == nil || command.policyWaits == s.rlsImportWaits {
		return nil
	}
	auth.State.CurrentUser = command.auth.State.CurrentUser
	if err := analyze.CheckBinding(auth, command.binding, command.syntax); err != nil {
		return err
	}
	command.policyWaits = s.rlsImportWaits
	return nil
}
