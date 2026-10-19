package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Called under Engine.mu after identity/schema analysis, before execution ACLs.
// Bound occurrences and stored view identities are the existing analysis owners;
// no data capture, expression evaluation or catalog-wide discovery occurs here.
func checkRowSecurityBinding(scope *StatementScope, auth AuthorizationView, b binding) error {
	if scope.rowSecurity {
		return nil
	}
	if relation := b.physicalBinding().relation; relation != nil {
		principal := access.RowSecurityPrincipal(auth.State.CurrentUser, rowSecurityLayers(auth.Catalog, b))
		if err := access.RowSecuritySettingGuard(false, auth.Roles, relation, principal); err != nil {
			return err
		}
	}
	for _, leaf := range b.querySources {
		principal := leaf.principal
		if principal == (authorization.PrincipalRef{}) {
			principal = auth.State.CurrentUser
		}
		if err := checkRowSecuritySource(auth, leaf.relation, principal, auth.State.CurrentUser); err != nil {
			return err
		}
	}
	for _, definition := range b.ctes {
		if definition.mutation != nil {
			caller := auth
			if rewrite := b.ruleCTEs[definition]; rewrite != nil {
				caller = rewrite.caller
			}
			if err := checkRowSecurityBinding(scope, caller, *definition.mutation); err != nil {
				return err
			}
			if rewrite := b.ruleCTEs[definition]; rewrite != nil {
				if err := checkRowSecurityBinding(scope, rewrite.command.auth, rewrite.command.binding); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Nonempty rule actions retain the caller's original range-table obligations.
// INSTEAD NOTHING has no executable query and therefore no such checks.
func checkRowSecurityRuleProgram(scope *StatementScope, auth AuthorizationView, original binding, program ruleProgram) error {
	if scope.rowSecurity || len(program.commands) == 0 {
		return nil
	}
	if err := checkRowSecurityBinding(scope, auth, original); err != nil {
		return err
	}
	for _, command := range program.commands {
		if err := checkRowSecurityBinding(scope, command.auth, command.binding); err != nil {
			return err
		}
	}
	return nil
}

// validateViewReadGraph has already rejected active-path cycles. Each layer's
// invoker is the executing user, not the owner of an enclosing definer view.
func checkRowSecuritySource(auth AuthorizationView, relation *catalog.Relation, principal, invoker authorization.PrincipalRef) error {
	if relation.RelationKind != catalog.ViewRelation {
		return access.RowSecuritySettingGuard(false, auth.Roles, relation, principal)
	}
	principal = relation.Owner
	if viewOf(relation).invoker {
		principal = invoker
	}
	for _, source := range viewOf(relation).sources {
		child, ok := auth.Catalog.Relation(source.ref.Address)
		if !ok || !storedViewReferenceValid(auth.Catalog, relation, source.ref) {
			return ids.ErrCatalogReplaced
		}
		if err := checkRowSecuritySource(auth, child, principal, invoker); err != nil {
			return err
		}
	}
	return nil
}
