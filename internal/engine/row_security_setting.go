package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

// Called under Engine.mu after identity/schema analysis, before execution ACLs.
// Bound occurrences and stored view identities are the existing analysis owners;
// no data capture, expression evaluation or catalog-wide discovery occurs here.
func checkRowSecurityBinding(scope *StatementScope, auth AuthorizationView, b analyze.Binding) error {
	if scope.rowSecurity {
		return nil
	}
	if relation := view.PhysicalBinding(b).Relation; relation != nil {
		principal := access.RowSecurityPrincipal(auth.State.CurrentUser, view.RowSecurityLayers(auth.Catalog, b))
		if err := access.RowSecuritySettingGuard(false, auth.Roles, relation, principal); err != nil {
			return err
		}
	}
	for _, leaf := range b.QuerySources {
		principal := leaf.Principal
		if principal == (authorization.PrincipalRef{}) {
			principal = auth.State.CurrentUser
		}
		if err := checkRowSecuritySource(auth, leaf.Relation, principal, auth.State.CurrentUser); err != nil {
			return err
		}
	}
	for _, definition := range b.CTEs {
		if mutation, ok := analyze.MutationBinding(definition); ok {
			caller := auth
			if rewrite := view.RuleCTEsOf(b.Rule.CTEs)[definition]; rewrite != nil {
				caller = ruleAuthorizationOf(rewrite.Caller)
			}
			if err := checkRowSecurityBinding(scope, caller, *mutation); err != nil {
				return err
			}
			if rewrite := view.RuleCTEsOf(b.Rule.CTEs)[definition]; rewrite != nil {
				if err := checkRowSecurityBinding(scope, ruleAuthorizationOf(rewrite.Command.Auth), rewrite.Command.Binding); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Nonempty rule actions retain the caller's original range-table obligations.
// INSTEAD NOTHING has no executable query and therefore no such checks.
func checkRowSecurityRuleProgram(scope *StatementScope, auth AuthorizationView, original analyze.Binding, program view.RuleProgram) error {
	if scope.rowSecurity || len(program.Commands) == 0 {
		return nil
	}
	if err := checkRowSecurityBinding(scope, auth, original); err != nil {
		return err
	}
	for _, command := range program.Commands {
		if err := checkRowSecurityBinding(scope, ruleAuthorizationOf(command.Auth), command.Binding); err != nil {
			return err
		}
	}
	return nil
}

// stored.ValidateViewReadGraph has already rejected active-path cycles. Each layer's
// invoker is the executing user, not the owner of an enclosing definer view.
func checkRowSecuritySource(auth AuthorizationView, relation *catalog.Relation, principal, invoker authorization.PrincipalRef) error {
	if relation.RelationKind != catalog.ViewRelation {
		return access.RowSecuritySettingGuard(false, auth.Roles, relation, principal)
	}
	principal = relation.Owner
	if stored.ViewOf(relation).Invoker() {
		principal = invoker
	}
	for _, source := range stored.ViewOf(relation).Sources() {
		child, ok := auth.Catalog.Relation(source.Ref.Address)
		if !ok || !stored.ViewReferenceValid(auth.Catalog, relation, source.Ref) {
			return ids.ErrCatalogReplaced
		}
		if err := checkRowSecuritySource(auth, child, principal, invoker); err != nil {
			return err
		}
	}
	return nil
}
