package view

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
)

// CheckRowSecurityBinding applies the row_security setting guard to b's
// target, query sources and data-modifying CTEs, and to the rule commands of
// those CTEs under their own authority. rowSecurity is the session's
// row_security setting; when it is on there is nothing to check.
//
// Called under Engine.mu after identity/schema analysis, before execution ACLs.
// Bound occurrences and stored view identities are the existing analysis owners;
// no data capture, expression evaluation or catalog-wide discovery occurs here.
func CheckRowSecurityBinding(rowSecurity bool, v access.View, b analyze.Binding) error {
	if rowSecurity {
		return nil
	}
	if relation := PhysicalBinding(b).Relation; relation != nil {
		principal := access.RowSecurityPrincipal(v.State.CurrentUser, RowSecurityLayers(v.Catalog, b))
		if err := access.RowSecuritySettingGuard(false, v.Roles, relation, principal); err != nil {
			return err
		}
	}
	for _, leaf := range b.QuerySources {
		principal := leaf.Principal
		if principal == (authorization.PrincipalRef{}) {
			principal = v.State.CurrentUser
		}
		if err := CheckRowSecuritySource(v, leaf.Relation, principal, v.State.CurrentUser); err != nil {
			return err
		}
	}
	for _, definition := range b.CTEs {
		if mutation, ok := analyze.MutationBinding(definition); ok {
			caller := v
			if rewrite := RuleCTEsOf(b.Rule.CTEs)[definition]; rewrite != nil {
				caller = rewrite.Caller.Access()
			}
			if err := CheckRowSecurityBinding(rowSecurity, caller, *mutation); err != nil {
				return err
			}
			if rewrite := RuleCTEsOf(b.Rule.CTEs)[definition]; rewrite != nil {
				if err := CheckRowSecurityBinding(rowSecurity, rewrite.Command.Auth.Access(), rewrite.Command.Binding); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// CheckRowSecurityRuleProgram checks the original binding and every rule
// command of program, each under its own authority.
// Nonempty rule actions retain the caller's original range-table obligations.
// INSTEAD NOTHING has no executable query and therefore no such checks.
func CheckRowSecurityRuleProgram(rowSecurity bool, v access.View, original analyze.Binding, program RuleProgram) error {
	if rowSecurity || len(program.Commands) == 0 {
		return nil
	}
	if err := CheckRowSecurityBinding(rowSecurity, v, original); err != nil {
		return err
	}
	for _, command := range program.Commands {
		if err := CheckRowSecurityBinding(rowSecurity, command.Auth.Access(), command.Binding); err != nil {
			return err
		}
	}
	return nil
}
