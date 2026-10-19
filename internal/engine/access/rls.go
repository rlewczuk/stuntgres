package access

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

// RLSDisposition is the row-security outcome for one relation and command:
// no restriction, default deny, or policy expressions to enforce.
type RLSDisposition uint8

const (
	RLSUnrestricted RLSDisposition = iota
	RLSDefaultDeny
	RLSNeedsExpressions
)

// Applicability is metadata only. Even TRUE/FALSE/NULL syntax requires the
// adjacent expression-enforcement capability; nil clauses contribute nothing.
func RowSecurityApplies(roles authorization.Read, d *catalog.Relation, principal authorization.PrincipalRef) (bool, error) {
	if !d.Security.RLSEnabled {
		return false, nil
	}
	actor, err := roles.Descriptor(principal)
	if err != nil {
		return false, err
	}
	if actor.Superuser || actor.BypassRLS {
		return false, nil
	}
	if !d.Security.RLSForced {
		owner, err := roles.InheritsRole(principal, d.Owner)
		if err != nil {
			return false, err
		}
		if owner {
			return false, nil
		}
	}
	return true, nil
}

// RowSecuritySettingGuard rejects queries whose logical result would depend on
// RLS when the executing session disables policy application. It never bypasses
// policies and deliberately leaves the metadata-only applicability API intact.
func RowSecuritySettingGuard(enabled bool, roles authorization.Read, d *catalog.Relation, principal authorization.PrincipalRef) error {
	if enabled {
		return nil
	}
	applies, err := RowSecurityApplies(roles, d, principal)
	if err != nil || !applies {
		return err
	}
	owner, err := roles.InheritsRole(principal, d.Owner)
	if err != nil {
		return err
	}
	diagnostic := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`query would be affected by row-level security policy for table "%s"`, d.Name())}
	if owner {
		diagnostic.Hint = "To disable the policy for the table's owner, use ALTER TABLE NO FORCE ROW LEVEL SECURITY."
	}
	return diagnostic
}

// RowSecurityDecision selects the permissive policies of d that apply to
// principal for command. proposed selects the WITH CHECK side for new rows.
func RowSecurityDecision(roles authorization.Read, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, proposed bool) (RLSDisposition, error) {
	applies, err := RowSecurityApplies(roles, d, principal)
	if err != nil || !applies {
		return RLSUnrestricted, err
	}
	for _, policy := range d.Security.Policies {
		if !policy.Permissive || policy.Command != catalog.PolicyAll && policy.Command != command {
			continue
		}
		expression := policy.Using
		if proposed && command != catalog.PolicySelect {
			expression = policy.Check
			if expression == nil && (policy.Command == catalog.PolicyAll || policy.Command == catalog.PolicyUpdate) {
				expression = policy.Using
			}
		}
		if expression == nil {
			continue
		}
		for _, role := range policy.Roles {
			if role.Public {
				return RLSNeedsExpressions, nil
			}
			applies, err := roles.InheritsRole(principal, role.Ref)
			if err != nil {
				return RLSUnrestricted, err
			}
			if applies {
				return RLSNeedsExpressions, nil
			}
		}
	}
	return RLSDefaultDeny, nil
}

// RowSecurityGroups is RowSecurityDecision for command, combined with the
// SELECT decision when the command also reads rows (selectRead).
func RowSecurityGroups(roles authorization.Read, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, proposed, selectRead bool) (RLSDisposition, error) {
	decision, err := RowSecurityDecision(roles, d, principal, command, proposed)
	if err != nil || !selectRead || command == catalog.PolicySelect {
		return decision, err
	}
	read, err := RowSecurityDecision(roles, d, principal, catalog.PolicySelect, proposed)
	if err != nil {
		return RLSUnrestricted, err
	}
	if decision == RLSDefaultDeny || read == RLSDefaultDeny {
		return RLSDefaultDeny, nil
	}
	if decision == RLSNeedsExpressions || read == RLSNeedsExpressions {
		return RLSNeedsExpressions, nil
	}
	return RLSUnrestricted, nil
}

// RowSecurityExpressionError reports that policy expressions must be enforced,
// which is unsupported.
func RowSecurityExpressionError() error {
	return sqlError("0A000", "row-security policy expression enforcement is unsupported")
}

// RowSecurityProposal returns the error for a proposed new row of table under
// decision, or nil when the row is unrestricted.
func RowSecurityProposal(decision RLSDisposition, table string) error {
	switch decision {
	case RLSDefaultDeny:
		return sqlError("42501", fmt.Sprintf(`new row violates row-level security policy for table "%s"`, table))
	case RLSNeedsExpressions:
		return RowSecurityExpressionError()
	default:
		return nil
	}
}

// RowSecurityLayer is one view layer of an auto-updatable view write, from
// the outermost view inward: the view's owner and whether it runs with
// security_invoker.
type RowSecurityLayer struct {
	Owner   authorization.PrincipalRef
	Invoker bool
}

// RowSecurityPrincipal returns the role whose policies apply to the base
// relation: the innermost view's owner, or current for an invoker view or a
// direct write.
func RowSecurityPrincipal(current authorization.PrincipalRef, layers []RowSecurityLayer) authorization.PrincipalRef {
	principal := current
	for _, layer := range layers {
		principal = layer.Owner
		if layer.Invoker {
			principal = current
		}
	}
	return principal
}
