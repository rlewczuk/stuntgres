package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

type rlsDisposition uint8

const (
	rlsUnrestricted rlsDisposition = iota
	rlsDefaultDeny
	rlsNeedsExpressions
)

// Applicability is metadata only. Even TRUE/FALSE/NULL syntax requires the
// adjacent expression-enforcement capability; nil clauses contribute nothing.
func rowSecurityApplies(roles authorization.Read, d *catalog.Relation, principal authorization.PrincipalRef) (bool, error) {
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

func rowSecurityDecision(roles authorization.Read, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, proposed bool) (rlsDisposition, error) {
	applies, err := rowSecurityApplies(roles, d, principal)
	if err != nil || !applies {
		return rlsUnrestricted, err
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
				return rlsNeedsExpressions, nil
			}
			applies, err := roles.InheritsRole(principal, role.Ref)
			if err != nil {
				return rlsUnrestricted, err
			}
			if applies {
				return rlsNeedsExpressions, nil
			}
		}
	}
	return rlsDefaultDeny, nil
}

func rowSecurityGroups(roles authorization.Read, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, proposed, selectRead bool) (rlsDisposition, error) {
	decision, err := rowSecurityDecision(roles, d, principal, command, proposed)
	if err != nil || !selectRead || command == catalog.PolicySelect {
		return decision, err
	}
	read, err := rowSecurityDecision(roles, d, principal, catalog.PolicySelect, proposed)
	if err != nil {
		return rlsUnrestricted, err
	}
	if decision == rlsDefaultDeny || read == rlsDefaultDeny {
		return rlsDefaultDeny, nil
	}
	if decision == rlsNeedsExpressions || read == rlsNeedsExpressions {
		return rlsNeedsExpressions, nil
	}
	return rlsUnrestricted, nil
}

func rowSecurityExpressionError() error {
	return sqlError("0A000", "row-security policy expression enforcement is unsupported")
}

func rowSecurityProposal(decision rlsDisposition, table string) error {
	switch decision {
	case rlsDefaultDeny:
		return sqlError("42501", fmt.Sprintf(`new row violates row-level security policy for table "%s"`, table))
	case rlsNeedsExpressions:
		return rowSecurityExpressionError()
	default:
		return nil
	}
}

// rowSecurityLayer is one view layer of an auto-updatable view write, from
// the outermost view inward: the view's owner and whether it runs with
// security_invoker.
type rowSecurityLayer struct {
	owner   authorization.PrincipalRef
	invoker bool
}

// rowSecurityPrincipal returns the role whose policies apply to the base
// relation: the innermost view's owner, or current for an invoker view or a
// direct write.
func rowSecurityPrincipal(current authorization.PrincipalRef, layers []rowSecurityLayer) authorization.PrincipalRef {
	principal := current
	for _, layer := range layers {
		principal = layer.owner
		if layer.invoker {
			principal = current
		}
	}
	return principal
}
